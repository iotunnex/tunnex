// Package metrics exposes the control plane's Prometheus surface (S11 D3.1–D3.3).
//
// ONE TRUTH, TWO RENDERINGS. The fleet-health metric is DERIVED from the health kinds the product already
// ships (nodes.AllKinds(), itself derived from the transitionTable that drives the dashboard) — never a
// parallel vocabulary invented for monitoring. The dashboard and the metric answer the same question from
// the same source; only the rendering differs.
//
// CARDINALITY (D3.3): fleet-level counts by KIND ONLY — no org or node labels. Per-org/per-node series on a
// shared Prometheus is unbounded cardinality, which is how monitoring stacks fall over, and that detail
// already lives in the API + dashboard. The honest limit, stated so nobody mis-reads the metric: it answers
// "HOW MANY gateways are apply_failing", never "WHICH ones" — the dashboard answers which.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// FleetHealthFunc returns the CURRENT count of gateways per health kind. It is called on every scrape, so it
// must be cheap and must never block the scrape indefinitely (the caller bounds it with a context).
// A nil/empty map is valid — it means "no gateways", and every kind still reports 0.
type FleetHealthFunc func() map[nodes.PolicyDegradedKind]int

// Collector reports fleet health. It implements prometheus.Collector so the counts are read at scrape time
// rather than cached behind a ticker — no staleness window, and no extra scheduler to leader-gate (D4).
type Collector struct {
	health FleetHealthFunc
	desc   *prometheus.Desc
}

// NewCollector builds the fleet collector. health may be nil (then every kind reports 0 — an honest
// "nothing known" rather than a missing series).
func NewCollector(health FleetHealthFunc) *Collector {
	return &Collector{
		health: health,
		desc: prometheus.NewDesc(
			"tunnex_gateway_policy_health",
			"Number of gateways in each policy-health kind. Kinds are the product's own health vocabulary "+
				"(one truth with the dashboard). Fleet-wide: this answers how many, not which.",
			[]string{"kind"}, nil,
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

// Collect emits ONE SERIES PER KIND, ranging over nodes.AllKinds() — so a kind with zero gateways reports 0
// rather than vanishing. That matters twice over: an absent series is indistinguishable from a scrape
// failure, and (D3.1) ranging over the enum means a 14th kind cannot be a series that silently never
// appears. TestEveryHealthKindIsEnumerated + TestCollectorEmitsEveryKind hold both halves.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	var counts map[nodes.PolicyDegradedKind]int
	if c.health != nil {
		counts = c.health()
	}
	for _, kind := range nodes.AllKinds() {
		ch <- prometheus.MustNewConstMetric(
			c.desc, prometheus.GaugeValue, float64(counts[kind]), string(kind),
		)
	}
}

// NewRegistry builds a registry carrying the fleet collector plus the Go/process collectors (heap, GC,
// goroutines, fds, CPU) — the baseline an operator needs to answer "is the control plane itself healthy",
// which is half of what this endpoint exists for. A dedicated registry (not the global default) keeps the
// exposed set explicit and testable.
func NewRegistry(health FleetHealthFunc) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		NewCollector(health),
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	return reg
}
