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
	"sort"
	"strconv"
	"strings"
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
const ProtocolVersion = 5

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
	CompiledForNode(ctx context.Context, orgID, nodeID uuid.UUID) (*policyspec.Compiled, error)
	// CompiledArtifactsForNodes returns each node's ROUTE-LESS compiled artifact with a SINGLE org
	// snapshot build — the batch counterpart to CompiledForNode. Route-less by design: the CORE
	// finalizeArtifact/pushedHash attach the site routes + derive the version, so the pushed-hash
	// baseline is computed the SAME way the served artifact is (the #1 single-source fix). nil for a node
	// with no enforcement artifact (off / device-less-off). Avoids an N+1 recompile per node (finding #5).
	CompiledArtifactsForNodes(ctx context.Context, orgID uuid.UUID, nodeIDs []uuid.UUID) (map[uuid.UUID]*policyspec.Compiled, error)
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
}

// NewService builds the node service.
func NewService(pool *pgxpool.Pool, ca *agentca.CA, sealer *crypto.Sealer) *Service {
	s := &Service{pool: pool, q: sqlc.New(pool), ca: ca, sealer: sealer}
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
	if s.policy != nil {
		pol, err := s.policy.CompiledForNode(ctx, node.OrgID, node.ID)
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

	// S8.2 site-to-site plumbing (CORE, all editions): if this node is a site gateway, add its site-link
	// WG peers (hub-and-spoke) + kernel routes, from the org's site topology loaded ONCE. finalizeArtifact
	// is the SINGLE SOURCE that attaches routes + derives the content version — the SAME step the
	// pushed-hash path (trackDesync / PolicyHealthForNodes) calls, so the served artifact and the desync
	// baseline can NEVER disagree about the artifact's contents (the #1 fix: two compile paths agreeing).
	if node.SiteID.Valid {
		load := s.siteTopoLoad
		if load == nil { // directly-constructed Service (tests) → the real loader
			load = s.loadSiteTopology
		}
		topo, terr := load(ctx, node.OrgID)
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
		peers, _ := siteLinkGraphFrom(topo, node)
		ds.Peers = append(ds.Peers, peers...)
		ds.Policy = s.finalizeArtifact(topo, node, ds.Policy)
	}
	return ds, nil
}

// siteTopology is the org's site-link input, loaded ONCE (loadSiteTopology) and consumed per-node by the
// PURE siteLinkGraphFrom / finalizeArtifact. Loading once lets the batch pushed-hash path finalize N
// nodes off a single pair of org queries instead of N (and the served path uses the same shape).
type siteTopology struct {
	gws     []sqlc.ListSiteGatewaysForOrgRow
	subnets map[uuid.UUID][]string // site_id -> approved subnet CIDRs
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
	return siteTopology{gws: gws, subnets: sub}, nil
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
// electSiteHub picks the org's transit HUB — the endpoint-bearing site gateway with the lowest id
// (single hub v1; multi-hub reserved). Returns nil when no gateway has a public endpoint (all NAT'd) —
// the B2 no-carrier condition. This is THE election: every hub fact (site-link peers, routes, AND the
// `is_site_hub` API projection) reads it, so the designation has exactly ONE source and the UI never
// re-elects (the D2 overrule). PURE.
func electSiteHub(topo siteTopology) *sqlc.ListSiteGatewaysForOrgRow {
	var hub *sqlc.ListSiteGatewaysForOrgRow
	for i := range topo.gws {
		g := &topo.gws[i]
		if g.Endpoint != "" && (hub == nil || g.ID.String() < hub.ID.String()) {
			hub = g
		}
	}
	return hub
}

func siteLinkGraphFrom(topo siteTopology, node sqlc.Node) ([]Peer, []policyspec.Route) {
	if !node.SiteID.Valid {
		return nil, nil
	}
	hub := electSiteHub(topo)
	// B2: no hub (every gateway is NAT'd, no public endpoint) → NO carrier for site traffic, so emit NO
	// routes and NO peers. Installing routes with no peer to carry them is the silent blackhole; the
	// no-hub condition is surfaced CP-side as site_hub_down (PolicyHealthForNodes), never a silent no-op.
	if hub == nil {
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

	var peers []Peer
	switch {
	case hub.ID == node.ID: // this node IS the hub (guaranteed non-nil by the B2 guard above)
		for i := range topo.gws {
			g := &topo.gws[i]
			if g.ID == node.ID {
				continue
			}
			ips := append([]string(nil), topo.subnets[uuid.UUID(g.SiteID.Bytes)]...)
			if len(ips) == 0 {
				continue // a spoke advertising no subnets yet contributes no crypto-routing
			}
			sort.Strings(ips)
			peers = append(peers, Peer{PublicKey: g.WgPublicKey, AllowedIPs: ips, Endpoint: g.Endpoint, SiteLink: true, PersistentKeepalive: siteLinkKeepaliveSecs})
		}
	default: // this node is a SPOKE → peer only with the hub
		if len(routeCIDRs) > 0 {
			peers = append(peers, Peer{PublicKey: hub.WgPublicKey, AllowedIPs: append([]string(nil), routeCIDRs...), Endpoint: hub.Endpoint, SiteLink: true, PersistentKeepalive: siteLinkKeepaliveSecs})
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
	if pol != nil {
		pol.Routes = routes
		pol.LocalSubnets = local
		pol.Version = policyspec.RequiredVersion(*pol)
		return pol
	}
	c := policyspec.Compiled{NodeID: node.ID.String(), Mode: "off", Mesh: true, Routes: routes, LocalSubnets: local}
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
	return electSiteHub(topo) != nil // one election: hub existence reads the same picker as the designation
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
	for _, st := range stats {
		var hs pgtype.Timestamptz
		if st.LastHandshake > 0 && st.LastHandshake <= maxHS {
			hs = pgtype.Timestamptz{Time: time.Unix(st.LastHandshake, 0).UTC(), Valid: true}
		}
		params = append(params, sqlc.UpsertDeviceStatusParams{
			NodeID: node.ID, PublicKey: st.PublicKey, LastHandshakeAt: hs,
			RxBytes: st.RxBytes, TxBytes: st.TxBytes,
		})
	}
	// br.Exec closes the batch results itself, so we do not Close separately.
	var firstErr error
	s.q.UpsertDeviceStatus(ctx, params).Exec(func(_ int, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	})
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
		"egress_nat":             egressNAT,
		"policy_version":         applied.Version,
		"policy_hash":            applied.Hash,
		"policy_error":           applied.Error,
		"policy_failing_since":   applied.FailingSince,
		"policy_refused_version": applied.RefusedVersion,
		"site_link_stale":         applied.SiteLinkStale,
		"site_subnet_unreachable": applied.SiteSubnetUnreachable,
		"max_policy_version":     applied.MaxSupportedVersion,
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
	arts, err := s.policy.CompiledArtifactsForNodes(ctx, node.OrgID, []uuid.UUID{node.ID})
	if err != nil {
		return // pushed artifact unavailable (compile fault) → can't-determine; never stamp/clear
	}
	// The pushed hash is finalized the SAME way the served artifact is (route-attach + version), so a
	// route-carrying enforcing gateway compares clean instead of a false silent_desync (the #1 fix). Only
	// a SITE gateway needs the topology (finalizeArtifact no-ops for non-site nodes) — skip the queries
	// otherwise.
	var topo siteTopology
	if node.SiteID.Valid {
		t, terr := s.loadSiteTopology(ctx, node.OrgID)
		if terr != nil {
			return // topology unavailable → can't-determine; never stamp/clear on a partial baseline
		}
		topo = t
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
		if t, err := s.loadSiteTopology(ctx, orgID); err == nil {
			b.topo = t
			if hub := electSiteHub(t); hub != nil {
				b.hubID, b.hasHub = hub.ID, true
			}
		} else {
			b.ok = false // load failed → can't determine hub-health (never a wrong designation)
		}
	}
	return b
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
		if arts, err := s.policy.CompiledArtifactsForNodes(ctx, orgID, ids); err == nil {
			pushed = make(map[uuid.UUID]string, len(nodes))
			for _, n := range nodes {
				pushed[n.ID] = s.pushedHash(topo, n, arts[n.ID])
			}
			pushKnown = true
		}
	}
	now := time.Now()
	for _, n := range nodes {
		caps := Capabilities(n.Capabilities)
		// Site-link health (S8.2, edition-independent — routing is core). site_hub_down (B2): this site
		// gateway has remote subnets to reach but the org has NO hub (no carrier) — CP-derived from the
		// topology. site_link_down (H5): agent-reported stale/absent site-link handshake. Both are
		// blackholes that must never read green.
		siteHubDown := topoOK && siteHubMissing(hubExists, topo, n)
		siteLinkDown := caps.SiteLinkStale
		// site_subnet_unreachable (S8.2c D3): the gateway advertises a local subnet it isn't on
		// (bridge-trapped wg0 / misconfig). A REACHABILITY fault the agent detects even when the link is
		// fresh — the reassuring-green trap. Edition-independent (routing is core, D11).
		siteSubnetUnreachable := caps.SiteSubnetUnreachable
		// A refused (unsupported-version) gateway is deny-all — definitively degraded,
		// edition-independent (S8.1 D1). Terms (1)+(2) are the agent-reported apply faults.
		deg := caps.PolicyError != "" || caps.PolicyFailingSince != "" || caps.PolicyRefusedVersion > 0 || siteHubDown || siteLinkDown || siteSubnetUnreachable
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
		case !enterprise && caps.PolicyFailingSince != "":
			kind = KindApplyFailing
		case !enterprise && caps.PolicyError != "":
			kind = KindStuckEnforcing
		case enterprise:
			kind = degradedKind(KindInput{
				PolicyError:        caps.PolicyError,
				PolicyFailingSince: caps.PolicyFailingSince,
				PushKnown:          pushKnown,
				PushedHash:         pushed[n.ID],
				AppliedHash:        caps.PolicyHash,
				DesyncSince:        tsTime(n.PolicyDesyncSince),
				ReportAge:          reportAge(now, n.PolicyReportedAt), // [fold 1] the REPORT clock, not last_seen
				Now:                now,
				UnsupportedVersion: caps.PolicyRefusedVersion > 0, // S8.1 D1: highest-priority kind
				SiteHubDown:           siteHubDown,           // S8.2 Item 7/9 (B2)
				SiteLinkDown:          siteLinkDown,          // S8.2 H5
				SiteSubnetUnreachable: siteSubnetUnreachable, // S8.2c D3
			})
		}
		out[n.ID] = PolicyHealth{Degraded: deg, Kind: kind}
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
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.RevokeNode(ctx, sqlc.RevokeNodeParams{OrgID: orgID, ID: nodeID}); e != nil {
			return e
		}
		// Cascade: the node's peers can no longer reach a gateway, so revoke them
		// too — no dangling active devices counting against caps or peer lists.
		if _, e := q.RevokeDevicesForNode(ctx, nodeID); e != nil {
			return e
		}
		return audit(ctx, q, orgID, &actor, "node.revoked", "node", nodeID.String(), map[string]any{})
	})
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
