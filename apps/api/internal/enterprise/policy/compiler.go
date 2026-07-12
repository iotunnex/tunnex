//go:build enterprise

// Package policy is the enterprise Zero Trust policy engine (S7.1): the CRUD
// service plus the pure Compile function that turns the stored model (groups,
// resources, allow-rules, org mode) into the per-node compiled artifact
// (policyspec.Compiled) the control plane pushes to agents.
//
// Compile is a PURE function of a Snapshot (a plain-data view of DB state) — no
// database, no clock, no I/O — so the security-critical policy decision is
// exhaustively unit-testable and DETERMINISTIC (equal input => byte-identical
// output, keeping reconcile a steady-state no-op). The service layer builds the
// Snapshot from sqlc rows; the model is enterprise-gated at the API, so this code
// only runs in the enterprise build (open build never imports it).
package policy

import (
	"sort"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// Modes (mirror the organizations.zero_trust_mode CHECK).
const (
	ModeOff       = "off"
	ModeEnforcing = "enforcing"
)

// Rule is an allow grant: members of SrcGroupID may reach the destination. DstKind
// selects which Dst*ID is meaningful ("resource" => static cidr:ports; "group" =>
// that group's members' device /32s).
type Rule struct {
	SrcGroupID    uuid.UUID
	DstKind       string
	DstResourceID uuid.UUID
	DstGroupID    uuid.UUID
}

// Resource is a static destination (a CIDR + optional L4 scope).
type Resource struct {
	ID       uuid.UUID
	CIDR     string
	Protocol string // any | tcp | udp
	PortLow  int    // 0 => unset
	PortHigh int    // 0 => unset
}

// Membership is one (group, user) pair.
type Membership struct {
	GroupID uuid.UUID
	UserID  uuid.UUID
}

// Device is an active peer: its owner, its gateway, and its assigned host address
// (no prefix). Only active devices owned by active users appear (the service query
// filters); a revoked device is simply absent, so its /32 leaves the output as both
// a source and a destination (the A1/A2 requirement — no inherited grants on IP reuse).
type Device struct {
	UserID     uuid.UUID
	NodeID     uuid.UUID
	AssignedIP string
}

// Snapshot is the full org policy state the compiler consumes.
type Snapshot struct {
	Mode        string
	Rules       []Rule
	Resources   []Resource
	Memberships []Membership
	Devices     []Device
}

// Compile produces the compiled artifact for every node that has at least one
// active device in the snapshot.
//
//   - Mode "off": each node gets a blanket-mesh artifact (Mesh=true, no allows) —
//     the legacy pre-Zero-Trust behavior, so enabling the feature is opt-in.
//   - Mode "enforcing" (and, fail-closed, ANY non-"off" value): each node gets a
//     default-deny artifact (Mesh=false) whose Allow set is EXACTLY the grants that
//     resolve for the devices on that node — the empty set if none (deny-all).
//
// The enforcing path can never set Mesh=true, so it is structurally incapable of
// reproducing the wg0<->wg0 blanket accept it replaces.
func Compile(s Snapshot) map[uuid.UUID]policyspec.Compiled {
	mesh := s.Mode == ModeOff

	// Nodes in play = nodes that have at least one active device.
	nodeSet := map[uuid.UUID]bool{}
	for _, d := range s.Devices {
		if d.AssignedIP == "" {
			continue
		}
		nodeSet[d.NodeID] = true
	}

	out := make(map[uuid.UUID]policyspec.Compiled, len(nodeSet))

	if mesh {
		for nodeID := range nodeSet {
			out[nodeID] = policyspec.Compiled{
				Version: policyspec.ProtocolVersion,
				NodeID:  nodeID.String(),
				Mode:    ModeOff,
				Mesh:    true,
				Allow:   nil,
			}
		}
		return out
	}

	// ── enforcing: resolve grants ────────────────────────────────────────────────
	// user -> set of groups
	userGroups := map[uuid.UUID]map[uuid.UUID]bool{}
	for _, m := range s.Memberships {
		g := userGroups[m.UserID]
		if g == nil {
			g = map[uuid.UUID]bool{}
			userGroups[m.UserID] = g
		}
		g[m.GroupID] = true
	}

	resourceByID := make(map[uuid.UUID]Resource, len(s.Resources))
	for _, r := range s.Resources {
		resourceByID[r.ID] = r
	}

	// group -> sorted, de-duplicated member device /32 hosts (destination resolution
	// for dst_kind=group). A device belongs to a group iff its OWNER is in the group.
	groupDeviceIPs := map[uuid.UUID][]string{}
	{
		seen := map[uuid.UUID]map[string]bool{}
		for _, d := range s.Devices {
			if d.AssignedIP == "" {
				continue
			}
			for g := range userGroups[d.UserID] {
				gs := seen[g]
				if gs == nil {
					gs = map[string]bool{}
					seen[g] = gs
				}
				if !gs[d.AssignedIP] {
					gs[d.AssignedIP] = true
					groupDeviceIPs[g] = append(groupDeviceIPs[g], d.AssignedIP)
				}
			}
		}
		for g := range groupDeviceIPs {
			sort.Strings(groupDeviceIPs[g])
		}
	}

	// Accumulate allows per node, de-duplicated.
	type nodeAcc struct {
		set  map[policyspec.AllowEntry]bool
		list []policyspec.AllowEntry
	}
	acc := map[uuid.UUID]*nodeAcc{}
	for nodeID := range nodeSet {
		acc[nodeID] = &nodeAcc{set: map[policyspec.AllowEntry]bool{}}
	}
	add := func(nodeID uuid.UUID, e policyspec.AllowEntry) {
		a := acc[nodeID]
		if a.set[e] {
			return
		}
		a.set[e] = true
		a.list = append(a.list, e)
	}

	for _, d := range s.Devices { // d is the SOURCE device
		if d.AssignedIP == "" {
			continue
		}
		owner := userGroups[d.UserID]
		if len(owner) == 0 {
			continue // owner in no groups => no grants (default-deny)
		}
		for _, r := range s.Rules {
			if !owner[r.SrcGroupID] {
				continue
			}
			switch r.DstKind {
			case "resource":
				res, ok := resourceByID[r.DstResourceID]
				if !ok || res.CIDR == "" {
					continue
				}
				add(d.NodeID, policyspec.AllowEntry{
					SrcIP:    d.AssignedIP,
					DstCIDR:  res.CIDR,
					Protocol: normProto(res.Protocol),
					PortLow:  res.PortLow,
					PortHigh: res.PortHigh,
				})
			case "group":
				for _, dstIP := range groupDeviceIPs[r.DstGroupID] {
					if dstIP == d.AssignedIP {
						continue // a device reaching itself is meaningless
					}
					add(d.NodeID, policyspec.AllowEntry{
						SrcIP:    d.AssignedIP,
						DstCIDR:  dstIP + "/32",
						Protocol: policyspec.ProtoAny, // device-to-device is L3
					})
				}
			}
		}
	}

	for nodeID := range nodeSet {
		list := acc[nodeID].list
		sortAllows(list)
		out[nodeID] = policyspec.Compiled{
			Version: policyspec.ProtocolVersion,
			NodeID:  nodeID.String(),
			Mode:    ModeEnforcing,
			Mesh:    false,
			Allow:   list, // may be nil/empty => deny-all (empty != permissive)
		}
	}
	return out
}

// normProto maps a stored protocol to the wire enum, defaulting unknown/empty to
// "any" (fail-open on the L4 scope is fine — the L3 grant itself is the gate).
func normProto(p string) policyspec.Protocol {
	switch p {
	case "tcp":
		return policyspec.ProtoTCP
	case "udp":
		return policyspec.ProtoUDP
	default:
		return policyspec.ProtoAny
	}
}

// sortAllows imposes a total order so output is byte-identical for equal input.
func sortAllows(a []policyspec.AllowEntry) {
	sort.Slice(a, func(i, j int) bool {
		if a[i].SrcIP != a[j].SrcIP {
			return a[i].SrcIP < a[j].SrcIP
		}
		if a[i].DstCIDR != a[j].DstCIDR {
			return a[i].DstCIDR < a[j].DstCIDR
		}
		if a[i].Protocol != a[j].Protocol {
			return a[i].Protocol < a[j].Protocol
		}
		if a[i].PortLow != a[j].PortLow {
			return a[i].PortLow < a[j].PortLow
		}
		return a[i].PortHigh < a[j].PortHigh
	})
}
