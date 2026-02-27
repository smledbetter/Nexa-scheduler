package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/nexascheduler/nexa/pkg/metrics"
	"github.com/nexascheduler/nexa/pkg/policy"
	nt "github.com/nexascheduler/nexa/pkg/testing"
)

// Compile-time interface compliance checks.
var _ framework.PreFilterPlugin = (*Plugin)(nil)
var _ framework.PostBindPlugin = (*Plugin)(nil)
var _ framework.PostFilterPlugin = (*Plugin)(nil)

// --- test helpers ---

// mockHandle implements the subset of framework.Handle needed by PostFilter.
type mockHandle struct {
	framework.Handle
	lister *mockNodeInfoLister
}

func (m *mockHandle) SnapshotSharedLister() framework.SharedLister {
	return &mockSharedLister{lister: m.lister}
}

type mockSharedLister struct {
	framework.SharedLister
	lister *mockNodeInfoLister
}

func (m *mockSharedLister) NodeInfos() framework.NodeInfoLister {
	return m.lister
}

// mockNodeInfoLister returns pre-configured NodeInfos.
type mockNodeInfoLister struct {
	framework.NodeInfoLister
	nodes []fwk.NodeInfo
}

func (m *mockNodeInfoLister) List() ([]fwk.NodeInfo, error) {
	return m.nodes, nil
}

func (m *mockNodeInfoLister) Get(name string) (fwk.NodeInfo, error) {
	for _, n := range m.nodes {
		if n.Node().Name == name {
			return n, nil
		}
	}
	return nil, errors.New("node not found")
}

// enabledPolicy returns a StaticProvider with both region and privacy enabled.
func enabledPolicy() policy.Provider {
	return &policy.StaticProvider{P: &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}}
}

// disabledPolicy returns a StaticProvider with both plugins disabled.
func disabledPolicy() policy.Provider {
	return &policy.StaticProvider{P: &policy.Policy{}}
}

// errorPolicy returns a StaticProvider that always fails.
func errorPolicy() policy.Provider {
	return &policy.StaticProvider{Err: errors.New("policy unavailable")}
}

// newTestPlugin creates a Plugin with captured log output.
func newTestPlugin(provider policy.Provider, handle framework.Handle, debug bool) (*Plugin, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, debug)
	return NewWithLogger(handle, provider, logger), buf
}

// parseEntries decodes JSON lines from the buffer into DecisionEntry slices.
func parseEntries(t *testing.T, buf *bytes.Buffer) []DecisionEntry {
	t.Helper()
	var entries []DecisionEntry
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var e DecisionEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("failed to parse JSON line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

// makeNodeInfoList creates NodeInfo objects and a mockHandle for PostFilter tests.
func makeNodeInfoList(nodes ...*v1.Node) (*mockHandle, []fwk.NodeInfo) {
	var infos []fwk.NodeInfo
	for _, n := range nodes {
		infos = append(infos, nt.MakeNodeInfo(n))
	}
	return &mockHandle{lister: &mockNodeInfoLister{nodes: infos}}, infos
}

// --- PostBind tests ---

func TestName(t *testing.T) {
	p := &Plugin{}
	if got := p.Name(); got != Name {
		t.Errorf("Name() = %q, want %q", got, Name)
	}
}

func TestPostBind(t *testing.T) {
	// Fix time for deterministic output.
	origNow := now
	now = func() time.Time { return time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC) }
	defer func() { now = origNow }()

	tests := []struct {
		name      string
		pod       *v1.Pod
		nodeName  string
		provider  policy.Provider
		wantEvent string
		wantNode  string
		wantPod   PodRef
		wantPol   PolicySnapshot
	}{
		{
			name:      "high privacy pod with all labels",
			pod:       nt.MakePod("secure-job", map[string]string{"nexa.io/privacy": "high", "nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a", "nexa.io/org": "acme"}),
			nodeName:  "node-1",
			provider:  enabledPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-1",
			wantPod:   PodRef{Name: "secure-job", Privacy: "high", Region: "us-west1", Zone: "us-west1-a", Org: "acme"},
			wantPol:   PolicySnapshot{RegionEnabled: true, PrivacyEnabled: true},
		},
		{
			name:      "pod with no scheduling labels",
			pod:       nt.MakePod("plain-job", nil),
			nodeName:  "node-2",
			provider:  enabledPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-2",
			wantPod:   PodRef{Name: "plain-job"},
			wantPol:   PolicySnapshot{RegionEnabled: true, PrivacyEnabled: true},
		},
		{
			name:      "policies disabled",
			pod:       nt.MakePod("job-x", map[string]string{"nexa.io/region": "eu-west1"}),
			nodeName:  "node-3",
			provider:  disabledPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-3",
			wantPod:   PodRef{Name: "job-x", Region: "eu-west1"},
			wantPol:   PolicySnapshot{},
		},
		{
			name:      "policy error degrades gracefully",
			pod:       nt.MakePod("job-y", map[string]string{"nexa.io/privacy": "high"}),
			nodeName:  "node-4",
			provider:  errorPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-4",
			wantPod:   PodRef{Name: "job-y", Privacy: "high"},
			wantPol:   PolicySnapshot{},
		},
		{
			name:      "pod with namespace",
			pod:       makePodWithNamespace("ns-job", "production", map[string]string{"nexa.io/org": "corp"}),
			nodeName:  "node-5",
			provider:  enabledPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-5",
			wantPod:   PodRef{Name: "ns-job", Namespace: "production", Org: "corp"},
			wantPol:   PolicySnapshot{RegionEnabled: true, PrivacyEnabled: true},
		},
		{
			name:      "extremely long label values",
			pod:       nt.MakePod("long-labels", map[string]string{"nexa.io/region": strings.Repeat("a", 1000), "nexa.io/org": strings.Repeat("b", 500)}),
			nodeName:  "node-6",
			provider:  enabledPolicy(),
			wantEvent: "scheduled",
			wantNode:  "node-6",
			wantPod:   PodRef{Name: "long-labels", Region: strings.Repeat("a", 1000), Org: strings.Repeat("b", 500)},
			wantPol:   PolicySnapshot{RegionEnabled: true, PrivacyEnabled: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, buf := newTestPlugin(tt.provider, nil, false)
			p.PostBind(context.Background(), nil, tt.pod, tt.nodeName)

			entries := parseEntries(t, buf)
			if len(entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(entries))
			}
			e := entries[0]

			if e.Level != "INFO" {
				t.Errorf("Level = %q, want INFO", e.Level)
			}
			if e.Event != "scheduled" {
				t.Errorf("Event = %q, want scheduled", e.Event)
			}
			if e.Node != tt.wantNode {
				t.Errorf("Node = %q, want %q", e.Node, tt.wantNode)
			}
			if e.Pod != tt.wantPod {
				t.Errorf("Pod = %+v, want %+v", e.Pod, tt.wantPod)
			}
			if e.Policy != tt.wantPol {
				t.Errorf("Policy = %+v, want %+v", e.Policy, tt.wantPol)
			}
			if e.Timestamp != "2026-02-27T12:00:00Z" {
				t.Errorf("Timestamp = %q, want 2026-02-27T12:00:00Z", e.Timestamp)
			}
		})
	}
}

// TestPostBindNoSensitiveData verifies that audit logs contain only scheduling
// metadata and never include env vars, secrets, or service account tokens.
func TestPostBindNoSensitiveData(t *testing.T) {
	pod := &v1.Pod{}
	pod.Name = "secret-job"
	pod.Namespace = "secure-ns"
	pod.Labels = map[string]string{"nexa.io/privacy": "high"}
	pod.Spec.Containers = []v1.Container{
		{
			Name: "worker",
			Env: []v1.EnvVar{
				{Name: "API_KEY", Value: "super-secret-key-12345"},
				{Name: "DB_PASSWORD", Value: "p@ssw0rd!"},
			},
		},
	}
	pod.Spec.ServiceAccountName = "sensitive-sa"

	p, buf := newTestPlugin(enabledPolicy(), nil, false)
	p.PostBind(context.Background(), nil, pod, "node-1")

	output := buf.String()
	if strings.Contains(output, "super-secret-key-12345") {
		t.Error("audit log contains API_KEY secret value")
	}
	if strings.Contains(output, "p@ssw0rd!") {
		t.Error("audit log contains DB_PASSWORD secret value")
	}
	if strings.Contains(output, "sensitive-sa") {
		t.Error("audit log contains service account name")
	}
}

// --- PostFilter tests ---

func TestPostFilter(t *testing.T) {
	origNow := now
	now = func() time.Time { return time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC) }
	defer func() { now = origNow }()

	tests := []struct {
		name         string
		pod          *v1.Pod
		provider     policy.Provider
		nodes        []*v1.Node
		statuses     map[string]*fwk.Status
		wantFilters  int
		wantEvent    string
		wantDebug    bool
		debugEntries int
	}{
		{
			name:     "single node rejected",
			pod:      nt.MakePod("fail-job", map[string]string{"nexa.io/privacy": "high"}),
			provider: enabledPolicy(),
			nodes:    []*v1.Node{nt.MakeNode("node-a", nil)},
			statuses: map[string]*fwk.Status{
				"node-a": fwk.NewStatus(fwk.Unschedulable, "node not wiped"),
			},
			wantFilters: 1,
			wantEvent:   "scheduling_failed",
		},
		{
			name:     "multiple nodes rejected",
			pod:      nt.MakePod("multi-fail", map[string]string{"nexa.io/region": "eu-west1"}),
			provider: enabledPolicy(),
			nodes: []*v1.Node{
				nt.MakeNode("node-a", nil),
				nt.MakeNode("node-b", nil),
				nt.MakeNode("node-c", nil),
			},
			statuses: map[string]*fwk.Status{
				"node-a": fwk.NewStatus(fwk.Unschedulable, "region mismatch"),
				"node-b": fwk.NewStatus(fwk.Unschedulable, "region mismatch"),
				"node-c": fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "node tainted"),
			},
			wantFilters: 3,
			wantEvent:   "scheduling_failed",
		},
		{
			name:         "debug mode emits filter details",
			pod:          nt.MakePod("debug-job", nil),
			provider:     enabledPolicy(),
			nodes:        []*v1.Node{nt.MakeNode("node-x", nil)},
			statuses:     map[string]*fwk.Status{"node-x": fwk.NewStatus(fwk.Unschedulable, "reason x")},
			wantFilters:  1,
			wantEvent:    "scheduling_failed",
			wantDebug:    true,
			debugEntries: 1,
		},
		{
			name:        "policy error degrades gracefully in PostFilter",
			pod:         nt.MakePod("error-job", nil),
			provider:    errorPolicy(),
			nodes:       []*v1.Node{nt.MakeNode("node-e", nil)},
			statuses:    map[string]*fwk.Status{"node-e": fwk.NewStatus(fwk.Unschedulable, "filtered")},
			wantFilters: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle, _ := makeNodeInfoList(tt.nodes...)
			debug := tt.wantDebug
			p, buf := newTestPlugin(tt.provider, handle, debug)

			statusMap := framework.NewNodeToStatus(tt.statuses, fwk.NewStatus(fwk.UnschedulableAndUnresolvable))
			result, status := p.PostFilter(context.Background(), nil, tt.pod, statusMap)

			// PostFilter is informational — returns nil result and Unschedulable.
			if result != nil {
				t.Error("expected nil PostFilterResult")
			}
			if status.Code() != fwk.Unschedulable {
				t.Errorf("status code = %v, want Unschedulable", status.Code())
			}

			entries := parseEntries(t, buf)
			expectedCount := 1
			if debug && tt.debugEntries > 0 {
				expectedCount += tt.debugEntries
			}
			if len(entries) != expectedCount {
				t.Fatalf("expected %d entries, got %d: %s", expectedCount, len(entries), buf.String())
			}

			// Check INFO entry.
			info := entries[0]
			if info.Level != "INFO" {
				t.Errorf("Level = %q, want INFO", info.Level)
			}
			if info.Event != "scheduling_failed" {
				t.Errorf("Event = %q, want scheduling_failed", info.Event)
			}
			if len(info.Filters) != tt.wantFilters {
				t.Errorf("filter count = %d, want %d", len(info.Filters), tt.wantFilters)
			}

			// Check DEBUG entry if expected.
			if debug && tt.debugEntries > 0 {
				dbg := entries[1]
				if dbg.Level != "DEBUG" {
					t.Errorf("debug Level = %q, want DEBUG", dbg.Level)
				}
				if dbg.Event != "filter_details" {
					t.Errorf("debug Event = %q, want filter_details", dbg.Event)
				}
			}
		})
	}
}

// TestPostFilterDebugOff verifies that no DEBUG entries appear when debug is false.
func TestPostFilterDebugOff(t *testing.T) {
	handle, _ := makeNodeInfoList(nt.MakeNode("node-1", nil))
	p, buf := newTestPlugin(enabledPolicy(), handle, false)

	statusMap := framework.NewNodeToStatus(
		map[string]*fwk.Status{"node-1": fwk.NewStatus(fwk.Unschedulable, "rejected")},
		fwk.NewStatus(fwk.UnschedulableAndUnresolvable),
	)
	p.PostFilter(context.Background(), nil, nt.MakePod("pod-1", nil), statusMap)

	entries := parseEntries(t, buf)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (INFO only), got %d", len(entries))
	}
	if entries[0].Level != "INFO" {
		t.Errorf("Level = %q, want INFO", entries[0].Level)
	}
}

// --- Logger tests ---

func TestLoggerJSONFormat(t *testing.T) {
	origNow := now
	now = func() time.Time { return time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC) }
	defer func() { now = origNow }()

	buf := &bytes.Buffer{}
	l := NewLogger(buf, false)
	l.LogDecision(DecisionEntry{
		Event: "scheduled",
		Pod:   PodRef{Name: "test-pod", Namespace: "default"},
		Node:  "node-1",
	})

	var raw map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	// Verify required fields exist.
	for _, key := range []string{"timestamp", "level", "event", "pod", "node", "policy"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing required field %q in JSON output", key)
		}
	}
}

func TestLogFilterDetailSkippedWhenDebugOff(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewLogger(buf, false)
	l.LogFilterDetail(DecisionEntry{Event: "filter_details"})

	if buf.Len() != 0 {
		t.Errorf("expected no output when debug=false, got %q", buf.String())
	}
}

func TestLogFilterDetailEmittedWhenDebugOn(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewLogger(buf, true)
	l.LogFilterDetail(DecisionEntry{Event: "filter_details"})

	if buf.Len() == 0 {
		t.Error("expected output when debug=true, got nothing")
	}

	var entry DecisionEntry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if entry.Level != "DEBUG" {
		t.Errorf("Level = %q, want DEBUG", entry.Level)
	}
}

// --- PreFilter and timing tests ---

func TestPreFilter(t *testing.T) {
	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	state := framework.NewCycleState()

	result, status := p.PreFilter(context.Background(), state, nt.MakePod("test", nil), nil)
	if result != nil {
		t.Error("PreFilter should return nil result (no node filtering)")
	}
	if status != nil {
		t.Errorf("PreFilter should return nil status, got %v", status)
	}

	// Verify start time was written to CycleState.
	data, err := state.Read(startTimeKey)
	if err != nil {
		t.Fatalf("CycleState missing start time: %v", err)
	}
	if _, ok := data.(*startTimeData); !ok {
		t.Errorf("CycleState data is %T, want *startTimeData", data)
	}
}

func TestPreFilterExtensions(t *testing.T) {
	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	if ext := p.PreFilterExtensions(); ext != nil {
		t.Error("PreFilterExtensions should return nil")
	}
}

func TestPostBindRecordsDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics.Register(reg)

	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	state := framework.NewCycleState()

	// Simulate PreFilter writing start time.
	state.Write(startTimeKey, &startTimeData{t: time.Now().Add(-10 * time.Millisecond)})

	p.PostBind(context.Background(), state, nt.MakePod("timed-pod", nil), "node-1")

	// Verify the histogram recorded an observation.
	m := &dto.Metric{}
	observer := metrics.SchedulingDuration.WithLabelValues("scheduled")
	if h, ok := observer.(prometheus.Metric); ok {
		if err := h.Write(m); err != nil {
			t.Fatalf("failed to read histogram: %v", err)
		}
	}
	if m.GetHistogram().GetSampleCount() != 1 {
		t.Errorf("expected 1 duration observation, got %d", m.GetHistogram().GetSampleCount())
	}
}

func TestPostFilterRecordsDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics.Register(reg)

	handle, _ := makeNodeInfoList(nt.MakeNode("node-f", nil))
	p, _ := newTestPlugin(enabledPolicy(), handle, false)
	state := framework.NewCycleState()
	state.Write(startTimeKey, &startTimeData{t: time.Now().Add(-5 * time.Millisecond)})

	statusMap := framework.NewNodeToStatus(
		map[string]*fwk.Status{"node-f": fwk.NewStatus(fwk.Unschedulable, "rejected")},
		fwk.NewStatus(fwk.UnschedulableAndUnresolvable),
	)
	p.PostFilter(context.Background(), state, nt.MakePod("fail-timed", nil), statusMap)

	m := &dto.Metric{}
	observer := metrics.SchedulingDuration.WithLabelValues("failed")
	if h, ok := observer.(prometheus.Metric); ok {
		if err := h.Write(m); err != nil {
			t.Fatalf("failed to read histogram: %v", err)
		}
	}
	if m.GetHistogram().GetSampleCount() != 1 {
		t.Errorf("expected 1 duration observation, got %d", m.GetHistogram().GetSampleCount())
	}
}

func TestObserveDurationNilState(t *testing.T) {
	// Should not panic when CycleState is nil.
	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	p.observeDuration(nil, "scheduled")
}

func TestObserveDurationNoMetrics(t *testing.T) {
	// Should not panic when metrics are not registered (nil collectors).
	origDuration := metrics.SchedulingDuration
	metrics.SchedulingDuration = nil
	defer func() { metrics.SchedulingDuration = origDuration }()

	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	state := framework.NewCycleState()
	state.Write(startTimeKey, &startTimeData{t: time.Now()})
	p.observeDuration(state, "scheduled")
}

func TestObserveDurationMissingKey(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics.Register(reg)

	// CycleState without start time — should silently skip.
	p, _ := newTestPlugin(enabledPolicy(), nil, false)
	state := framework.NewCycleState()
	p.observeDuration(state, "scheduled")

	m := &dto.Metric{}
	observer := metrics.SchedulingDuration.WithLabelValues("scheduled")
	if h, ok := observer.(prometheus.Metric); ok {
		if err := h.Write(m); err != nil {
			t.Fatalf("failed to read histogram: %v", err)
		}
	}
	if m.GetHistogram().GetSampleCount() != 0 {
		t.Errorf("expected 0 observations when key missing, got %d", m.GetHistogram().GetSampleCount())
	}
}

// --- helpers ---

func makePodWithNamespace(name, namespace string, labels map[string]string) *v1.Pod {
	pod := nt.MakePod(name, labels)
	pod.Namespace = namespace
	return pod
}
