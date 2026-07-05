// Package ipalloc is the org tunnel-address allocator: deterministic, collision-
// free assignment from an org's flat pool CIDR. It is pure (CIDR + used-set in,
// address out) so every acceptance property — reserved addresses, exhaustion at
// exactly capacity, release/reuse, CIDR-resize safety, determinism — is unit
// testable. The DB's UNIQUE(org_id, assigned_ip) is the concurrency backstop;
// this package decides WHICH address, the index guarantees no two ever collide.
package ipalloc

import (
	"errors"
	"fmt"
	"net/netip"
)

var (
	// ErrPoolExhausted means every allocatable host in the pool is taken.
	ErrPoolExhausted = errors.New("ip pool exhausted")
	// ErrBadCIDR means the pool CIDR is not a valid IPv4 prefix.
	ErrBadCIDR = errors.New("invalid pool CIDR")
	// ErrPoolTooSmall means the CIDR has no allocatable host after reservations.
	ErrPoolTooSmall = errors.New("pool CIDR too small")
)

// Two addresses are reserved at the low end (network, gateway) and one at the top
// (broadcast), structurally — derived from the CIDR, never from loop bounds.
const reservedLow = 2 // .0 network, .1 gateway (the node interface)

// Allocate returns the lowest free host in the pool, deterministically (same
// pool + same used-set → same address), skipping the reserved network/gateway/
// broadcast addresses. used addresses outside the pool are ignored.
func Allocate(cidr string, used []string) (string, error) {
	p, err := prefix(cidr)
	if err != nil {
		return "", err
	}
	network := toU32(p.Addr())
	hostBits := 32 - p.Bits()
	if hostBits < 2 { // need network + gateway + >=1 host + broadcast
		return "", ErrPoolTooSmall
	}
	broadcast := network + (uint32(1) << hostBits) - 1
	first := network + reservedLow // skip .0 network and .1 gateway
	last := broadcast - 1          // skip broadcast

	taken := make(map[uint32]bool, len(used))
	for _, s := range used {
		if a, err := netip.ParseAddr(s); err == nil && p.Contains(a) {
			taken[toU32(a)] = true
		}
	}
	for u := first; u <= last; u++ {
		if !taken[u] {
			return fromU32(u).String(), nil
		}
	}
	return "", ErrPoolExhausted
}

// GatewayCIDR returns the gateway (node interface) address with the pool's prefix
// length, e.g. "10.99.0.1/24" — the first usable host in the pool.
func GatewayCIDR(cidr string) (string, error) {
	p, err := prefix(cidr)
	if err != nil {
		return "", err
	}
	if 32-p.Bits() < 1 {
		return "", ErrPoolTooSmall
	}
	gw := fromU32(toU32(p.Addr()) + 1)
	return fmt.Sprintf("%s/%d", gw, p.Bits()), nil
}

// OutOfRange returns the allocated addresses that fall OUTSIDE newCIDR — the
// resize-shrink safety check. A non-empty result means the shrink would orphan
// live allocations and must be refused. Unparseable inputs count as offenders.
func OutOfRange(newCIDR string, allocated []string) ([]string, error) {
	p, err := prefix(newCIDR)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range allocated {
		a, perr := netip.ParseAddr(s)
		if perr != nil || !p.Contains(a) {
			out = append(out, s)
		}
	}
	return out, nil
}

// prefix parses cidr into a canonical (masked) IPv4 prefix.
func prefix(cidr string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, ErrBadCIDR
	}
	return p.Masked(), nil
}

func toU32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func fromU32(u uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(u >> 24), byte(u >> 16), byte(u >> 8), byte(u)})
}
