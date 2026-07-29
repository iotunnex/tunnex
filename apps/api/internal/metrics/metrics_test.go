package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// collect gathers the fleet metric's series as kind -> value.
func collect(t *testing.T, health FleetHealthFunc) map[string]float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(health))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "tunnex_gateway_policy_health" {
			continue
		}
		for _, m := range f.Metric {
			var kind string
			for _, l := range m.Label {
				if l.GetName() == "kind" {
					kind = l.GetValue()
				}
			}
			out[kind] = m.GetGauge().GetValue()
		}
	}
	return out
}

// TestCollectorEmitsEveryKind — D3.1's other half (the census red in package nodes holds the first).
//
// The metric ranges over nodes.AllKinds(), so EVERY kind emits a series even at zero. Two failures this
// prevents: an absent series is indistinguishable from a scrape failure (an operator alerting on
// "apply_failing > 0" learns nothing from a series that isn't there), and a kind with no metric path would
// be a producer with no consumer — invisible until someone asks why the graph is empty.
func TestCollectorEmitsEveryKind(t *testing.T) {
	got := collect(t, func() map[nodes.PolicyDegradedKind]int { return nil })

	all := nodes.AllKinds()
	if len(all) == 0 {
		t.Fatal("AllKinds() is empty — the metric would expose nothing")
	}
	for _, k := range all {
		v, ok := got[string(k)]
		if !ok {
			t.Fatalf("kind %q emits NO series — a kind with no metric path is invisible to monitoring", k)
		}
		if v != 0 {
			t.Fatalf("kind %q should report 0 when the fleet reports nothing, got %v", k, v)
		}
	}
	if len(got) != len(all) {
		t.Fatalf("series count %d != kind count %d — the metric invented or dropped a kind", len(got), len(all))
	}
}

// TestCollectorReportsCounts — the values are the fleet's, and unreported kinds still emit 0.
func TestCollectorReportsCounts(t *testing.T) {
	got := collect(t, func() map[nodes.PolicyDegradedKind]int {
		return map[nodes.PolicyDegradedKind]int{
			nodes.KindHealthy:      7,
			nodes.KindApplyFailing: 2,
		}
	})
	if got[string(nodes.KindHealthy)] != 7 || got[string(nodes.KindApplyFailing)] != 2 {
		t.Fatalf("counts not reported: healthy=%v apply_failing=%v", got[string(nodes.KindHealthy)], got[string(nodes.KindApplyFailing)])
	}
	if v, ok := got[string(nodes.KindSilentDesync)]; !ok || v != 0 {
		t.Fatalf("an unreported kind must still emit 0, got ok=%v v=%v", ok, v)
	}
}

// TestNoOrgOrNodeLabels — D3.3's cardinality ruling, enforced rather than documented.
//
// Fleet counts by kind ONLY. A well-meaning future change adding an org_id or node_id label would multiply
// the series count by the tenant/fleet size on a shared Prometheus — the failure mode that takes monitoring
// stacks down. Per-node detail is REGISTERED with a trigger (a customer running their own Prometheus who
// asks for it); until then the dashboard answers "which", and this metric answers "how many".
func TestNoOrgOrNodeLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(func() map[nodes.PolicyDegradedKind]int { return nil }))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"org", "org_id", "node", "node_id", "device", "device_id", "user", "user_id"}
	for _, f := range families {
		for _, m := range f.Metric {
			for _, l := range m.Label {
				name := strings.ToLower(l.GetName())
				for _, b := range banned {
					if name == b {
						t.Fatalf("metric %q carries label %q — unbounded cardinality (D3.3): fleet counts "+
							"by kind only; per-node detail belongs in the API/dashboard", f.GetName(), name)
					}
				}
			}
		}
	}
	_ = dto.MetricType_GAUGE // keep the dto import honest across client_golang versions
}

// TestWildcardBindDetection — the security-relevant half of D3.2. The default must be loopback, and a
// wildcard bind must be RECOGNISED (it is warned about, not silently accepted).
func TestWildcardBindDetection(t *testing.T) {
	if !strings.HasPrefix(DefaultAddr, "127.0.0.1:") {
		t.Fatalf("the metrics default MUST be loopback so a public endpoint is impossible by construction, got %q", DefaultAddr)
	}
	for _, addr := range []string{":9090", "0.0.0.0:9090", "[::]:9090"} {
		if !isWildcard(addr) {
			t.Fatalf("%q binds every interface but was not detected as a wildcard", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:9090", "10.0.0.4:9090", "[::1]:9090"} {
		if isWildcard(addr) {
			t.Fatalf("%q is a specific interface but was flagged as a wildcard", addr)
		}
	}
}
