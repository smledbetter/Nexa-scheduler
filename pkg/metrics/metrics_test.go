package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// freshRegistry creates and registers metrics with an isolated registry.
// Each test gets its own registry to prevent counter pollution (H7 baseline #2).
func freshRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	Register(reg)
	return reg
}

// counterValue reads the current value of a counter with the given labels.
func counterValue(t *testing.T, counter *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := counter.WithLabelValues(labels...).Write(m); err != nil {
		t.Fatalf("failed to read counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

// histogramCount reads the sample count from a histogram with the given labels.
func histogramCount(t *testing.T, hist *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	m := &dto.Metric{}
	observer := hist.WithLabelValues(labels...)
	if h, ok := observer.(prometheus.Metric); ok {
		if err := h.Write(m); err != nil {
			t.Fatalf("failed to read histogram: %v", err)
		}
	}
	return m.GetHistogram().GetSampleCount()
}

func TestRegisterCreatesAllMetrics(t *testing.T) {
	_ = freshRegistry(t)

	if SchedulingDuration == nil {
		t.Error("SchedulingDuration is nil after Register")
	}
	if FilterResults == nil {
		t.Error("FilterResults is nil after Register")
	}
	if ScoreDistribution == nil {
		t.Error("ScoreDistribution is nil after Register")
	}
	if IsolationViolations == nil {
		t.Error("IsolationViolations is nil after Register")
	}
	if PolicyEvaluations == nil {
		t.Error("PolicyEvaluations is nil after Register")
	}
}

func TestFilterResultsCounter(t *testing.T) {
	_ = freshRegistry(t)

	tests := []struct {
		name   string
		plugin string
		result string
		count  int
	}{
		{name: "region accepted", plugin: "NexaRegion", result: "accepted", count: 3},
		{name: "region rejected", plugin: "NexaRegion", result: "rejected", count: 1},
		{name: "privacy accepted", plugin: "NexaPrivacy", result: "accepted", count: 2},
		{name: "privacy error", plugin: "NexaPrivacy", result: "error", count: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range tt.count {
				FilterResults.WithLabelValues(tt.plugin, tt.result).Inc()
			}
			got := counterValue(t, FilterResults, tt.plugin, tt.result)
			if got != float64(tt.count) {
				t.Errorf("FilterResults(%s, %s) = %v, want %v", tt.plugin, tt.result, got, tt.count)
			}
		})
	}
}

func TestIsolationViolationsCounter(t *testing.T) {
	_ = freshRegistry(t)

	tests := []struct {
		name   string
		reason string
	}{
		{name: "node not wiped", reason: "node_not_wiped"},
		{name: "cross org", reason: "cross_org"},
		{name: "strict org", reason: "strict_org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			IsolationViolations.WithLabelValues(tt.reason).Inc()
			got := counterValue(t, IsolationViolations, tt.reason)
			if got < 1 {
				t.Errorf("IsolationViolations(%s) = %v, want >= 1", tt.reason, got)
			}
		})
	}
}

func TestPolicyEvaluationsCounter(t *testing.T) {
	_ = freshRegistry(t)

	// Success path.
	PolicyEvaluations.WithLabelValues("NexaRegion", "success").Inc()
	PolicyEvaluations.WithLabelValues("NexaRegion", "success").Inc()
	got := counterValue(t, PolicyEvaluations, "NexaRegion", "success")
	if got != 2 {
		t.Errorf("PolicyEvaluations(NexaRegion, success) = %v, want 2", got)
	}

	// Error path.
	PolicyEvaluations.WithLabelValues("NexaPrivacy", "error").Inc()
	got = counterValue(t, PolicyEvaluations, "NexaPrivacy", "error")
	if got != 1 {
		t.Errorf("PolicyEvaluations(NexaPrivacy, error) = %v, want 1", got)
	}
}

func TestSchedulingDurationHistogram(t *testing.T) {
	_ = freshRegistry(t)

	// Successful scheduling observation.
	SchedulingDuration.WithLabelValues("scheduled").Observe(0.05)
	SchedulingDuration.WithLabelValues("scheduled").Observe(0.10)
	count := histogramCount(t, SchedulingDuration, "scheduled")
	if count != 2 {
		t.Errorf("SchedulingDuration(scheduled) count = %d, want 2", count)
	}

	// Failed scheduling observation (zero duration edge case).
	SchedulingDuration.WithLabelValues("failed").Observe(0)
	count = histogramCount(t, SchedulingDuration, "failed")
	if count != 1 {
		t.Errorf("SchedulingDuration(failed) count = %d, want 1", count)
	}
}

func TestScoreDistributionHistogram(t *testing.T) {
	_ = freshRegistry(t)

	ScoreDistribution.WithLabelValues("NexaRegion").Observe(100)
	ScoreDistribution.WithLabelValues("NexaRegion").Observe(50)
	ScoreDistribution.WithLabelValues("NexaRegion").Observe(0)

	count := histogramCount(t, ScoreDistribution, "NexaRegion")
	if count != 3 {
		t.Errorf("ScoreDistribution(NexaRegion) count = %d, want 3", count)
	}
}

func TestRegistryIsolation(t *testing.T) {
	// Register with two separate registries to verify no cross-pollution.
	reg1 := prometheus.NewRegistry()
	Register(reg1)
	FilterResults.WithLabelValues("NexaRegion", "accepted").Inc()
	val1 := counterValue(t, FilterResults, "NexaRegion", "accepted")

	// Re-register with a fresh registry — counter should reset.
	reg2 := prometheus.NewRegistry()
	Register(reg2)
	val2 := counterValue(t, FilterResults, "NexaRegion", "accepted")

	if val1 != 1 {
		t.Errorf("registry 1 counter = %v, want 1", val1)
	}
	if val2 != 0 {
		t.Errorf("registry 2 counter = %v, want 0 (should be fresh)", val2)
	}
}
