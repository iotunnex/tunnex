// Package nodes is the control-plane side of the tunnex-node agent: join-token
// enrollment, cert-identity authorization, short-lived-cert renewal (the
// revocation mechanism), and the desired-state the agent reconciles toward.
package nodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/agentca"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/ipalloc"
	"github.com/tunnexio/tunnex/apps/api/internal/pgerr"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
	"github.com/tunnexio/tunnex/apps/api/internal/wgkey"
)

// ProtocolVersion is the control-plane protocol version, kept in lockstep with
// policyspec.ProtocolVersion (TestProtocolVersionConstantsAgree). v2 (S7.5.1): rule_id.
// v3 (S7.5.4): src_device_id — both additive + hash-invisible. v4 (S8.1 Slice 3): sites as a
// destination kind — Option A, no new wire field, but Version IS in-hash so v4 is a real hash change,
// and S8.1 D1's agent gate makes an agent at maxSupported<4 REFUSE it rather than mis-enforce (the
// v4 bump is no longer "safe to safe-ignore" — it is the enforcement boundary the gate protects).
// v6 (A3b, S8.6): pool_cidr on the site-gateway artifact (device-pool Docker accepts) — an old agent
// would silently strand device transit on Docker hosts, so the gate refuses (lockstep with policyspec).
const ProtocolVersion = 6

const joinTokenTTL = time.Hour

// defaultGatewayCIDR is the interface address used when an org's pool can't be
// read (soft-deleted org / invalid CIDR) — matches the default pool's gateway so
// desired-state fetches degrade gracefully instead of failing.
const defaultGatewayCIDR = "10.99.0.1/24"

// Peer is one WireGuard peer in a node's desired state. S3.2 populates these;
// S3.1 carries the shape so the reconcile protocol is complete.
type Peer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
	Endpoint   string   `json:"endpoint,omitempty"`
	// SiteLink (S8.2) marks a gateway-DIALED site-link peer whose Endpoint is control-plane-managed
	// (static), NOT a roaming client. The agent's peer dirty-check compares Endpoint only for these (B4),
	// and reports their handshake staleness for the site-link health surface (H5). Device peers roam →
	// SiteLink=false → endpoint-blind.
	SiteLink bool `json:"site_link,omitempty"`
	// PersistentKeepalive (S8.3 CK, seconds) keeps a site-link tunnel warm through NAT: a NAT'd spoke
	// must dial the hub, and an idle link would otherwise false-stale (H5 site_link_down from mere
	// idleness). Set only on SITE-LINK peers (CP intent); 0 (omitted) on roaming device peers, which
	// re-handshake on demand. The agent compares it for SiteLink peers so a change re-syncs (Slice 0).
	PersistentKeepalive int `json:"persistent_keepalive,omitempty"`
}

// DesiredState is what an agent should converge its interface to. Version lets
// the agent detect changes cheaply; ProtocolVersion gates compatibility.
type DesiredState struct {
	ProtocolVersion  int    `json:"protocol_version"`
	NodeID           string `json:"node_id"`
	InterfaceAddress string `json:"interface_address"` // TODO(S3.5): from the org pool allocator
	MTU              int    `json:"mtu"`               // explicit, never inherited from ambient
	ListenPort       int    `json:"listen_port"`
	// Version is the node's push change-version at fetch time; the agent echoes it
	// on the next watch so a change during the fetch gap is not missed.
	Version uint64 `json:"version"`
	Peers   []Peer `json:"peers"`
	// Policy is the compiled Zero Trust policy (S7.2). Omitted in the open build
	// (nil provider) and when no provider is wired -> the agent decodes nil and
	// keeps the legacy blanket mesh (its asserted absent=mesh default).
	Policy *policyspec.Compiled `json:"policy,omitempty"`
}

// PolicyProvider compiles the Zero Trust policy artifact for one node (S7.2).
// nil in the open build (no policy field is ever sent -> agents keep the legacy
// mesh); the enterprise build wires the policy engine via SetPolicyProvider.
type PolicyProvider interface {
	// activeHub (S8.6 REDUCE #1) is the DERIVED active transit hub for this compile pass, computed ONCE by
	// the caller (electSiteHub over the same loaded topology that feeds the data-plane graph) and threaded
	// in so the policy transit grant lands on the SAME hub the routing cites. uuid.Nil = no hub.
	CompiledForNode(ctx context.Context, orgID, nodeID, activeHub uuid.UUID) (*policyspec.Compiled, error)
	// CompiledArtifactsForNodes returns each node's ROUTE-LESS compiled artifact with a SINGLE org
	// snapshot build — the batch counterpart to CompiledForNode. Route-less by design: the CORE
	// finalizeArtifact/pushedHash attach the site routes + derive the version, so the pushed-hash
	// baseline is computed the SAME way the served artifact is (the #1 single-source fix). nil for a node
	// with no enforcement artifact (off / device-less-off). Avoids an N+1 recompile per node (finding #5).
	// activeHub threaded as above (org-wide — one hub for the batch).
	CompiledArtifactsForNodes(ctx context.Context, orgID uuid.UUID, nodeIDs []uuid.UUID, activeHub uuid.UUID) (map[uuid.UUID]*policyspec.Compiled, error)
}

// Service provides node control-plane operations.
type Service struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	ca     *agentca.CA
	policy PolicyProvider // nil => open build / not wired
	// sealer supplies the keyed proof-of-secret fingerprint (S4.5 convention)
	// written to the join-token audit rows, so issuance and redemption correlate
	// without the raw token ever entering the audit stream.
	sealer *crypto.Sealer
	// siteTopoLoad loads the S8.2 site topology. Defaults to loadSiteTopology; a test overrides it to
	// inject a fault, proving the DesiredState-ATOMIC contract (a topology error fails the whole fetch).
	siteTopoLoad func(context.Context, uuid.UUID) (siteTopology, error)
	// failovers holds the per-org in-memory hysteresis state for the S8.6 failover tick — rebuilt from
	// stored freshness on a CP restart (no persistence for state the substrate re-derives). Guarded by
	// failoverMu (the tick runs on a background goroutine).
	failovers  map[uuid.UUID]*FailoverController
	failoverMu sync.Mutex
}

// NewService builds the node service.
func NewService(pool *pgxpool.Pool, ca *agentca.CA, sealer *crypto.Sealer) *Service {
	s := &Service{pool: pool, q: sqlc.New(pool), ca: ca, sealer: sealer, failovers: map[uuid.UUID]*FailoverController{}}
	s.siteTopoLoad = s.loadSiteTopology
	return s
}

// SetPolicyProvider wires the enterprise policy engine (S7.2). Call before serving.
func (s *Service) SetPolicyProvider(p PolicyProvider) { s.policy = p }

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IssueJoinToken mints a single-use enrollment token for an org, optionally
// pinning a node name.
func (s *Service) IssueJoinToken(ctx context.Context, actor, orgID uuid.UUID, nodeName string) (string, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}
	var namePin *string
	if nodeName != "" {
		namePin = &nodeName
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.CreateJoinToken(ctx, sqlc.CreateJoinTokenParams{
			OrgID: orgID, NodeName: namePin, TokenHash: hash, ExpiresAt: time.Now().Add(joinTokenTTL),
		}); e != nil {
			return e
		}
		// Keyed fingerprint (never the raw token, never a bare hash) so this row
		// correlates with the node.enrolled row that redeems the same token.
		return audit(ctx, q, orgID, &actor, "node.token_issued", "node", nodeName,
			map[string]any{"node_name": nodeName, "token_fingerprint": s.sealer.Fingerprint([]byte(raw))})
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// EnrollResult is returned to a newly-enrolled agent.
type EnrollResult struct {
	NodeID  string
	CertPEM string
	CAPEM   string
}

// Enroll consumes a join token and issues the agent's first certificate. The
// token is single-use; the cert serial becomes the node's identity.
func (s *Service) Enroll(ctx context.Context, rawToken, csrPEM, nodeName, agentVersion string) (EnrollResult, error) {
	var res EnrollResult
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		tok, e := q.ConsumeJoinToken(ctx, hashToken(rawToken))
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.New(401, "invalid_join_token", "the join token is invalid, used, or expired")
		}
		if e != nil {
			return e
		}
		if tok.NodeName != nil && *tok.NodeName != "" {
			if nodeName != "" && nodeName != *tok.NodeName {
				return apierr.BadRequest("node_name_mismatch", "this token is pinned to a different node name")
			}
			nodeName = *tok.NodeName
		}
		if nodeName == "" {
			return apierr.BadRequest("node_name_required", "a node name is required")
		}
		certPEM, serial, e := s.ca.SignCSR([]byte(csrPEM), nodeName)
		if e != nil {
			return apierr.BadRequest("invalid_csr", "could not sign the certificate request")
		}
		node, e := q.CreateNode(ctx, sqlc.CreateNodeParams{OrgID: tok.OrgID, Name: nodeName, CertSerial: serial, AgentVersion: agentVersion})
		if e != nil {
			if pgerr.IsUnique(e) {
				return apierr.Conflict("node_exists", "a node with this name already exists")
			}
			return e
		}
		res = EnrollResult{NodeID: node.ID.String(), CertPEM: certPEM, CAPEM: string(s.ca.CertPEM())}
		// Same keyed fingerprint as the node.token_issued row — issue and redeem
		// correlate in the audit stream without the raw token appearing anywhere.
		return audit(ctx, q, tok.OrgID, nil, "node.enrolled", "node", node.ID.String(),
			map[string]any{"name": nodeName, "agent_version": agentVersion, "token_fingerprint": s.sealer.Fingerprint([]byte(rawToken))})
	})
	if err != nil {
		return EnrollResult{}, err
	}
	return res, nil
}

// AuthenticateCert maps an mTLS client cert serial to its node, rejecting
// unknown or revoked certs. This is the machine-edition identity↔credential rule.
func (s *Service) AuthenticateCert(ctx context.Context, certSerial string) (sqlc.Node, error) {
	node, err := s.q.GetNodeByCertSerial(ctx, certSerial)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Node{}, apierr.New(401, "unknown_agent", "unrecognized agent certificate")
	}
	if err != nil {
		return sqlc.Node{}, err
	}
	if node.Status != "active" {
		return sqlc.Node{}, apierr.New(401, "agent_revoked", "this agent has been revoked")
	}
	return node, nil
}

// Renew issues a fresh short-lived cert for an active node. A revoked node is
// refused — this IS the revocation mechanism (short certs + renewal refusal).
func (s *Service) Renew(ctx context.Context, node sqlc.Node, csrPEM, agentVersion string) (string, error) {
	if node.Status != "active" {
		return "", apierr.New(401, "agent_revoked", "this agent has been revoked")
	}
	certPEM, serial, err := s.ca.SignCSR([]byte(csrPEM), node.Name)
	if err != nil {
		return "", apierr.BadRequest("invalid_csr", "could not sign the certificate request")
	}
	if err := s.q.RenewNodeCert(ctx, sqlc.RenewNodeCertParams{ID: node.ID, CertSerial: serial, AgentVersion: agentVersion}); err != nil {
		return "", err
	}
	return certPEM, nil
}

// DesiredState returns the interface config + peers the agent should converge
// to: one Peer per active device owned by an active user, each with its assigned
// /32 as AllowedIPs. The interface address is the org pool's gateway (S3.5);
// MTU is explicit (WireGuard's default 1420).
func (s *Service) DesiredState(ctx context.Context, node sqlc.Node) (DesiredState, error) {
	_ = s.q.TouchNodeSeen(ctx, node.ID)
	rows, err := s.q.ListActivePeersForNode(ctx, node.ID)
	if err != nil {
		return DesiredState{}, err
	}
	peers := make([]Peer, 0, len(rows))
	for _, r := range rows {
		p := Peer{PublicKey: r.PublicKey}
		// AllowedIPs is the peer's assigned tunnel address (its /32). A device with
		// no address yet (shouldn't happen post-S3.4 allocation) carries no routes.
		if r.AssignedIp != nil && *r.AssignedIp != "" {
			p.AllowedIPs = []string{*r.AssignedIp + "/32"}
		}
		peers = append(peers, p)
	}
	// The interface address is the pool gateway (first usable host) with the
	// pool's prefix, so the server has an on-link route to the whole pool and can
	// route peer traffic. Derived from the org pool (S3.5). If the org row is
	// unavailable (e.g. soft-deleted) or its CIDR is somehow invalid, fall back to
	// the default pool rather than failing the whole fetch — the agent must still
	// be able to converge (e.g. to drop peers), not spin on errors.
	gatewayCIDR := defaultGatewayCIDR
	org, orgErr := s.q.GetOrganizationByID(ctx, node.OrgID)
	if orgErr == nil {
		if gw, gerr := ipalloc.GatewayCIDR(org.PoolCidr); gerr == nil {
			gatewayCIDR = gw
		}
	}
	ds := DesiredState{
		ProtocolVersion:  ProtocolVersion,
		NodeID:           node.ID.String(),
		InterfaceAddress: gatewayCIDR,
		MTU:              1420,
		ListenPort:       51820,
		Peers:            peers,
	}
	// S8.6 REDUCE #1: load the site topology ONCE up front (site nodes only) and derive the active hub from
	// it BEFORE the policy compile, so the policy transit grant and the data-plane site-link graph cite the
	// SAME hub (one derivation per compile pass, fed to both). A non-site node loads no topology and passes
	// activeHub=Nil — no site→site transit grant lands on a non-gateway node either way. The load moved up
	// from the S8.2 block below; its DesiredState-ATOMIC failure semantics are preserved byte-for-byte.
	var topo siteTopology
	var haveTopo bool
	var activeHub uuid.UUID
	if node.SiteID.Valid {
		load := s.siteTopoLoad
		if load == nil { // directly-constructed Service (tests) → the real loader
			load = s.loadSiteTopology
		}
		t, terr := load(ctx, node.OrgID)
		if terr != nil {
			// DesiredState-ATOMIC LAW (original, unamended): a multi-section artifact assembly error FAILS
			// THE WHOLE FETCH — never a partial artifact that reads whole. The agent's standing FAIL-STATIC
			// contract (since S3.1) then holds LAST-GOOD everything, so nothing (peers, routes, policy) is
			// torn down across the blip. A topology-query error is the SAME class as any other DesiredState
			// query error — a DB fault marginally widening the fetch's failure surface, NOT a new coupling
			// of revocation to sites: revocation rides the push path, and a push landing during a DB
			// outage always waited. (The R1 "omit the section" attempt was WRONG — full-sweep reconcile
			// DELETES an omitted section, tearing down the live site path; F1. The security-precedence
			// amendment is withdrawn: it manufactured partial sections that full-sweep cannot survive.)
			return DesiredState{}, terr
		}
		topo, haveTopo = t, true
		if h := electSiteHub(topo, time.Now()); h != nil { // the ONE derivation head, fed to policy + graph
			activeHub = h.ID
		}
		// WF-A D-WFA-5b — device-peer HOSTING (the companion to endpoint-derivation). A device assigned to a
		// HUB-SET MEMBER is hosted on EVERY member's DesiredState, so the promoted hub already knows the
		// device when the re-homed dial lands (without this, ListActivePeersForNode's node_id scoping means
		// the promoted hub lacks the device → the dial handshake fails → (C) is a half-fix). On the ACTIVE
		// PRIMARY the device peer carries its /32 (crypto-routes the device); on a STANDBY it is WARM (empty
		// AllowedIPs — pubkey known so the handshake completes, the /32 rides the active-primary recompile on
		// promotion, mirroring the site-link single-valued invariant). A device on a NON-member gateway is
		// UNCHANGED (its own /32; it dials its own gateway — the spoke-device gap stays deferred).
		members := activeHubMembers(topo, time.Now())
		isMember, thisIsPrimary := false, false
		memberIDs := make([]uuid.UUID, 0, len(members))
		for i := range members {
			memberIDs = append(memberIDs, members[i].ID)
			if members[i].ID == node.ID {
				isMember, thisIsPrimary = true, i == 0
			}
		}
		if isMember {
			wp, werr := s.widenedDevicePeers(ctx, memberIDs, thisIsPrimary)
			if werr != nil {
				return DesiredState{}, werr // DesiredState-ATOMIC: a widening query fault fails the whole fetch
			}
			ds.Peers = wp // REPLACE the node's own /32 device peers with the union (site-link peers append below)
		}
	}

	if s.policy != nil {
		pol, err := s.policy.CompiledForNode(ctx, node.OrgID, node.ID, activeHub)
		switch {
		case err == nil:
			ds.Policy = pol
		case orgErr == nil && org.ZeroTrustMode == zeroTrustOff:
			// A policy-subsystem error must NOT fail the whole desired state — the PEERS
			// are already built above, so revocation still converges (the <5s SLA is
			// independent of the policy engine, finding #3). Scoping (finding #2): when we
			// can CONFIRM the org has Zero Trust OFF, serve the mesh — a policy-subsystem
			// blip must not blackhole an org that never opted into enforcement. We leave
			// ds.Policy nil (the agent decodes nil = blanket mesh, and onPolicy fires on nil
			// to unset any prior policy). nil matches the compiler's off-mode output for a
			// DEVICE-LESS node exactly (CompiledForNode returns nil there), so the pushed/
			// applied hashes stay "" and PolicyDegradedForNodes never false-alarms (finding
			// #C — a non-nil mesh artifact here diverged from that nil and read as degraded).
			slog.Warn("policy_compile_failed_org_off_serving_mesh",
				slog.String("node_id", node.ID.String()), slog.String("error", err.Error()))
			// ds.Policy stays nil.
		default:
			// Enforcing, OR the org mode is UNKNOWN (org row unreadable): FAIL CLOSED. An
			// enforcing org must never revert to the open mesh on a policy error, and if we
			// cannot confirm the mode we assume the boundary is in force. Serve the peers;
			// lock the policy to a deny-all enforcing artifact identical to the compiler's
			// device-less enforcing fallback — SAME policyspec.ProtocolVersion (finding #D:
			// nodes.ProtocolVersion is a different constant; using it would fork the hash
			// from CompiledForNode's and false-alarm every fail-closed gateway). (nil would
			// decode as mesh = fail-OPEN.)
			slog.Warn("policy_compile_failed_failing_closed",
				slog.String("node_id", node.ID.String()), slog.String("error", err.Error()))
			// Content-derived version (S8.2 D1b): a deny-all has an empty Allow → RequiredVersion == 4,
			// byte-identical to the compiler's device-less enforcing fallback for the SAME node, so the
			// pushed/applied hashes still agree (finding #D preserved — no fork).
			ds.Policy = &policyspec.Compiled{
				Version: policyspec.RequiredVersion(policyspec.Compiled{Mode: "enforcing"}),
				NodeID:  node.ID.String(), Mode: "enforcing", Mesh: false,
			}
		}
	}

	// S8.2 site-to-site plumbing (CORE, all editions): if this node is a site gateway, add its site-link WG
	// peers (hub-and-spoke) + kernel routes, from the org's site topology loaded ONCE above. finalizeArtifact
	// is the SINGLE SOURCE that attaches routes + derives the content version — the SAME step the pushed-hash
	// path (trackDesync / PolicyHealthForNodes) calls, so the served artifact and the desync baseline can
	// NEVER disagree about the artifact's contents (the #1 fix: two compile paths agreeing).
	if haveTopo {
		peers, _ := siteLinkGraphFrom(topo, node)
		ds.Peers = append(ds.Peers, peers...)
		ds.Policy = s.finalizeArtifact(topo, node, ds.Policy)
	}
	return ds, nil
}

// widenedDevicePeers is WF-A D-WFA-5b's device-peer hosting: the UNION of device peers across all hub-set
// members (deduped by pubkey), so a device assigned to any member is present on every member. thisIsPrimary
// decides AllowedIPs: the ACTIVE PRIMARY carries each device's /32 (crypto-routing); a STANDBY holds the
// peer WARM (empty AllowedIPs — the /32 lands when it's promoted and recompiles). Sorted by pubkey so the
// agent's reconcile is a steady-state no-op. A per-member query error fails the whole fetch (atomic).
func (s *Service) widenedDevicePeers(ctx context.Context, memberIDs []uuid.UUID, thisIsPrimary bool) ([]Peer, error) {
	seen := map[string]bool{}
	out := make([]Peer, 0)
	for _, mid := range memberIDs {
		rows, err := s.q.ListActivePeersForNode(ctx, mid)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if seen[r.PublicKey] {
				continue
			}
			seen[r.PublicKey] = true
			p := Peer{PublicKey: r.PublicKey}
			if thisIsPrimary && r.AssignedIp != nil && *r.AssignedIp != "" {
				p.AllowedIPs = []string{*r.AssignedIp + "/32"} // active primary crypto-routes the device
			}
			// standby: empty AllowedIPs (warm) — pubkey known, handshake completes, no routing until promotion
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublicKey < out[j].PublicKey })
	return out, nil
}

// siteTopology is the org's site-link input, loaded ONCE (loadSiteTopology) and consumed per-node by the
// PURE siteLinkGraphFrom / finalizeArtifact. Loading once lets the batch pushed-hash path finalize N
// nodes off a single pair of org queries instead of N (and the served path uses the same shape).
type siteTopology struct {
	gws     []sqlc.ListSiteGatewaysForOrgRow
	subnets map[uuid.UUID][]string // site_id -> approved subnet CIDRs
	// dnsForwards (S8.4) is the org's cross-site DNS forwarding table — the union of every site's
	// dns_forwarding entries, compiled onto EVERY gateway so any gateway can answer for any site's zone.
	dnsForwards []policyspec.DNSForward
	// hubMembers (S8.6 Slice 4) is the PERSISTED ACTIVE hub order (org_hub_set.members) resolved to gateway
	// rows IN ORDER — the ONE truth the compiler consumes (a failover promotion changes it, flowing through
	// the ordinary compile+push). Empty when org_hub_set has no row (a not-yet-reconciled org) → the
	// compiler falls back to electSiteHubSet (single-hub), so a fresh org still compiles.
	hubMembers []sqlc.ListSiteGatewaysForOrgRow
	// poolCIDR (A3b, S8.6) is the org's device pool (organizations.pool_cidr), canonical masked form.
	// Consumed by siteLinkGraphFrom (the spoke's hub-PRIMARY peer AllowedIPs — device transit reachability)
	// and finalizeArtifact (Compiled.PoolCIDR → the agent's pool-class DOCKER-USER accepts). Scope (paper):
	// pool rides at most ONE peer per node (the wg single-valued invariant), so A3b covers devices-on-HUB
	// reaching remote sites; devices-on-SPOKES cross-site is the REGISTERED residual (per-device placement
	// on the hub's spoke peers = the churn class D-A3b-1 rejected). Empty when the org row is gone
	// (soft-deleted org — its gateways are converging to teardown anyway).
	poolCIDR string
}

// loadSiteTopology runs the two org-wide site queries once. Full-sweep by construction: an unbound/
// deleted site drops out of ListSiteGatewaysForOrg / ListSiteSubnetsForOrg, so its peers + routes vanish.
func (s *Service) loadSiteTopology(ctx context.Context, orgID uuid.UUID) (siteTopology, error) {
	gws, err := s.q.ListSiteGatewaysForOrg(ctx, orgID)
	if err != nil {
		return siteTopology{}, err
	}
	subs, err := s.q.ListSiteSubnetsForOrg(ctx, orgID) // approved (site_id, cidr)
	if err != nil {
		return siteTopology{}, err
	}
	sub := map[uuid.UUID][]string{}
	for _, ss := range subs {
		sub[ss.SiteID] = append(sub[ss.SiteID], ss.Cidr.String())
	}
	// S8.4: union the sites' dns_forwarding JSONB into the org table. A malformed row is SKIPPED (never
	// fail the whole topology load over one bad DNS blob — the agent's forwarder also skip-degrades).
	raws, err := s.q.ListSiteDNSForwardsForOrg(ctx, orgID)
	if err != nil {
		return siteTopology{}, err
	}
	var fwds []policyspec.DNSForward
	for _, raw := range raws {
		if len(raw) == 0 {
			continue
		}
		var entries []policyspec.DNSForward
		if e := json.Unmarshal(raw, &entries); e != nil {
			slog.Warn("dns_forwarding_unmarshal_skipped", "org_id", orgID.String(), "error", e.Error())
			continue
		}
		fwds = append(fwds, entries...)
	}
	// S8.6 REDUCE: the PERSISTED active hub order, resolved to gateway rows in order — what the compiler
	// consumes so a failover promotion flows through the ordinary compile. DERIVE-THEN-FILTER (#1 sharpening):
	// the active order is deriveActive(configured, demoted) — the ONE shared derivation — computed FIRST, then
	// the gateway-existence filter is applied to the DERIVED order (never to `configured` upstream of the
	// derivation — that would be a second, shadow derivation input, the exact class the reduce killed). A
	// member no longer a live gateway (unbound/deleted) is dropped from the active order at CONSUMPTION, a
	// transient the membership-event reconcile (ReconcileHubSet on the unbind/delete path) then makes durable.
	// No row → nil → fallback (a not-yet-reconciled org still compiles).
	var hubMembers []sqlc.ListSiteGatewaysForOrgRow
	if hs, herr := s.q.GetOrgHubSet(ctx, orgID); herr == nil {
		byID := make(map[uuid.UUID]sqlc.ListSiteGatewaysForOrgRow, len(gws))
		for _, g := range gws {
			byID[g.ID] = g
		}
		for _, mid := range deriveActive(hs.Configured, hs.Demoted) {
			if g, ok := byID[mid]; ok {
				hubMembers = append(hubMembers, g)
			}
		}
	} else if herr != pgx.ErrNoRows {
		return siteTopology{}, herr
	}
	// A3b: the org's device pool, canonical masked. A read ERROR fails the load (DesiredState-ATOMIC — a
	// silently-empty pool would strand device transit dead-while-green, the exact class the law exists
	// for); ErrNoRows (soft-deleted org) degrades to empty — those gateways are tearing down regardless.
	var poolCIDR string
	if org, oerr := s.q.GetOrganizationByID(ctx, orgID); oerr == nil {
		if p, perr := netip.ParsePrefix(org.PoolCidr); perr == nil {
			poolCIDR = p.Masked().String()
		}
	} else if oerr != pgx.ErrNoRows {
		return siteTopology{}, oerr
	}
	return siteTopology{gws: gws, subnets: sub, dnsForwards: fwds, hubMembers: hubMembers, poolCIDR: poolCIDR}, nil
}

// deriveActive is THE shared hub-order derivation (S8.6 REDUCE) — the ONE function every consumer reads
// (loadSiteTopology→the data-plane + policy compilers, the failover controller, GetHubSetView). The ACTIVE
// order = the CONFIGURED order with DEMOTED members moved to the BACK (kept as warm standbys in configured
// order, NEVER dropped — a demoted member is a failover candidate, not a deletion). Pure. When nothing is
// demoted the active order IS the configured order (fail-back is that convergence). Members named in
// `demoted` but absent from `configured` are ignored (a stale demotion the next configured-write clears).
func deriveActive(configured, demoted []uuid.UUID) []uuid.UUID {
	dead := make(map[uuid.UUID]bool, len(demoted))
	for _, id := range demoted {
		dead[id] = true
	}
	live := make([]uuid.UUID, 0, len(configured))
	back := make([]uuid.UUID, 0, len(demoted))
	for _, id := range configured {
		if dead[id] {
			back = append(back, id)
		} else {
			live = append(live, id)
		}
	}
	return append(live, back...)
}

// activeHubMembers is the compiler's ordered hub set — the PERSISTED active order (org_hub_set, maintained
// by ReconcileHubSet + the failover tick), or a fallback single-hub election when org_hub_set has no row
// yet (a not-yet-reconciled org). members[0] is the ACTIVE transit hub. This is the one seam through which
// a failover promotion reaches the data plane: the tick changes the persisted order, the next compile reads
// it here — no failover-special path.
func activeHubMembers(topo siteTopology, now time.Time) []sqlc.ListSiteGatewaysForOrgRow {
	if len(topo.hubMembers) > 0 {
		return topo.hubMembers
	}
	return electSiteHubSet(topo, now)
}

// DeviceDial is WF-A D-WFA-6: a device's CURRENT dial (endpoint + gateway pubkey) derived from the org's
// ACTIVE HUB, so a running device re-homes on promotion via the routed-ranges poll. AUTH (D-WFA-6 cond 2):
// the org-scoped GetDevice is the cross-ORG guard; the owner check is the cross-DEVICE guard — a device
// fetches ONLY its own dial. A non-owned / missing device returns device_not_found (no-oracle: never reveal
// another user's device exists). derived=false (empty endpoint+pubkey) when the device's node is NOT a
// hub-set member — the client then keeps its baked endpoint (the deferred spoke-device case).
func (s *Service) DeviceDial(ctx context.Context, orgID, deviceID, callerUserID uuid.UUID) (endpoint, pubkey string, derived bool, err error) {
	dev, e := s.q.GetDevice(ctx, sqlc.GetDeviceParams{ID: deviceID, OrgID: orgID})
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return "", "", false, apierr.NotFound("device_not_found", "no such device in this organization")
		}
		return "", "", false, e
	}
	if dev.UserID != callerUserID { // cross-device guard: only the owner may fetch a device's dial (no-oracle NotFound)
		return "", "", false, apierr.NotFound("device_not_found", "no such device in this organization")
	}
	// Active-only (review #3): the data-plane rule is "peers exist only for an ACTIVE device" — a pending
	// device has no gateway peer, so its dial is useless AND serving it would make this API contradict that
	// model. (Revoked devices are already excluded — GetDevice filters deleted_at.) Same no-oracle NotFound.
	if dev.Status != "active" {
		return "", "", false, apierr.NotFound("device_not_found", "no such device in this organization")
	}
	return s.NodeDial(ctx, orgID, dev.NodeID)
}

// NodeDial derives the active-hub dial (endpoint + gateway pubkey) for a NODE (WF-A D-WFA-6) — the shared
// core of DeviceDial + the mint-time device-config derivation (a new device on a hub-set member dials the
// active hub, not its arbitrary assigned gateway). No auth (the caller has already authorized the node/
// device). derived=false when the node is not a hub-set member (caller keeps the node's own endpoint).
func (s *Service) NodeDial(ctx context.Context, orgID, nodeID uuid.UUID) (endpoint, pubkey string, derived bool, err error) {
	topo, e := s.loadSiteTopology(ctx, orgID)
	if e != nil {
		return "", "", false, e
	}
	ep, pk, ok := activeHubDialFrom(nodeID, activeHubMembers(topo, time.Now()))
	return ep, pk, ok, nil
}

// activeHubDialFrom is WF-A's endpoint-derivation primitive (D-WFA-5 (C)): a device whose assigned node is a
// HUB-SET MEMBER dials the ACTIVE PRIMARY (activeMembers[0] — the head of the ONE derivation, activeHub
// Members), so its dial FOLLOWS promotions while identity (node_id) stays put. Returns the active primary's
// (endpoint, pubkey) + derived=true when nodeID is a member; derived=false otherwise — the caller keeps the
// node's OWN endpoint (the deferred spoke-device case; no promotion event fires there). PURE: the active
// order is passed in (already deriveActive'd), never re-elected here.
func activeHubDialFrom(nodeID uuid.UUID, activeMembers []sqlc.ListSiteGatewaysForOrgRow) (endpoint, pubkey string, derived bool) {
	if len(activeMembers) == 0 {
		return "", "", false
	}
	for i := range activeMembers {
		if activeMembers[i].ID == nodeID {
			return activeMembers[0].Endpoint, activeMembers[0].WgPublicKey, true
		}
	}
	return "", "", false
}

// siteLinkGraphFrom builds a site-gateway node's site-link WG peers + kernel routes from a loaded
// siteLinkKeepaliveSecs (S8.3 CK) is the persistent-keepalive interval on every site-link peer — the
// wireguard-conventional 25s, comfortably under NAT UDP-mapping timeouts, so a NAT'd spoke stays dialable
// and an idle link never false-stales for want of a handshake.
const siteLinkKeepaliveSecs = 25

// topology (S8.2 hub-and-spoke, Item 6/7) — PURE. Returns (nil, nil) when the node is not a site gateway
// or there is no remote site to reach. HUB = the site gateway with a public endpoint (v1; deterministic
// by lowest node id if several — multi-hub reserved). A spoke peers ONLY with the hub (AllowedIPs = ALL
// remote subnets, reaching other spokes VIA the hub); the hub peers with every spoke (each peer's
// AllowedIPs = that spoke's OWN subnets); the hub forwards between them. Routes = every remote site's
// approved subnets. Deterministic (sorted) so a steady-state reconcile is a no-op.
// hubStaleWindow — a gateway not seen within this window is UNHEALTHY for hub ORDERING (sorts after fresh
// peers). ~3 missed reports; the server-side twin of the site surface's freshness idea.
const hubStaleWindow = 90 * time.Second

// electSiteHubSet is THE org transit-hub election (S8.6 D1) — ORG-LEVEL (one transit hub for the org's whole
// site mesh; NOT per-site — see docs/S8.6-decisions.md keying correction). Returns the CAPABLE gateways
// (public endpoint + a reported WG key — the ONLY membership criterion, so a capable gateway from ANY site
// enters; the standby need not share the primary's site) ORDERED: hub_priority (admin pin, ascending) >
// health (fresh-before-stale) > id (deterministic). members[0] is the active transit hub; the rest are
// failover candidates in order. PURE given `now` (used only for the health cut).
func electSiteHubSet(topo siteTopology, now time.Time) []sqlc.ListSiteGatewaysForOrgRow {
	capable := make([]sqlc.ListSiteGatewaysForOrgRow, 0, len(topo.gws))
	for _, g := range topo.gws {
		if g.Endpoint != "" && g.WgPublicKey != "" { // the capability gate (endpoint + key)
			capable = append(capable, g)
		}
	}
	sort.Slice(capable, func(i, j int) bool { return hubLess(&capable[i], &capable[j], now) })
	// TWO-TIER MEMBERSHIP (S8.6 (3)) — HA is OPT-IN BY PIN, resolving the "capable ⇒ hub-posture" collision
	// (an endpoint-bearing LEAF, e.g. an accidentally-public spoke, must NOT be drafted into hub duty). The
	// pin was already the top of the ordering; it is now ALSO the membership DECLARATION (operators outrank
	// magic, completing itself):
	//   - ANY pins present → the set is the PINNED, capable gateways (pin declares "carry the org's transit";
	//     capability still GATES — a pinned-but-NAT'd/keyless gateway is ineligible). Pinned are the sorted
	//     prefix, so collecting them preserves pin>health>id order.
	//   - NO pins → a SINGLE auto-elected hub (today's zero-config behavior, BYTE-IDENTICAL — no standbys
	//     without declared intent, so a fresh org needs zero configuration).
	var pinned []sqlc.ListSiteGatewaysForOrgRow
	for _, g := range capable {
		if g.HubPriority != nil {
			pinned = append(pinned, g)
		}
	}
	if len(pinned) > 0 {
		return pinned
	}
	if len(capable) == 0 {
		return nil
	}
	return capable[:1] // single-hub set of one (zero-config)
}

func hubHealthy(g *sqlc.ListSiteGatewaysForOrgRow, now time.Time) bool {
	return g.LastSeenAt.Valid && now.Sub(g.LastSeenAt.Time) < hubStaleWindow
}

// hubLess orders two capable gateways: PINNED (hub_priority non-null, ascending) before unpinned; then
// HEALTHY before stale; then id ascending. The admin pin OUTRANKS health — operators outrank the election.
func hubLess(a, b *sqlc.ListSiteGatewaysForOrgRow, now time.Time) bool {
	ap, bp := a.HubPriority, b.HubPriority
	if (ap != nil) != (bp != nil) {
		return ap != nil // pinned before unpinned
	}
	if ap != nil && *ap != *bp {
		return *ap < *bp // lower priority number = more preferred
	}
	if ah, bh := hubHealthy(a, now), hubHealthy(b, now); ah != bh {
		return ah // healthy before stale
	}
	return a.ID.String() < b.ID.String()
}

// electSiteHub returns the org's transit HUB — the HEAD of the election set (members[0]) — or nil when no
// gateway is capable (the B2 no-carrier condition, all NAT'd). THE election every hub fact projects
// (site-link peers, routes, the is_site_hub API projection); the compiler uses ONLY this primary (single-hub
// v1 — standbys don't grow tunnels until S8.6 Slice 3). PURE given now.
func electSiteHub(topo siteTopology, now time.Time) *sqlc.ListSiteGatewaysForOrgRow {
	set := activeHubMembers(topo, now) // the ACTIVE head (persisted order), so is_site_hub/health reflect failover
	if len(set) == 0 {
		return nil
	}
	return &set[0]
}

// HubSet is the org's persisted transit-hub authority (S8.6 REDUCE) — the two writer-partitioned fields.
// Configured is ReconcileHubSet's output (the CONFIGURED membership); Demoted is the failover controller's
// output. The ACTIVE order is Active() = deriveActive(Configured, Demoted) — never stored, one derivation.
type HubSet struct {
	OrgID      uuid.UUID
	Configured []uuid.UUID
	Demoted    []uuid.UUID
	Generation int64
}

// Active is the derived active hub order (the ONE shared derivation) — Configured with Demoted at the back.
func (h HubSet) Active() []uuid.UUID { return deriveActive(h.Configured, h.Demoted) }

// ReconcileHubSet recomputes the org's CONFIGURED transit-hub membership (electSiteHubSet) and PERSISTS it
// via the configured-field writer, bumping the D5 generation ONLY when the configured order changes (the
// atomic CASE). Called at every membership/order-change point (bind/unbind/pin). It writes `configured` ONLY
// — NEVER `demoted` (the writer partition: the failover controller owns demotion state, so a reconcile
// landing during a live failover updates membership without clobbering the demotion). A configured change is
// AUDITED as its own kind (auditHubMembership, condition 1b) — DISTINCT from a promotion/failback — in the
// SAME tx as the write (swallowed-audit law). System actor: a derived consequence of the bind/unbind/pin
// that carries its own user-actor audit.
func (s *Service) ReconcileHubSet(ctx context.Context, orgID uuid.UUID) (HubSet, error) {
	topo, err := s.siteTopoLoad(ctx, orgID)
	if err != nil {
		return HubSet{}, err
	}
	set := electSiteHubSet(topo, time.Now())
	configured := make([]uuid.UUID, 0, len(set))
	for i := range set {
		configured = append(configured, set[i].ID)
	}
	prev, err := s.GetHubSet(ctx, orgID)
	if err != nil {
		return HubSet{}, err
	}
	var out HubSet
	if txErr := s.withTx(ctx, func(q *sqlc.Queries) error {
		row, err := q.UpsertOrgHubSetConfigured(ctx, sqlc.UpsertOrgHubSetConfiguredParams{OrgID: orgID, Configured: configured})
		if err != nil {
			return err
		}
		out = HubSet{OrgID: row.OrgID, Configured: row.Configured, Demoted: row.Demoted, Generation: row.Generation}
		if !sameOrder(prev.Configured, out.Configured) {
			return audit(ctx, q, orgID, nil, auditHubMembership, "org", orgID.String(), map[string]any{
				"configured": idsToStrings(out.Configured), "generation": out.Generation,
			})
		}
		return nil
	}); txErr != nil {
		return HubSet{}, txErr
	}
	return out, nil
}

// GetHubSet reads the persisted transit-hub set (S8.6 REDUCE) — the two fields + the D5 generation. No rows
// (never reconciled) returns a zero set with empty fields, not an error.
func (s *Service) GetHubSet(ctx context.Context, orgID uuid.UUID) (HubSet, error) {
	hs, err := s.q.GetOrgHubSet(ctx, orgID)
	if err == pgx.ErrNoRows {
		return HubSet{OrgID: orgID, Configured: []uuid.UUID{}, Demoted: []uuid.UUID{}, Generation: 0}, nil
	}
	if err != nil {
		return HubSet{}, err
	}
	return HubSet{OrgID: hs.OrgID, Configured: hs.Configured, Demoted: hs.Demoted, Generation: hs.Generation}, nil
}

// latestByPubKey folds node_peer_status rows to the freshest observation per peer pubkey (S8.6 REDUCE #8 —
// ONE shared helper for the two readers: GetHubSetView's metrics + the failover tick's freshness). A GAUGE,
// not a sum: the LATEST handshake wins, never accumulated.
func latestByPubKey(rows []sqlc.NodePeerStatus) map[string]MemberMetrics {
	latest := map[string]MemberMetrics{}
	for _, r := range rows {
		if r.LastHandshakeAt.Valid && r.LastHandshakeAt.Time.After(latest[r.PublicKey].LastHandshakeAt) {
			latest[r.PublicKey] = MemberMetrics{LastHandshakeAt: r.LastHandshakeAt.Time, RxBytes: r.RxBytes, TxBytes: r.TxBytes}
		}
	}
	return latest
}

// MemberLiveness is ONE hub-set member's liveness verdict from THE ONE derivation (WF-B D-WFB-1,
// founder-ruled shared pure function): spoke-observed handshake freshness ⋂ hub-set membership.
// BOTH the failover controller (reads .Fresh for its Step) AND the site-link health surface (reads
// .Fresh + .Demoted for the badge) call deriveMemberLiveness — a SINGLE symbol, never two functions
// with a "MUST match" comment claiming equivalence (that class died in the S8.6 reduce). A health
// badge disagreeing with the controller about who is stale is the two-truths class at the failover
// seam; one pure function called twice cannot disagree with itself.
type MemberLiveness struct {
	Observed bool          // a living witness reported a VALID (non-NULL) handshake for this member (else NO verdict)
	Fresh    bool          // Observed AND age < failoverStaleWindow (meaningless when !Observed)
	Age      time.Duration // now − last handshake (valid only when Observed; for the controller's log line)
	Demoted  bool          // the failover controller has failed-over-PAST this member (in the demoted set)
}

// deriveMemberLiveness is THE ONE liveness derivation (WF-B D-WFB-1). Pure + clockless (now passed
// in). GREP-RED (docs/WF-B-site-link-badge-decisions.md): no site-link freshness computation
// (`now.Sub(…LastHandshakeAt) < failoverStaleWindow`) exists ANYWHERE outside this function — the
// controller and the health surface both read its output.
func deriveMemberLiveness(configured []uuid.UUID, pubkey map[uuid.UUID]string, rows []sqlc.NodePeerStatus, demoted []uuid.UUID, now time.Time) map[uuid.UUID]MemberLiveness {
	latest := latestByPubKey(rows)
	dem := make(map[uuid.UUID]bool, len(demoted))
	for _, id := range demoted {
		dem[id] = true
	}
	out := make(map[uuid.UUID]MemberLiveness, len(configured))
	for _, id := range configured {
		ml := MemberLiveness{Demoted: dem[id]}
		if m, observed := latest[pubkey[id]]; observed { // latestByPubKey skips NULL handshakes → absence = no witness
			ml.Observed = true
			ml.Age = now.Sub(m.LastHandshakeAt)
			ml.Fresh = ml.Age < failoverStaleWindow
		}
		out[id] = ml
	}
	return out
}

// idsToStrings renders a node-id slice for audit metadata (stable, ordered).
func idsToStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// MemberMetrics is a hub member's LATEST node_peer_status observation (S8.6 L1). PRESENT only when a row
// exists (someone handshook with this member) — a not-reporting link has NO metrics (nil), DISTINCT from an
// idle link (a row with rx/tx = 0). rx/tx are RAW gauges since the last handshake (display only, never
// summed monotonic — S11.1). The render cites THIS storage shape, not the report schema.
type MemberMetrics struct {
	LastHandshakeAt  time.Time
	RxBytes, TxBytes int64
}

// HubMemberView is one hub-set member as SERVED: its node id, its ROLE (primary = members[0], else
// standby), its admin hub_priority (the CONFIGURED order — so the UI can show a promotion "in effect" when
// the active order diverges), and its latest metrics (nil when not reporting).
type HubMemberView struct {
	NodeID      uuid.UUID
	Role        string
	HubPriority *int32
	Metrics     *MemberMetrics
}

// HubSetView is the org's persisted hub set as SERVED (S8.6 Slice 6): the D5 generation (the set's version
// tag) + the ordered members with role + L1 metrics. ONE truth — the same persisted org_hub_set every
// consumer (compiler, health, this view) reads; no inference.
type HubSetView struct {
	Generation int64
	Members    []HubMemberView
}

// GetHubSetView serves the persisted active hub set + per-member L1 metrics (node_peer_status). Empty when
// no set is persisted (a not-yet-pinned org). Org-scoped by the caller (member-readable, D5/S8.3 precedent).
func (s *Service) GetHubSetView(ctx context.Context, orgID uuid.UUID) (HubSetView, error) {
	hs, err := s.GetHubSet(ctx, orgID)
	if err != nil {
		return HubSetView{}, err
	}
	gws, err := s.q.ListSiteGatewaysForOrg(ctx, orgID)
	if err != nil {
		return HubSetView{}, err
	}
	keyByNode := make(map[uuid.UUID]string, len(gws))
	prioByNode := make(map[uuid.UUID]*int32, len(gws))
	for i := range gws {
		keyByNode[gws[i].ID] = gws[i].WgPublicKey
		prioByNode[gws[i].ID] = gws[i].HubPriority
	}
	rows, err := s.q.ListNodePeerStatusForOrg(ctx, orgID)
	if err != nil {
		return HubSetView{}, err
	}
	latest := latestByPubKey(rows)
	// S8.6 #3: DERIVE-THEN-FILTER — the SAME discipline loadSiteTopology applies to the data plane. The
	// active order is derived (HubSet.Active()), then filtered against the LIVE gateways (keyByNode is built
	// from ListSiteGatewaysForOrg, the identical status='active' source the data plane reads). A `configured`
	// member no longer a live gateway (revoked/deleted/departed, before the corrector tick has rewritten
	// configured) is DROPPED here — so the view can never show a member the data plane has failed away from.
	active := make([]uuid.UUID, 0, len(hs.Active()))
	for _, mid := range hs.Active() {
		if _, live := keyByNode[mid]; live {
			active = append(active, mid)
		}
	}
	view := HubSetView{Generation: hs.Generation, Members: make([]HubMemberView, 0, len(active))}
	for i, mid := range active {
		mv := HubMemberView{NodeID: mid, Role: "standby", HubPriority: prioByNode[mid]}
		if i == 0 {
			mv.Role = "primary"
		}
		if m, ok := latest[keyByNode[mid]]; ok {
			mm := m
			mv.Metrics = &mm // present only when a node_peer_status row exists (idle=0 vs not-reporting=nil)
		}
		view.Members = append(view.Members, mv)
	}
	return view, nil
}

// SetHubPriority sets (or clears, nil) a gateway's admin hub PIN (D1) and re-elects. Org-checked (a
// cross-org node id -> 404). Audited in-tx; the re-election persists after so the pin takes effect.
func (s *Service) SetHubPriority(ctx context.Context, actor, orgID, nodeID uuid.UUID, priority *int32) error {
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		old, e := q.GetNodeHubPriority(ctx, sqlc.GetNodeHubPriorityParams{ID: nodeID, OrgID: orgID})
		if e == pgx.ErrNoRows {
			return apierr.NotFound("node_not_found", "no such node in this organization")
		}
		if e != nil {
			return e
		}
		n, e := q.SetNodeHubPriority(ctx, sqlc.SetNodeHubPriorityParams{NodeID: nodeID, OrgID: orgID, HubPriority: priority})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("node_not_found", "no such node in this organization")
		}
		// Audit the OLD→NEW pin (a topology-consequential act — pinning creates/edits the HA hub set).
		return audit(ctx, q, orgID, &actor, "node.hub_priority_set", "node", nodeID.String(), map[string]any{"old_priority": old, "new_priority": priority})
	})
	if err != nil {
		return err
	}
	_, err = s.ReconcileHubSet(ctx, orgID)
	return err
}

func siteLinkGraphFrom(topo siteTopology, node sqlc.Node) ([]Peer, []policyspec.Route) {
	if !node.SiteID.Valid {
		return nil, nil
	}
	members := activeHubMembers(topo, time.Now()) // the PERSISTED active order (S8.6 Slice 4), or single-hub fallback
	// B2: no capable gateway (all NAT'd) → NO carrier for site traffic, so emit NO routes and NO peers.
	// A route with no peer to carry it is the silent blackhole; surfaced CP-side as site_hub_down.
	if len(members) == 0 {
		return nil, nil
	}
	mySite := uuid.UUID(node.SiteID.Bytes)
	routeSeen := map[string]bool{}
	var routeCIDRs []string
	for i := range topo.gws {
		g := &topo.gws[i]
		if uuid.UUID(g.SiteID.Bytes) == mySite {
			continue
		}
		for _, c := range topo.subnets[uuid.UUID(g.SiteID.Bytes)] {
			if !routeSeen[c] {
				routeSeen[c] = true
				routeCIDRs = append(routeCIDRs, c)
			}
		}
	}
	sort.Strings(routeCIDRs)
	routes := make([]policyspec.Route, 0, len(routeCIDRs))
	for _, c := range routeCIDRs {
		routes = append(routes, policyspec.Route{DstCIDR: c})
	}

	isMember := false
	for i := range members {
		if members[i].ID == node.ID {
			isMember = true
			break
		}
	}

	var peers []Peer
	if isMember {
		// HUB posture — carried by the PRIMARY *and* every STANDBY (the D2 symmetry: a standby is a hub that
		// isn't preferred yet, so promotion changes nothing hub-side). Peer with every non-self,
		// subnet-advertising gateway NOT in this hub's OWN site. SAME-SITE exclusion (S8.6) is the real
		// invariant — two gateways on one shared LAN need no tunnel between them (kills the spurious same-site
		// hub↔hub link the single-node lift now makes possible). A CROSS-site member keeps its subnet-carrying
		// link — in the data plane it IS a spoke, whatever the election calls it (so its subnets stay reachable).
		for i := range topo.gws {
			g := &topo.gws[i]
			if g.ID == node.ID || uuid.UUID(g.SiteID.Bytes) == mySite {
				continue
			}
			ips := append([]string(nil), topo.subnets[uuid.UUID(g.SiteID.Bytes)]...)
			if len(ips) == 0 {
				continue // a gateway advertising no subnets yet contributes no crypto-routing
			}
			sort.Strings(ips)
			peers = append(peers, Peer{PublicKey: g.WgPublicKey, AllowedIPs: ips, Endpoint: g.Endpoint, SiteLink: true, PersistentKeepalive: siteLinkKeepaliveSecs})
		}
	} else if len(routeCIDRs) > 0 {
		// SPOKE: the remote subnets live in the PRIMARY (members[0]) peer's AllowedIPs ONLY — the single-valued
		// invariant (WG's undefined-which across overlapping AllowedIPs is a nondeterminism generator). Every
		// STANDBY member is a WARM keepalive-only peer: AllowedIPs EMPTY (so WG can never crypto-route traffic
		// to it — the no-traffic property is STRUCTURAL, not a convention), endpoint + keepalive so the tunnel
		// handshakes (warm + observable in node_peer_status). Promotion (Slice 4) re-compiles the subnets onto
		// the standby's AllowedIPs — no build, no handshake wait: the tunnel is already up.
		primary := &members[0]
		// A3b: the device POOL rides the hub-PRIMARY peer's AllowedIPs alongside the remote routes — the
		// far half of device→remote-site transit: inbound, wg admits device-sourced (pool-saddr) packets
		// arriving via the hub; outbound, replies to pool addresses crypto-route back to the hub. Primary
		// ONLY — a standby stays AllowedIPs-empty (single-valued invariant), and promotion recompiles the
		// pool onto the new primary exactly as it does routeCIDRs. Reachability, not permission: the far
		// gateway's ip tunnex chain adjudicates via compiled far-grants (D-A3b-1/2).
		spokeIPs := append([]string(nil), routeCIDRs...)
		if topo.poolCIDR != "" {
			spokeIPs = append(spokeIPs, topo.poolCIDR)
			sort.Strings(spokeIPs)
		}
		peers = append(peers, Peer{PublicKey: primary.WgPublicKey, AllowedIPs: spokeIPs, Endpoint: primary.Endpoint, SiteLink: true, PersistentKeepalive: siteLinkKeepaliveSecs})
		for i := 1; i < len(members); i++ {
			sb := &members[i]
			peers = append(peers, Peer{PublicKey: sb.WgPublicKey, AllowedIPs: []string{}, Endpoint: sb.Endpoint, SiteLink: true, PersistentKeepalive: siteLinkKeepaliveSecs})
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
	return peers, routes
}

// finalizeArtifact is THE SINGLE SOURCE OF TRUTH for a site gateway's served/hashed compiled artifact
// (the #1 fix). It attaches the node's site-to-site kernel routes to the route-less compiled artifact
// and derives the content version — and BOTH the served desired-state AND the pushed-hash desync
// baseline call it, so the two paths can never disagree about the artifact's contents. A non-site node
// or a node with no remote routes is returned unchanged. A nil route-less artifact WITH routes (open
// build, or an off-mode node that would carry routes) is synthesized as a mesh artifact carrying them.
func (s *Service) finalizeArtifact(topo siteTopology, node sqlc.Node, pol *policyspec.Compiled) *policyspec.Compiled {
	if !node.SiteID.Valid {
		return pol
	}
	_, routes := siteLinkGraphFrom(topo, node)
	if len(routes) == 0 {
		return pol
	}
	// D2: attach THIS gateway's own approved site subnets (the authoritative local-subnet answer) so the
	// agent can source its site routes from the site LAN. Out-of-hash plumbing; rides with Routes (v5).
	local := append([]string(nil), topo.subnets[uuid.UUID(node.SiteID.Bytes)]...)
	sort.Strings(local)
	// S8.4: attach the org-wide DNS forwarding table (out-of-hash CONVENIENCE — NOT hashed, NO version bump,
	// so a route-carrying artifact stays byte-identical for the desync baseline). Every gateway carries the
	// whole table so any gateway answers for any site's zone. nil for a no-DNS org (empty until Slice 2).
	dns := append([]policyspec.DNSForward(nil), topo.dnsForwards...)
	// A3b: attach the org pool so the agent renders the pool-class DOCKER-USER accepts (device transit /
	// device↔device at the Docker tier; the chain adjudicates). Rides WITH routes (this point is past the
	// routes==0 return), so v6 stays content-derived: only multi-site orgs' gateways carry it — a
	// single-site org keeps its pre-v6 artifact byte-identical (and its Docker-dark device↔device is the
	// REGISTERED PD-3 residual, with non-site gateways).
	if pol != nil {
		pol.Routes = routes
		pol.LocalSubnets = local
		pol.DNSForwards = dns
		pol.PoolCIDR = topo.poolCIDR
		pol.Version = policyspec.RequiredVersion(*pol)
		return pol
	}
	c := policyspec.Compiled{NodeID: node.ID.String(), Mode: "off", Mesh: true, Routes: routes, LocalSubnets: local, DNSForwards: dns, PoolCIDR: topo.poolCIDR}
	c.Version = policyspec.RequiredVersion(c)
	return &c
}

// pushedHash is the desync baseline for one node: the CanonicalHash of its FINALIZED artifact, or "" for
// a non-enforcing (off/mesh) artifact — an off org has no enforcement boundary, so it never shows
// policy-desynced (finding #C). Because it finalizes the SAME way the served artifact does, a route-
// carrying ENFORCING gateway's pushed hash matches what the agent applies — no false silent_desync (#1).
func (s *Service) pushedHash(topo siteTopology, node sqlc.Node, routeless *policyspec.Compiled) string {
	final := s.finalizeArtifact(topo, node, routeless)
	if final == nil || final.Mode != "enforcing" {
		return ""
	}
	return policyspec.CanonicalHash(*final)
}

// siteTopoHasHub reports whether ANY gateway carries a public endpoint (a hub exists). Org-wide +
// node-independent, so the batch health path computes it ONCE (R5: was an O(N·G) per-node rescan).
func siteTopoHasHub(topo siteTopology) bool {
	return electSiteHub(topo, time.Now()) != nil // one election: hub existence reads the same picker as the designation
}

// siteHubMissing reports the B2 no-carrier condition for ONE node, given a precomputed hubExists: a site
// gateway with REMOTE site subnets to reach but no hub — surfaced as site_hub_down so it is never a
// silent no-op. False for a non-site node, when a hub exists, or when nothing is remote.
func siteHubMissing(hubExists bool, topo siteTopology, node sqlc.Node) bool {
	if hubExists || !node.SiteID.Valid {
		return false
	}
	mySite := uuid.UUID(node.SiteID.Bytes)
	for sid, cidrs := range topo.subnets {
		if sid != mySite && len(cidrs) > 0 {
			return true // remote subnets exist but no hub to carry them
		}
	}
	return false
}

// validEndpoint reports whether s is a clean host:port with a numeric port and
// no whitespace/control characters (which would allow config injection).
func validEndpoint(s string) bool {
	if s == "" || len(s) > 259 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p > 0 && p <= 65535
}

// PeerStatus is per-peer live telemetry reported by the agent.
type PeerStatus struct {
	PublicKey     string
	LastHandshake int64 // unix seconds, 0 = never
	RxBytes       int64
	TxBytes       int64
}

// ReportStatus upserts the reported per-peer telemetry, mapping each pubkey to
// its active device on the node. Batched (one round-trip); unknown pubkeys no-op.
func (s *Service) ReportStatus(ctx context.Context, node sqlc.Node, stats []PeerStatus) error {
	if len(stats) == 0 {
		return nil
	}
	// Reject an implausibly-future handshake (bogus counter / bad clock): stored
	// verbatim it would make time.Since() negative and pin the device "online"
	// forever. A small skew tolerance is allowed. This is the SINGLE enforcement
	// point of the "handshake is never in the future" data invariant that every
	// online reader relies on (see tenancy.OnlineWindow) — hence the regression
	// test in status_test.go. A dropped future report degrades in the HONEST
	// direction: it nulls a previously-valid handshake (fake-offline is a
	// tolerable degradation; fake-online would be a lie).
	maxHS := time.Now().Add(2 * time.Minute).Unix()
	params := make([]sqlc.UpsertDeviceStatusParams, 0, len(stats))
	peerParams := make([]sqlc.UpsertNodePeerStatusParams, 0, len(stats)) // S8.6: the gateway-peer sibling
	for _, st := range stats {
		var hs pgtype.Timestamptz
		if st.LastHandshake > 0 && st.LastHandshake <= maxHS {
			hs = pgtype.Timestamptz{Time: time.Unix(st.LastHandshake, 0).UTC(), Valid: true}
		}
		params = append(params, sqlc.UpsertDeviceStatusParams{
			NodeID: node.ID, PublicKey: st.PublicKey, LastHandshakeAt: hs,
			RxBytes: st.RxBytes, TxBytes: st.TxBytes,
		})
		// S8.6 SIBLING upsert: the SAME report also feeds node_peer_status for peers that are GATEWAYS. Each
		// upsert's own EXISTS guard routes the peer — a device pubkey no-ops here, a gateway pubkey no-ops in
		// device_status — so neither table crosses. No new agent field: the CP finally stores the gateway-peer
		// telemetry the agent already sends (REPORTED != STORED, fixed).
		peerParams = append(peerParams, sqlc.UpsertNodePeerStatusParams{
			NodeID: node.ID, PublicKey: st.PublicKey, LastHandshakeAt: hs,
			RxBytes: st.RxBytes, TxBytes: st.TxBytes,
		})
	}
	// br.Exec closes the batch results itself, so we do not Close separately.
	var firstErr error
	record := func(_ int, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.q.UpsertDeviceStatus(ctx, params).Exec(record)
	s.q.UpsertNodePeerStatus(ctx, peerParams).Exec(record)
	return firstErr
}

// AppliedPolicy is the agent-reported Zero Trust policy IN FORCE on the gateway
// (S7.2 staleness): the version + canonical hash of the last successfully applied
// Compiled, and the last apply error if any. Stored in the capabilities JSONB;
// the control plane compares it against what it pushed — a gateway running stale
// policy must be VISIBLE (a policy violation in slow motion), never silent.
type AppliedPolicy struct {
	Version int    `json:"policy_version"`
	Hash    string `json:"policy_hash"`
	Error   string `json:"policy_error"`
	// FailingSince (RFC3339, empty when healthy) is the agent-reported mismatch
	// onset: when apply FIRST started failing. The stale alarm measures from here,
	// so a normal push that applies cleanly never registers stale (finding #3).
	FailingSince string `json:"policy_failing_since"`
	// RefusedVersion (S8.1 D1) is the compiled-artifact version the agent REFUSED as
	// unsupported (> its MaxSupportedVersion), or 0 when none. Surfaced as the distinct
	// `unsupported_policy_version` health kind (remedy: upgrade the agent).
	RefusedVersion int `json:"policy_refused_version"`
	// SiteLinkStale (S8.2 H5) is agent-computed: at least one of this gateway's SITE-LINK peers (the
	// hub, or a spoke) has a stale/absent WG handshake — site-to-site traffic on that link is dead.
	// Surfaced as site_link_down so a down bridge is never green-on-the-dashboard.
	SiteLinkStale bool `json:"site_link_stale"`
	// SiteSubnetUnreachable (S8.2c D3) is agent-computed: the gateway advertises a local site subnet no
	// host address is inside (bridge-trapped wg0 / misconfig). Surfaced as site_subnet_unreachable.
	SiteSubnetUnreachable bool `json:"site_subnet_unreachable"`
	// ConntrackFlushUnavailable (S8.7 Slice 2) is agent-reported: the expired-grant conntrack flush is
	// failing (no CAP_NET_ADMIN / netlink fault) — revoked grants' flows may linger. Surfaced as
	// conntrack_flush_unavailable.
	ConntrackFlushUnavailable bool `json:"conntrack_flush_unavailable"`
	// MaxSupportedVersion (S8.3 CW) is the highest artifact Version the agent can apply. Observability
	// (outside the hash); stored so the UI can warn which gateways would deny-all on a version bump.
	MaxSupportedVersion int `json:"max_policy_version"`
}

// ReportWGInfo records the agent's locally-generated WireGuard public key and
// public endpoint. It validates the key is a well-formed 32-byte base64 value and
// the endpoint (if present) is a clean host:port — a malformed value would poison
// the .conf of every peer on this node. A zero-row update (e.g. the node was
// revoked mid-report) is an error, not a silent no-op.
func (s *Service) ReportWGInfo(ctx context.Context, node sqlc.Node, publicKey, endpoint string, egressNAT bool, applied AppliedPolicy) error {
	if !wgkey.Valid(publicKey) {
		return apierr.BadRequest("invalid_wg_key", "public_key must be a 32-byte base64 WireGuard key")
	}
	// A non-empty endpoint must be a clean host:port. This is the value that gets
	// concatenated verbatim into every peer's .conf, so an unvalidated endpoint
	// (newlines, extra directives) from a compromised agent could inject arbitrary
	// wg-quick config into other users' downloads. Empty is allowed (COALESCE in
	// the query keeps any prior value).
	if endpoint != "" && !validEndpoint(endpoint) {
		return apierr.BadRequest("invalid_endpoint", "endpoint must be a host:port with no whitespace")
	}
	// Bound the agent-supplied policy-status strings (they land in a JSONB column and
	// in dashboards) — a compromised agent must not stuff megabytes or control bytes.
	if len(applied.Hash) > 64 {
		applied.Hash = applied.Hash[:64]
	}
	if len(applied.Error) > 512 {
		applied.Error = applied.Error[:512]
	}
	// Bound the agent-supplied failing_since string too (it lands in JSONB).
	if len(applied.FailingSince) > 40 {
		applied.FailingSince = applied.FailingSince[:40]
	}
	// Gateway capabilities the agent probes + re-reports every reconcile (S3.7 +
	// S7.2 applied-policy status). The column is a forward-compat JSONB map; we build
	// it server-side from the typed report so a compromised agent can't inject
	// arbitrary JSON. egress_nat gates full-tunnel device creation (gateway_no_egress).
	caps, err := json.Marshal(map[string]any{
		"egress_nat":                  egressNAT,
		"policy_version":              applied.Version,
		"policy_hash":                 applied.Hash,
		"policy_error":                applied.Error,
		"policy_failing_since":        applied.FailingSince,
		"policy_refused_version":      applied.RefusedVersion,
		"site_link_stale":             applied.SiteLinkStale, // VESTIGIAL (WF-B D-WFB-1b): still reported+persisted for backward-compat, but NO LONGER CONSUMED — the CP derives site-link health from the ONE liveness derivation (fillSiteLinkVerdict). Retire or re-adopt deliberately in an agent-vN; do not silently resurrect.
		"site_subnet_unreachable":     applied.SiteSubnetUnreachable,
		"conntrack_flush_unavailable": applied.ConntrackFlushUnavailable,
		"max_policy_version":          applied.MaxSupportedVersion,
	})
	if err != nil {
		return err
	}
	n, err := s.q.SetNodeWGInfo(ctx, sqlc.SetNodeWGInfoParams{ID: node.ID, WgPublicKey: publicKey, Endpoint: endpoint, Capabilities: caps})
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.Conflict("node_not_active", "node is no longer active; key not stored")
	}
	s.trackDesync(ctx, node, applied.Hash)
	return nil
}

// trackDesync is the SINGLE WRITER of nodes.policy_desync_since (S7.4b X-4 + single-writer
// amendment): on each FRESH report it stamps the term-3 desync onset (CP clock, X-2) or clears
// on reconvergence / non-enforcing. Called from exactly one site (ReportWGInfo). The OPEN build
// (s.policy == nil) is provably SILENT — no query runs, no error, no enterprise hash-compare
// import in the open binary. The value is ALWAYS the CP clock (time.Now) — an agent report can
// never supply it (AppliedPolicy has no desync field; the column is not in the agent-fed caps).
func (s *Service) trackDesync(ctx context.Context, node sqlc.Node, appliedHash string) {
	if s.policy == nil {
		return // open build — desync tracking is enterprise-only; silent, no write
	}
	// The pushed hash is finalized the SAME way the served artifact is (route-attach + version), so a
	// route-carrying enforcing gateway compares clean instead of a false silent_desync (the #1 fix). Only
	// a SITE gateway needs the topology (finalizeArtifact no-ops for non-site nodes) — skip the queries
	// otherwise. The topology loads BEFORE the compile so the derived active hub threads in (S8.6 REDUCE
	// #1 — the pushed baseline cites the same hub the served artifact does).
	var topo siteTopology
	var activeHub uuid.UUID
	if node.SiteID.Valid {
		t, terr := s.loadSiteTopology(ctx, node.OrgID)
		if terr != nil {
			return // topology unavailable → can't-determine; never stamp/clear on a partial baseline
		}
		topo = t
		if h := electSiteHub(topo, time.Now()); h != nil {
			activeHub = h.ID
		}
	}
	arts, err := s.policy.CompiledArtifactsForNodes(ctx, node.OrgID, []uuid.UUID{node.ID}, activeHub)
	if err != nil {
		return // pushed artifact unavailable (compile fault) → can't-determine; never stamp/clear
	}
	pushed := s.pushedHash(topo, node, arts[node.ID])
	if pushed == "" || pushed == appliedHash {
		// non-enforcing (off/mesh) OR reconverged — convergence is a STATE predicate, so a
		// revert-to-clear (target moved back to the applied hash) legitimately clears.
		// [fold 2] LOG a failed clear (don't swallow): a stale onset would render the NEXT
		// legit push as a false red silent_desync. Self-healing bound ≤ R — the next report
		// re-evaluates + retries this clear (the node stays reconverged).
		if err := s.q.ClearNodePolicyDesyncSince(ctx, sqlc.ClearNodePolicyDesyncSinceParams{ID: node.ID, OrgID: node.OrgID}); err != nil {
			slog.Warn("policy_desync_clear_failed", "node_id", node.ID, "error", err.Error())
		}
		return
	}
	// enforcing + mismatch → stamp the onset (idempotent: WHERE IS NULL preserves the first
	// onset PER EPISODE; a re-push after a clear re-stamps a NEW onset).
	// [fold 5] LOG a failed stamp: a NULL onset would render a genuinely stuck node as
	// converging forever. Self-healing bound ≤ R — the next report retries (still mismatched).
	if err := s.q.StampNodePolicyDesyncSince(ctx, sqlc.StampNodePolicyDesyncSinceParams{
		ID:                node.ID,
		OrgID:             node.OrgID,
		PolicyDesyncSince: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		slog.Warn("policy_desync_stamp_failed", "node_id", node.ID, "error", err.Error())
	}
}

// NodeCapabilities is the typed view of a node's capabilities JSONB, read where the
// control plane gates on a gateway's abilities (e.g. full-tunnel egress) or surfaces
// its applied-policy status (S7.2 staleness).
type NodeCapabilities struct {
	EgressNAT     bool   `json:"egress_nat"`
	PolicyVersion int    `json:"policy_version"`
	PolicyHash    string `json:"policy_hash"`
	PolicyError   string `json:"policy_error"`
	// PolicyFailingSince (RFC3339) is the agent-reported mismatch ONSET: when apply
	// first started failing (empty when healthy). The stale window measures from
	// here, not the applied-hash age -- so a normal push never false-alarms (#3).
	PolicyFailingSince string `json:"policy_failing_since"`
	// PolicyRefusedVersion (S8.1 D1) is the compiled-artifact version the agent REFUSED
	// as unsupported (0 = none). Drives the `unsupported_policy_version` health kind.
	PolicyRefusedVersion int `json:"policy_refused_version"`
	// SiteLinkStale (S8.2 H5) — agent-computed: a site-link peer has a stale/absent handshake.
	// Drives the `site_link_down` health kind (a dead bridge is never green).
	SiteLinkStale bool `json:"site_link_stale"`
	// SiteSubnetUnreachable (S8.2c D3) — agent-computed: advertises a local subnet no host addr is inside.
	// Drives the `site_subnet_unreachable` health kind (the reassuring-green bridge-mode trap).
	SiteSubnetUnreachable bool `json:"site_subnet_unreachable"`
	// ConntrackFlushUnavailable (S8.7 Slice 2) — agent-reported: the expired-grant conntrack flush is
	// failing. Drives the `conntrack_flush_unavailable` health kind (lowest priority).
	ConntrackFlushUnavailable bool `json:"conntrack_flush_unavailable"`
	// MaxPolicyVersion (S8.3 CW) — the agent's reported max-supported policy version. 0 = never reported
	// (a pre-CW/pre-upgrade agent): read as BELOW the ceiling, never unknown-treated-as-ready (S7.5.3
	// absence-is-not-compliance). Surfaced on the Node API for the cross-site upgrade warning.
	MaxPolicyVersion int `json:"max_policy_version"`
}

// zeroTrustOff mirrors organizations.zero_trust_mode = 'off' (the compiler's ModeOff).
// Kept as a neutral local const so the open build never imports enterprise/policy.
const zeroTrustOff = "off"

// PolicyDegradedForNodes computes ONE conservative Zero Trust health signal per node for
// the API — the COLLAPSED staleness surface (S7.2 design change; see docs/S7.2-decisions.md
// for the 3-signal→2-field→gap-states→3→1-disjunction history). All nodes must belong to
// orgID. A node is DEGRADED iff any of:
//
//	(1) caps.PolicyError != ""          — an apply is failing right now. This is ALSO the
//	                                      stuck-enforcing case: a gateway that failed to
//	                                      apply a mesh/off ruleset and is still enforcing a
//	                                      disabled policy sets applyErr (the "silent stale
//	                                      policy = violation in slow motion" case found live
//	                                      across passes 2–4).
//	(2) caps.PolicyFailingSince != ""   — an enforcing apply has been failing since its
//	                                      onset (any duration — conservative).
//	(3) enforcing AND pushed != applied — a silent desync: the policy IN FORCE differs from
//	                                      what the control plane would push now. "" pushed
//	                                      means non-enforcing (off/mesh), which has no
//	                                      boundary and never degrades. INSTANTANEOUS compare
//	                                      (no silent-desync onset is tracked server-side —
//	                                      that would be new state, against the reduce goal),
//	                                      so it may briefly over-report during a normal
//	                                      push's converge window; that is intentional per the
//	                                      OVER-report principle below.
//
// The field errs toward OVER-reporting (a false "degraded" is an annoyance; a false
// "healthy" is the silent-blackhole class we hit three times) — EXCEPT in the provider
// CAN'T-DETERMINE window: when the compile transiently errors (pushed nil), term (3) is
// skipped, so an enforcing gateway already desynced reads not-degraded for that window.
// This is bounded + safe: the gateway is guaranteed on its LAST-GOOD fail-closed policy
// (never open, never blackholing-from-this-cause), and it matches the couldn't-determine
// disposition (a transient control-plane fault is not a gateway fault). The rich agent
// signals (failingSince / hash / applyErr) still land in the capabilities JSONB unchanged;
// the DIFFERENTIATED surface (which-kind-of-degraded + badge UX) is S7.4, reading that JSONB.
//
// Open build / no policy provider: nothing degrades (no policy engine).
// PolicyHealth is the atomic per-node health: the authoritative bool + the advisory kind,
// derived from ONE snapshot (fold [0]) — a single pushed-hash compile + one caps read per node —
// so the two can NEVER read different snapshots (the cross-snapshot race that suppressed the
// badge on a genuinely-desynced gateway).
type PolicyHealth struct {
	Degraded bool
	Kind     PolicyDegradedKind
	// WF-B: the SUBORDINATE site-link note — INDEPENDENT of the headline Kind (D-WFB-2/D-WFB-3). Set when a
	// DEMOTED hub member's link is dead WHILE transit rides the active primary (healthy): the site's
	// headline stays its real state, and this names the demoted-dead peer as a distinct line item
	// ("aws-gw-1 (demoted)"). Empty peer = no note. NEVER set when the headline is site_link_down (a real
	// transit failure is not accompanied by a reassuring subordinate line — the inverse red's guard).
	SiteLinkNotePeer    string // the demoted-dead peer's display name ("" = no note)
	SiteLinkNoteDemoted bool   // always true when SiteLinkNotePeer set (carries the render's "(demoted)" qualifier)
}

// NodeDisplayExtras is per-node S8.3 display truth surfaced on the Node API: the hub designation (a
// PROJECTION of electSiteHub, never re-elected UI-side — D2) and the agent's reported max policy version.
type NodeDisplayExtras struct {
	IsSiteHub        bool
	MaxPolicyVersion int // 0 = never reported (pre-CW agent) → the UI reads this as below-ceiling
}

// SiteTopoBatch is the per-request site topology + elected hub, loaded ONCE for a node list and shared by
// the health + display passes so ListNodes does not load the topology (and elect the hub) TWICE (R5 batch
// discipline — review #3). Opaque: build it with LoadSiteTopoBatch and pass it to both methods.
type SiteTopoBatch struct {
	topo   siteTopology
	ok     bool      // false = a node is a site gateway but the topology LOAD failed (hub-health can't determine)
	hubID  uuid.UUID // the elected hub's node id (valid only when hasHub)
	hasHub bool      // electSiteHub(topo) != nil — the ONE election, computed once for the batch
	// WF-B (site-link health from THE ONE liveness derivation — deriveMemberLiveness): the ORG-LEVEL
	// site-link verdict, computed ONCE per batch and applied to every site gateway (transit health is
	// org-level — the hub set serves all sites). NEVER caps.SiteLinkStale (the retired agent bool, which
	// cannot name a peer or know demotion — D-WFB-1b).
	siteLinkHeadlineDown bool      // the ACTIVE PRIMARY hub is stale → org site transit is genuinely dead (the headline)
	demotedDeadPeer      uuid.UUID // a DEMOTED member is dead while the primary is fresh → subordinate named line (Nil = none)
	// memberWireFresh (WF-C L2 D-WFC2-1a): per hub-set member, whether its spoke-observed handshake is FRESH
	// (deriveMemberLiveness .Observed && .Fresh) — the wire half of the zombie-hub conjunction. Populated from
	// THE ONE liveness derivation in fillSiteLinkVerdict; a non-member (or unobserved member) is absent/false.
	memberWireFresh map[uuid.UUID]bool
}

// LoadSiteTopoBatch loads the site topology once for a node list + elects the hub once (electSiteHub — the
// same picker the site-link graph uses). A zero batch (ok=true, no hub) when no node is a site gateway.
// Pass the result to PolicyHealthForNodes + NodeDisplayExtrasForNodes so neither reloads it.
func (s *Service) LoadSiteTopoBatch(ctx context.Context, orgID uuid.UUID, nodes []sqlc.Node) SiteTopoBatch {
	b := SiteTopoBatch{ok: true}
	anySite := false
	for _, n := range nodes {
		if n.SiteID.Valid {
			anySite = true
			break
		}
	}
	if anySite {
		now := time.Now()
		if t, err := s.loadSiteTopology(ctx, orgID); err == nil {
			b.topo = t
			if hub := electSiteHub(t, now); hub != nil {
				b.hubID, b.hasHub = hub.ID, true
			}
			s.fillSiteLinkVerdict(ctx, orgID, &b, now)
		} else {
			b.ok = false // load failed → can't determine hub-health (never a wrong designation)
		}
	}
	return b
}

// fillSiteLinkVerdict derives the ORG-LEVEL site-link health (WF-B) from THE ONE liveness derivation
// (deriveMemberLiveness — the same symbol the failover controller reads; no second freshness, the
// two-truths guard). PINNED org: members = the persisted hub set, active primary = deriveActive[0], a
// DEMOTED-yet-dead member becomes the subordinate note. UNPINNED-but-hubbed org: the sole member = the
// ELECTED hub (no failover, so no demotion → no subordinate note, just the headline). No hub → no verdict
// (the batch's zero value: headline false, no peer). A no-witness (unobserved) member yields NO verdict
// (never a headline-down on silence — the same HOLD the controller applies).
func (s *Service) fillSiteLinkVerdict(ctx context.Context, orgID uuid.UUID, b *SiteTopoBatch, now time.Time) {
	pubkey := make(map[uuid.UUID]string, len(b.topo.gws))
	for i := range b.topo.gws {
		pubkey[b.topo.gws[i].ID] = b.topo.gws[i].WgPublicKey
	}
	hs, herr := s.GetHubSet(ctx, orgID) // empty (Configured nil) on ErrNoRows — an unpinned org
	var members, demoted []uuid.UUID
	var activePrimary uuid.UUID
	if herr == nil && len(hs.Configured) > 0 {
		members, demoted = hs.Configured, hs.Demoted
		if active := deriveActive(hs.Configured, hs.Demoted); len(active) > 0 {
			activePrimary = active[0]
		}
	} else if b.hasHub {
		members, activePrimary = []uuid.UUID{b.hubID}, b.hubID // unpinned: the elected hub, no demotion
	} else {
		return // no hub → no site-link verdict
	}
	rows, err := s.q.ListNodePeerStatusForOrg(ctx, orgID)
	if err != nil {
		return // can't read the substrate → no verdict (never a false headline)
	}
	live := deriveMemberLiveness(members, pubkey, rows, demoted, now)
	b.siteLinkHeadlineDown, b.demotedDeadPeer = siteLinkVerdictFrom(members, activePrimary, live)
	// WF-C L2 (D-WFC2-1a): surface each member's WIRE freshness from THE SAME liveness map (no recompute) —
	// the zombie-hub kind's wire half. Only observed-AND-fresh members are true; silence/staleness → false.
	b.memberWireFresh = make(map[uuid.UUID]bool, len(live))
	for id, ml := range live {
		b.memberWireFresh[id] = ml.Observed && ml.Fresh
	}
}

// siteLinkVerdictFrom is the PURE WF-B verdict (unit-pinnable, no DB): given the hub members, the active
// primary, and THE ONE liveness map (deriveMemberLiveness), returns (headlineDown, demotedDeadPeer).
//   - HEADLINE: the active primary is observed-but-stale → org transit is genuinely dead. A real transit
//     failure is the headline and NO subordinate note competes with it (the inverse-red guard).
//   - SUBORDINATE: else, a DEMOTED member observed-but-stale while the primary is fresh — the walk's exact
//     case (transit rides the primary at 0% loss; the demoted-dead link is a named line, not the headline).
//   - A no-witness (unobserved) member yields neither — never a headline-down on silence.
func siteLinkVerdictFrom(members []uuid.UUID, activePrimary uuid.UUID, live map[uuid.UUID]MemberLiveness) (headlineDown bool, demotedDead uuid.UUID) {
	ap, apObserved := live[activePrimary]
	if apObserved && !ap.Fresh {
		return true, uuid.Nil // primary observed-STALE → org transit is genuinely dead → the headline
	}
	if !apObserved {
		// SILENCE on the primary yields NOTHING — no headline (silence ≠ death) AND no subordinate. A
		// subordinate note asserts "transit is fine, only this demoted link is down"; an UNOBSERVED primary
		// is precisely the state where we cannot assert transit is fine, so a reassuring subordinate here is
		// the reassuring-green class rebuilt one tier down — the combination the inverse-red didn't cover
		// (WF-B review F1). Silence yields nothing: no headline, no subordinate, no reassurance.
		return false, uuid.Nil
	}
	// primary observed-FRESH → transit is healthy; a DEMOTED member observed-stale is the subordinate note.
	for _, id := range members {
		if ml := live[id]; ml.Demoted && ml.Observed && !ml.Fresh {
			return false, id
		}
	}
	return false, uuid.Nil
}

// siteTopoBatchFor returns the caller-provided prefetched batch, or loads one — so a caller that passes no
// batch (every existing test) behaves EXACTLY as before (one load), while ListNodes passes a shared batch.
func (s *Service) siteTopoBatchFor(ctx context.Context, orgID uuid.UUID, nodes []sqlc.Node, pre []SiteTopoBatch) SiteTopoBatch {
	if len(pre) > 0 {
		return pre[0]
	}
	return s.LoadSiteTopoBatch(ctx, orgID, nodes)
}

// NodeDisplayExtrasForNodes returns the hub designation + reported max-version per node. The hub is a
// PROJECTION of the ONE election (electSiteHub, from the shared batch — not a second one). Max-version is
// read from each node's caps JSONB (absence → 0). All nodes must belong to orgID. Pass the shared batch to
// avoid reloading the topology (review #3).
func (s *Service) NodeDisplayExtrasForNodes(ctx context.Context, orgID uuid.UUID, nodes []sqlc.Node, pre ...SiteTopoBatch) map[uuid.UUID]NodeDisplayExtras {
	out := make(map[uuid.UUID]NodeDisplayExtras, len(nodes))
	b := s.siteTopoBatchFor(ctx, orgID, nodes, pre)
	for _, n := range nodes {
		var caps NodeCapabilities
		_ = json.Unmarshal(n.Capabilities, &caps) // absent/garbage caps → zero-value (MaxPolicyVersion 0)
		out[n.ID] = NodeDisplayExtras{IsSiteHub: b.hasHub && n.SiteID.Valid && n.ID == b.hubID, MaxPolicyVersion: caps.MaxPolicyVersion}
	}
	return out
}

// PolicyHealthForNodes computes both the bool and the advisory kind from a SINGLE org compile.
// Atomicity unit = everything the render consumes, per node, from one snapshot: the pushed hash
// (one CompiledHashesForNodes), the caps, the CP-stamped onset, and the report-freshness — all
// from the same node row + the same pushed map. (Residual: the node rows are read by ListNodes
// slightly before this compile; a push in that gap makes pushed reflect the new policy while
// applied reflects the old — which is a REAL just-pushed desync and correctly renders
// `converging`, so it is harmless, not a suppressed alarm.)
func (s *Service) PolicyHealthForNodes(ctx context.Context, orgID uuid.UUID, nodes []sqlc.Node, pre ...SiteTopoBatch) map[uuid.UUID]PolicyHealth {
	out := make(map[uuid.UUID]PolicyHealth, len(nodes))
	enterprise := s.policy != nil
	// Site topology — loaded ONCE for the batch, in BOTH editions (site-link routing + its health are
	// CORE, D11). Only when some node is a site gateway. Drives site_hub_down (B2: no carrier) AND
	// finalizes the pushed hash (enterprise) the SAME way the served artifact is (#1: no false desync).
	// The batch is loaded here when unset (existing callers) or shared from ListNodes (review #3) — same
	// topo + hub, so the health output is byte-identical either way.
	b := s.siteTopoBatchFor(ctx, orgID, nodes, pre)
	topo := b.topo
	topoOK := b.ok
	hubExists := b.hasHub // R5: the ONE election (== siteTopoHasHub(topo)), computed once for the batch
	var pushed map[uuid.UUID]string
	pushKnown := false
	if enterprise && topoOK {
		ids := make([]uuid.UUID, len(nodes))
		for i, n := range nodes {
			ids[i] = n.ID
		}
		// err (transient compile/DB) -> pushKnown stays false: term (3) can't be evaluated, but the
		// agent-reported terms still apply. A transient control-plane hiccup is not a gateway fault.
		// S8.6 REDUCE #1: the batch's ONE elected hub (b.hubID, computed once via electSiteHub in the
		// SiteTopoBatch) is threaded in — the pushed-hash baseline cites the same hub the served artifact
		// does. uuid.Nil when the batch has no hub.
		if arts, err := s.policy.CompiledArtifactsForNodes(ctx, orgID, ids, b.hubID); err == nil {
			pushed = make(map[uuid.UUID]string, len(nodes))
			for _, n := range nodes {
				pushed[n.ID] = s.pushedHash(topo, n, arts[n.ID])
			}
			pushKnown = true
		}
	}
	now := time.Now()
	// WF-B: resolve the demoted-dead peer's display NAME once (from the node list — the peer is a hub
	// member, present in an org-wide ListNodes). Fallback to a short id if a subset call omits it.
	nameByID := make(map[uuid.UUID]string, len(nodes))
	for _, n := range nodes {
		nameByID[n.ID] = n.Name
	}
	subPeerName := ""
	if b.demotedDeadPeer != uuid.Nil {
		if nm := nameByID[b.demotedDeadPeer]; nm != "" {
			subPeerName = nm
		} else {
			subPeerName = b.demotedDeadPeer.String()[:8]
		}
	}
	for _, n := range nodes {
		caps := Capabilities(n.Capabilities)
		// Site-link health (S8.2, edition-independent — routing is core). site_hub_down (B2): this site
		// gateway has remote subnets to reach but the org has NO hub (no carrier) — CP-derived from the
		// topology. site_link_down (H5, WF-B): the ORG-LEVEL site-link HEADLINE — the ACTIVE PRIMARY hub is
		// stale (org transit dead), derived from THE ONE liveness derivation (SiteTopoBatch.fillSiteLink
		// Verdict), applied to every site gateway. NOT caps.SiteLinkStale: the agent bool is RETIRED from
		// consumption (D-WFB-1b) — it cannot name a peer or know demotion, so the CP derivation replaces it
		// as the one truth. The field stays reported in the caps payload (backward-compat), VESTIGIAL until
		// an agent-vN drops or re-adopts it deliberately (dormant-data guard: not silently resurrected).
		siteHubDown := topoOK && siteHubMissing(hubExists, topo, n)
		siteLinkDown := n.SiteID.Valid && b.siteLinkHeadlineDown
		// site_subnet_unreachable (S8.2c D3): the gateway advertises a local subnet it isn't on
		// (bridge-trapped wg0 / misconfig). A REACHABILITY fault the agent detects even when the link is
		// fresh — the reassuring-green trap. Edition-independent (routing is core, D11).
		siteSubnetUnreachable := caps.SiteSubnetUnreachable
		// conntrack_flush_unavailable (S8.7 Slice 2): the agent's expired-grant flush is failing — a
		// LOWEST-priority enforcement-hygiene degradation (revoked grants' flows may linger). Only fires in
		// enterprise (an open gateway has no grants → no flush → never set).
		conntrackFlushUnavailable := caps.ConntrackFlushUnavailable
		// hub_forwarding_not_reconciling (WF-C L2 D-WFC2-1a): the zombie-hub CONJUNCTION — this hub-set
		// member's wire is FRESH (b.memberWireFresh, from THE ONE liveness derivation) while its OWN agent is
		// SILENT (last_seen stale, the SAME hubStaleWindow the hub ordering uses — no new freshness). A dead
		// agent forwarding headless: stale-enforcement, edition-independent. A non-member is absent from the
		// map → false, so this can only fire for a hub-set member. (last_seen absent = never-seen = dead too.)
		agentDead := !(n.LastSeenAt.Valid && now.Sub(n.LastSeenAt.Time) < hubStaleWindow)
		hubForwardingNotReconciling := b.memberWireFresh[n.ID] && agentDead
		// A refused (unsupported-version) gateway is deny-all — definitively degraded,
		// edition-independent (S8.1 D1). Terms (1)+(2) are the agent-reported apply faults.
		deg := caps.PolicyError != "" || caps.PolicyFailingSince != "" || caps.PolicyRefusedVersion > 0 || siteHubDown || siteLinkDown || siteSubnetUnreachable || conntrackFlushUnavailable || hubForwardingNotReconciling
		if !deg && pushKnown {
			if h := pushed[n.ID]; h != "" && h != caps.PolicyHash { // term (3)
				deg = true
			}
		}
		// [fold 8] the open build has NO policy engine → no desync path. The kind must AGREE
		// with the bool structurally (not just architecturally): if caps somehow carry an apply
		// error, reflect it (apply_failing/stuck) so {Degraded,Kind} can't disagree; else healthy.
		// (Normally the open agent reports neither field — this is the structural guarantee.)
		kind := KindHealthy
		switch {
		case !enterprise && caps.PolicyRefusedVersion > 0:
			// S8.1 D1: the version gate is on the AGENT — edition-independent. An open-build
			// gateway has no policy engine (no desync path) but still refuses a too-new artifact.
			kind = KindUnsupportedPolicyVersion
		case !enterprise && siteHubDown:
			kind = KindSiteHubDown // edition-independent (routing/peers are core, D11)
		case !enterprise && siteLinkDown:
			kind = KindSiteLinkDown
		case !enterprise && siteSubnetUnreachable:
			kind = KindSiteSubnetUnreachable // D3, edition-independent
		case !enterprise && hubForwardingNotReconciling:
			// WF-C L2 (D-WFC2-1a): zombie hub — edition-independent (a crashed agent is core). Ranked above
			// the apply kinds (a dead agent's stale report must not mask it), below the site-reachability kinds.
			kind = KindHubForwardingNotReconciling
		case !enterprise && caps.PolicyFailingSince != "":
			kind = KindApplyFailing
		case !enterprise && caps.PolicyError != "":
			kind = KindStuckEnforcing
		case !enterprise && conntrackFlushUnavailable:
			// [3] LOWEST priority — ranked AFTER the apply faults so a louder failure is never masked by the
			// hygiene label (structural agreement; open never sets it — no grants).
			kind = KindConntrackFlushUnavailable
		case enterprise:
			kind = degradedKind(KindInput{
				PolicyError:                 caps.PolicyError,
				PolicyFailingSince:          caps.PolicyFailingSince,
				PushKnown:                   pushKnown,
				PushedHash:                  pushed[n.ID],
				AppliedHash:                 caps.PolicyHash,
				DesyncSince:                 tsTime(n.PolicyDesyncSince),
				ReportAge:                   reportAge(now, n.PolicyReportedAt), // [fold 1] the REPORT clock, not last_seen
				Now:                         now,
				UnsupportedVersion:          caps.PolicyRefusedVersion > 0, // S8.1 D1: highest-priority kind
				SiteHubDown:                 siteHubDown,                   // S8.2 Item 7/9 (B2)
				SiteLinkDown:                siteLinkDown,                  // S8.2 H5
				SiteSubnetUnreachable:       siteSubnetUnreachable,         // S8.2c D3
				ConntrackFlushUnavailable:   conntrackFlushUnavailable,     // S8.7 Slice 2 (lowest priority)
				HubForwardingNotReconciling: hubForwardingNotReconciling,   // WF-C L2 D-WFC2-1a (zombie hub)
			})
		}
		ph := PolicyHealth{Degraded: deg, Kind: kind}
		// WF-B subordinate note: a DEMOTED member is dead while transit rides the active primary. Attach the
		// named line to every OTHER site gateway (not the dead peer itself — it renders offline), and NEVER
		// when this node's own headline is site_link_down (the inverse-red guard: a real transit failure gets
		// no reassuring subordinate). INDEPENDENT of Kind — the headline stays its real state.
		if subPeerName != "" && n.SiteID.Valid && n.ID != b.demotedDeadPeer && !siteLinkDown {
			ph.SiteLinkNotePeer = subPeerName
			ph.SiteLinkNoteDemoted = true
		}
		out[n.ID] = ph
	}
	return out
}

// tsTime unwraps a nullable timestamp to a zero-or-value time.
func tsTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

// reportAge is how long since the node last REPORTED its applied policy (policy_reported_at,
// [fold 1] — NOT last_seen_at, which polls also bump). NULL (never reported / pre-migration) →
// forever-stale → desync_unknown on the desync path, NEVER fresh.
func reportAge(now time.Time, reportedAt pgtype.Timestamptz) time.Duration {
	if !reportedAt.Valid {
		return 1<<62 - 1 // effectively "forever stale"
	}
	return now.Sub(reportedAt.Time)
}

// Capabilities decodes a node row's capabilities JSONB (an empty/invalid value → all
// false, the safe default: no capability claimed).
func Capabilities(raw []byte) NodeCapabilities {
	var c NodeCapabilities
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

// Revoke marks a node revoked (renewal will then be refused).
func (s *Service) Revoke(ctx context.Context, actor, orgID, nodeID uuid.UUID) error {
	// Is this node a SITE GATEWAY? (Read before the revoke — RevokeNode leaves site_id set, so it reads the
	// same either way, but this is the pre-revoke truth.) S8.6 #9: only a gateway revoke reconciles the hub
	// set; a non-gateway device revoke must NOT churn a full reconcile.
	binding, bErr := s.q.GetNodeSiteBinding(ctx, sqlc.GetNodeSiteBindingParams{ID: nodeID, OrgID: orgID})
	wasGateway := bErr == nil && binding.Valid
	if err := s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.RevokeNode(ctx, sqlc.RevokeNodeParams{OrgID: orgID, ID: nodeID}); e != nil {
			return e
		}
		// Cascade: the node's peers can no longer reach a gateway, so revoke them
		// too — no dangling active devices counting against caps or peer lists.
		if _, e := q.RevokeDevicesForNode(ctx, nodeID); e != nil {
			return e
		}
		return audit(ctx, q, orgID, &actor, "node.revoked", "node", nodeID.String(), map[string]any{})
	}); err != nil {
		return err
	}
	// S8.6 #4/#9: a revoked GATEWAY left the hub-set candidate pool (status='active' filter) → re-elect +
	// persist so the drop is durable + audited immediately. Best-effort belt: a hiccup self-heals on the next
	// failover tick (the configured corrector). Gated on gateway-ness — a laptop revoke is a no-op here.
	if wasGateway {
		if _, err := s.ReconcileHubSet(ctx, orgID); err != nil {
			slog.WarnContext(ctx, "hub_set_reconcile_failed", "op", "revoke", "org_id", orgID.String(), "error", err.Error())
		}
	}
	return nil
}

// ListNodes returns an org's nodes.
func (s *Service) ListNodes(ctx context.Context, orgID uuid.UUID) ([]sqlc.Node, error) {
	return s.q.ListNodes(ctx, orgID)
}

func newToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }

func audit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, actor *uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b := []byte("{}")
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	actorID := pgtype.UUID{}
	if actor != nil {
		actorID = pgtype.UUID{Bytes: [16]byte(*actor), Valid: true}
	}
	_, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID: pgtype.UUID{Bytes: [16]byte(orgID), Valid: true}, ActorUserID: actorID,
		Action: action, TargetType: &targetType, TargetID: &targetID, Metadata: b,
	})
	return err
}
