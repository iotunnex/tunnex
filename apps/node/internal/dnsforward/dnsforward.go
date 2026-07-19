// Package dnsforward is the in-agent cross-site DNS forwarder (S8.4 D2). It answers ONLY for the org's
// declared zones (the compiled {domain -> resolver_ip} table) by relaying the query to that zone's internal
// resolver over the tunnel; everything else is REFUSED (split-horizon — the client's scoped-resolver config
// only sends matched-domain queries here). It is a small Go forwarder inside the agent's lifecycle — no
// dnsmasq/CoreDNS process (external daemons are the EPIC-9 pattern).
//
// Discipline (S8.4 rulings):
//   - BIND SCOPE (D2): binds ONLY wg-facing addresses — never a public interface. An open resolver on a
//     cloud VM is an abuse vector. wgBindAddrs derives the bind set from the wg interface alone.
//   - SKIP-DEGRADED (D2): a malformed table entry is skipped with a logged warning; the rest serve. One
//     typo never takes down every site's names.
//   - fail-static / SERVFAIL (D2): resolver unreachable -> SERVFAIL, the tunnel is untouched, the last-good
//     table is retained. DNS-down is NEVER tunnel-down.
//   - RATE LIMIT (D2): per-source token bucket as cheap hygiene.
//   - CONVENIENCE (D5): the table is out-of-hash plumbing; a forwarder fault never fails the reconcile.
package dnsforward

import (
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// forward is one compiled, validated forwarding rule: queries whose QNAME is within Domain go to Resolver.
type forward struct {
	domain   string     // normalized fqdn, lowercase, trailing dot (e.g. "corp.local.")
	resolver netip.Addr // the site's internal resolver, validated in-subnet at write time (D7)
}

// table is the immutable compiled forwarding set — swapped atomically on each SetTable.
type table struct {
	rules []forward
}

// match returns the resolver for the longest domain suffix covering qname (fqdn, lowercased), or ok=false.
// Longest-match so a more specific zone (a.corp.local) wins over a broader one (corp.local).
func (t *table) match(qname string) (netip.Addr, bool) {
	q := strings.ToLower(qname)
	if !strings.HasSuffix(q, ".") {
		q += "."
	}
	best := -1
	var res netip.Addr
	for _, r := range t.rules {
		if q == r.domain || strings.HasSuffix(q, "."+r.domain) {
			if len(r.domain) > best {
				best, res = len(r.domain), r.resolver
			}
		}
	}
	return res, best >= 0
}

// Entry mirrors nodepolicy.DNSForward without importing it (the reconcile loop maps the compiled slice in).
type Entry struct {
	Domain     string
	ResolverIP string
}

// buildTable compiles entries, SKIP-DEGRADED: a malformed domain or resolver IP is skipped + logged, the
// rest compile (D2 — one typo must not blank every zone). Dedup is a WRITE-time concern (D1 overlap
// refusal); here we keep the last for a repeated domain, deterministically.
func buildTable(entries []Entry, log *slog.Logger) *table {
	seen := map[string]int{}
	var rules []forward
	for _, e := range entries {
		d := normalizeDomain(e.Domain)
		ip, err := netip.ParseAddr(e.ResolverIP)
		if d == "" || err != nil || !ip.IsValid() {
			if log != nil {
				log.Warn("dns_forward_entry_skipped", "domain", e.Domain, "resolver_ip", e.ResolverIP)
			}
			continue
		}
		if i, ok := seen[d]; ok {
			rules[i].resolver = ip // last-wins, deterministic
			continue
		}
		seen[d] = len(rules)
		rules = append(rules, forward{domain: d, resolver: ip})
	}
	return &table{rules: rules}
}

// normalizeDomain lowercases + ensures a single trailing dot; returns "" for an empty/invalid label set.
func normalizeDomain(d string) string {
	d = strings.TrimSpace(strings.ToLower(d))
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.Contains(d, " ") {
		return ""
	}
	for _, lbl := range strings.Split(d, ".") {
		if lbl == "" {
			return "" // empty label (e.g. "a..b") is malformed
		}
	}
	return d + "."
}

// exchangeFn relays a raw DNS query to a resolver and returns the raw response. Injectable so the forward
// logic (match/SERVFAIL/rate-limit) is unit-testable without real sockets. The real one is a UDP round-trip.
type exchangeFn func(resolver netip.Addr, query []byte) ([]byte, error)

// Forwarder holds the atomic table + a per-source rate limiter. Serve() (real UDP) is thin over handle().
type Forwarder struct {
	tbl      atomic.Pointer[table]
	exchange exchangeFn
	log      *slog.Logger

	// F1 lifecycle seams (nil → real). Serve runs a BIND-RECONCILE loop that re-reads the wg interface's
	// addresses every tick and reconciles its live listeners to match — so it binds when wg0 appears
	// (it does NOT exist at agent boot; the reconcile loop creates it later), re-binds after a flap, and
	// closes listeners when an address goes. bindSource/listen/bindInterval are injectable for the red.
	bindSource   func(string) ([]netip.Addr, error) // nil → wgBindAddrs
	listen       func(netip.Addr) (udpListener, error)
	bindInterval time.Duration // 0 → 5s

	mu       sync.Mutex
	buckets  map[netip.Addr]*bucket // per-source token buckets
	now      func() time.Time       // injectable clock for the rate-limit red (nil → time.Now)
	lastSweep time.Time             // F7: last idle-bucket eviction pass (zero → sweep on first allow)
}

// New builds a Forwarder with the real UDP exchange unless one is injected (tests pass a fake).
func New(log *slog.Logger, exchange exchangeFn) *Forwarder {
	if exchange == nil {
		exchange = udpExchange
	}
	f := &Forwarder{exchange: exchange, log: log, buckets: map[netip.Addr]*bucket{}}
	f.tbl.Store(&table{})
	return f
}

// SetTable recompiles + atomically swaps the forwarding table (called each reconcile tick). fail-static: a
// swap NEVER errors — a bad entry was already dropped by buildTable, so the last-good rest stays serving.
func (f *Forwarder) SetTable(entries []Entry) { f.tbl.Store(buildTable(entries, f.log)) }

// handle answers one query from src. Not-in-table -> REFUSED (scoped). In-table -> relay to the resolver;
// resolver error -> SERVFAIL (fail-static, tunnel untouched). Over the rate limit -> dropped (nil, no reply).
func (f *Forwarder) handle(query []byte, src netip.Addr) []byte {
	if !f.allow(src) {
		return nil // rate-limited: drop silently
	}
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil // unparseable — not ours to answer
	}
	q, err := p.Question()
	if err != nil {
		return refuse(hdr.ID, query)
	}
	res, ok := f.tbl.Load().match(q.Name.String())
	if !ok {
		return refuse(hdr.ID, query) // out of scope — split-horizon: the host's own resolver handles it
	}
	resp, err := f.exchange(res, query)
	if err != nil {
		if f.log != nil {
			f.log.Warn("dns_forward_upstream_failed", "resolver", res.String(), "qname", q.Name.String(), "error", err.Error())
		}
		return servfail(hdr.ID, query) // resolver unreachable -> SERVFAIL; tunnel + last-good table untouched
	}
	return resp
}

// refuse / servfail build a minimal response echoing the query's question with the given RCode, so a client
// gets a definite answer (not a timeout). Best-effort: on a pack error, no reply.
func refuse(id uint16, query []byte) []byte  { return respondRCode(id, query, dnsmessage.RCodeRefused) }
func servfail(id uint16, query []byte) []byte { return respondRCode(id, query, dnsmessage.RCodeServerFailure) }

func respondRCode(id uint16, query []byte, rcode dnsmessage.RCode) []byte {
	var p dnsmessage.Parser
	if _, err := p.Start(query); err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, Response: true, RCode: rcode, RecursionAvailable: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil
	}
	if err := b.Question(q); err != nil {
		return nil
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}
