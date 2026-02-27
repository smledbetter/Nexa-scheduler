// Package metrics defines Prometheus metrics for the Nexa scheduler.
// All metrics use the "nexa_" prefix and follow Prometheus naming conventions.
// Register must be called once at scheduler startup before any metrics are recorded.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metric collectors. These are nil until Register is called.
var (
	// SchedulingDuration tracks the total time from PreFilter to PostBind/PostFilter.
	// Labels: result (scheduled, failed).
	SchedulingDuration *prometheus.HistogramVec

	// FilterResults counts filter outcomes per plugin.
	// Labels: plugin (NexaRegion, NexaPrivacy), result (accepted, rejected, error).
	FilterResults *prometheus.CounterVec

	// ScoreDistribution tracks the distribution of scores assigned by each plugin.
	// Labels: plugin (NexaRegion, NexaPrivacy).
	ScoreDistribution *prometheus.HistogramVec

	// IsolationViolations counts privacy/isolation filter rejections by reason.
	// Labels: reason (node_not_wiped, cross_org, strict_org).
	IsolationViolations *prometheus.CounterVec

	// PolicyEvaluations counts policy provider calls per plugin.
	// Labels: plugin (NexaRegion, NexaPrivacy), result (success, error).
	PolicyEvaluations *prometheus.CounterVec
)

// Register creates and registers all Nexa metrics with the given registerer.
// Must be called once before any metrics are recorded.
// Tests should pass a fresh prometheus.NewRegistry() for isolation.
func Register(reg prometheus.Registerer) {
	SchedulingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexa_scheduling_duration_seconds",
		Help:    "Total scheduling cycle duration from PreFilter to PostBind/PostFilter.",
		Buckets: prometheus.DefBuckets,
	}, []string{"result"})

	FilterResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nexa_filter_results_total",
		Help: "Number of filter evaluations by plugin and result.",
	}, []string{"plugin", "result"})

	ScoreDistribution = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexa_score_distribution",
		Help:    "Distribution of scores assigned by each scoring plugin.",
		Buckets: []float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
	}, []string{"plugin"})

	IsolationViolations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nexa_isolation_violations_total",
		Help: "Number of isolation violations detected during filtering.",
	}, []string{"reason"})

	PolicyEvaluations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nexa_policy_evaluations_total",
		Help: "Number of policy evaluations by plugin and result.",
	}, []string{"plugin", "result"})

	reg.MustRegister(
		SchedulingDuration,
		FilterResults,
		ScoreDistribution,
		IsolationViolations,
		PolicyEvaluations,
	)
}
