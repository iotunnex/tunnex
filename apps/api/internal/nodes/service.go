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

// ProtocolVersion is the control-plane protocol version. The control plane must
// serve agents at version N and N-1 during rolling upgrades.
const ProtocolVersion = 1

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
	// CompiledHashesForNodes returns each node's canonical pushed-hash with a SINGLE
	// org snapshot build — the batch read-path counterpart to CompiledForNode that the
	// node-list status uses to avoid an N+1 recompile per node (finding #5).
	CompiledHashesForNodes(ctx context.Context, orgID uuid.UUID, nodeIDs []uuid.UUID) (map[uuid.UUID]string, error)
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
}

// NewService builds the node service.
func NewService(pool *pgxpool.Pool, ca *agentca.CA, sealer *crypto.Sealer) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), ca: ca, sealer: sealer}
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
			// can CONFIRM the org has Zero Trust OFF, serve the mesh (an explicit blanket
			// artifact) — a policy-subsystem blip must not blackhole an org that never
			// opted into enforcement, imposing an availability dependency on a subsystem it
			// doesn't use. The mesh artifact mirrors the compiler's off-mode output so the
			// applied-hash stays consistent with a normal off push.
			slog.Warn("policy_compile_failed_org_off_serving_mesh",
				slog.String("node_id", node.ID.String()), slog.String("error", err.Error()))
			ds.Policy = &policyspec.Compiled{
				Version: ProtocolVersion, NodeID: node.ID.String(), Mode: zeroTrustOff, Mesh: true,
			}
		default:
			// Enforcing, OR the org mode is UNKNOWN (org row unreadable): FAIL CLOSED. An
			// enforcing org must never revert to the open mesh on a policy error, and if we
			// cannot confirm the mode we assume the boundary is in force. Serve the peers;
			// lock the policy to a deny-all enforcing artifact. (nil would decode as mesh =
			// fail-OPEN.)
			slog.Warn("policy_compile_failed_failing_closed",
				slog.String("node_id", node.ID.String()), slog.String("error", err.Error()))
			ds.Policy = &policyspec.Compiled{
				Version: ProtocolVersion, NodeID: node.ID.String(), Mode: "enforcing", Mesh: false,
			}
		}
	}
	return ds, nil
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
		"egress_nat":           egressNAT,
		"policy_version":       applied.Version,
		"policy_hash":          applied.Hash,
		"policy_error":         applied.Error,
		"policy_failing_since": applied.FailingSince,
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
	return nil
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
}

// PolicyStaleAfter is the staleness alarm window (S7.2 decision): pushed != applied
// is an ALARM only when it persists longer than this -- 3 report intervals (the
// report loop runs every 30s; a normal apply window is <=2s of push->fetch->apply
// plus up to one 30s report cycle before the new applied hash lands, so a mismatch
// older than 90s cannot be a normal window). policy_error is surfaced IMMEDIATELY,
// regardless of hash state -- an apply failure is never smoothed by this window.
const PolicyStaleAfter = 90 * time.Second

// zeroTrustOff mirrors organizations.zero_trust_mode = 'off' (the compiler's ModeOff).
// Kept as a neutral local const so the open build never imports enterprise/policy.
const zeroTrustOff = "off"

// PolicyStatus surfaces a node's Zero Trust policy health for the API (finding #5/#7):
//   - stale:  apply has been FAILING longer than PolicyStaleAfter (from failingSince) —
//     the alarm-worthy case (a gateway that cannot apply the pushed policy).
//   - synced: the policy IN FORCE (reported applied hash) equals what the control plane
//     would push right now (freshly compiled canonical hash). synced=false catches
//     SILENT staleness — a gateway that never fetched the new policy — which the
//     failing-apply signal alone misses. The dashboard BADGE (debounce/UX) is S7.4;
//     this raw field is S7.2 so proof-7's observed surface reaches production.
//
// Open build / no policy provider: stale=false, synced=true (no policy to be out of sync).
func (s *Service) PolicyStatus(ctx context.Context, node sqlc.Node, now time.Time) (stale, synced bool) {
	caps := Capabilities(node.Capabilities)
	stale = caps.PolicyStale(now)
	synced = true
	if s.policy != nil {
		pol, err := s.policy.CompiledForNode(ctx, node.OrgID, node.ID)
		if err != nil {
			// COULD NOT DETERMINE what we'd push right now (transient compile/DB error).
			// That is NOT evidence of a desync — reporting synced=false here would raise a
			// false out-of-sync alarm on a HEALTHY gateway, training operators to ignore
			// the signal (the same class of false alarm as the #3 false-staleness). Leave
			// synced=true (unknown != desynced); a genuine desync still surfaces on the
			// next successful compile (finding #4).
			return stale, true
		}
		pushed := ""
		if pol != nil {
			pushed = policyspec.CanonicalHash(*pol)
		}
		synced = pushed == caps.PolicyHash
	}
	return stale, synced
}

// PolicyStatusForNodes is the batch read-path form of PolicyStatus (finding #5): it
// computes every node's (stale, synced) with a SINGLE org policy compile instead of one
// per node. All nodes must belong to orgID. Same signals as PolicyStatus — stale is pure
// (from caps), synced compares the node's applied hash to the pushed hash computed once —
// and the same #4 rule: an errored/unknown compile reports synced=true (never a false
// desync), because a transient control-plane hiccup is not a gateway fault.
func (s *Service) PolicyStatusForNodes(ctx context.Context, orgID uuid.UUID, nodes []sqlc.Node, now time.Time) (stale, synced map[uuid.UUID]bool) {
	stale = make(map[uuid.UUID]bool, len(nodes))
	synced = make(map[uuid.UUID]bool, len(nodes))
	var pushed map[uuid.UUID]string
	if s.policy != nil {
		ids := make([]uuid.UUID, len(nodes))
		for i, n := range nodes {
			ids[i] = n.ID
		}
		// err (transient) -> pushed stays nil -> synced=true for all (finding #4: unknown
		// is not a desync).
		if h, err := s.policy.CompiledHashesForNodes(ctx, orgID, ids); err == nil {
			pushed = h
		}
	}
	for _, n := range nodes {
		caps := Capabilities(n.Capabilities)
		stale[n.ID] = caps.PolicyStale(now)
		if pushed == nil {
			synced[n.ID] = true
			continue
		}
		synced[n.ID] = pushed[n.ID] == caps.PolicyHash
	}
	return stale, synced
}

// PolicyStale reports whether this node is running STALE policy: apply has been
// FAILING (applied != desired) for longer than PolicyStaleAfter. Measured from the
// mismatch ONSET (PolicyFailingSince), so a healthy node (no failing_since) is never
// stale and a normal push that applies within the window never false-alarms (#3).
// policy_error is surfaced IMMEDIATELY elsewhere; this is the persistent alarm.
func (c NodeCapabilities) PolicyStale(now time.Time) bool {
	if c.PolicyFailingSince == "" {
		return false // apply is healthy -> not stale
	}
	t, err := time.Parse(time.RFC3339, c.PolicyFailingSince)
	if err != nil {
		return true // corrupt mark while failing: fail toward visibility
	}
	return now.Sub(t) > PolicyStaleAfter
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
