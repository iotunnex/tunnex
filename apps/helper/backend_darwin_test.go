//go:build darwin

package helper

import (
	"reflect"
	"testing"
)

// TestRouteTargets pins the RC2 split-default mapping: a full-tunnel default is
// installed as the WG-standard half-route PAIR (more specific than the physical
// default, so it takes precedence WITHOUT destroying it), while any non-default
// destination passes through unchanged. This is what makes teardown/crash recover
// the physical default automatically instead of stranding the host.
func TestRouteTargets(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"0.0.0.0/0", []string{"0.0.0.0/1", "128.0.0.0/1"}},
		{"::/0", []string{"::/1", "8000::/1"}},
		{"10.99.0.0/24", []string{"10.99.0.0/24"}}, // split-tunnel route untouched
		{"10.99.0.1/32", []string{"10.99.0.1/32"}},
	}
	for _, c := range cases {
		if got := routeTargets(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("routeTargets(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
