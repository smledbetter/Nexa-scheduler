package privacy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/policy"
	nt "github.com/nexascheduler/nexa/pkg/testing"
)

// Compile-time interface compliance checks.
var _ framework.FilterPlugin = (*Plugin)(nil)
var _ framework.ScorePlugin = (*Plugin)(nil)

// enabledPolicy returns a StaticProvider with privacy enabled and no defaults.
func enabledPolicy() policy.Provider {
	return &policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{Enabled: true}}}
}

func TestName(t *testing.T) {
	p := &Plugin{}
	if got := p.Name(); got != Name {
		t.Errorf("Name() = %q, want %q", got, Name)
	}
}

func TestScoreExtensions(t *testing.T) {
	p := &Plugin{}
	if ext := p.ScoreExtensions(); ext != nil {
		t.Errorf("ScoreExtensions() = %v, want nil", ext)
	}
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name         string
		podLabels    map[string]string
		nodeLabels   map[string]string
		existingPods []*v1.Pod // pods already running on the node
		wantPass     bool
		wantReason   string
	}{
		{
			name:       "no privacy label — accept any node",
			podLabels:  nil,
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
		{
			name:       "standard privacy — accept any node",
			podLabels:  map[string]string{"nexa.io/privacy": "standard"},
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
		{
			name:       "high privacy + wiped node — accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			wantPass:   true,
		},
		{
			name:       "high privacy + not wiped — reject",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{},
			wantPass:   false,
			wantReason: "not wiped",
		},
		{
			name:       "high privacy + wiped=false — reject",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/wiped": "false"},
			wantPass:   false,
			wantReason: "not wiped",
		},
		{
			name:       "high privacy + wiped + same org history — accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true", "nexa.io/last-workload-org": "acme"},
			wantPass:   true,
		},
		{
			name:       "high privacy + wiped + no org history — accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			wantPass:   true,
		},
		{
			name:       "high privacy + wiped + different org history — reject",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true", "nexa.io/last-workload-org": "evil-corp"},
			wantPass:   false,
			wantReason: "does not match pod org",
		},
		{
			name:       "high privacy + wiped + no pod org — accept (no org constraint)",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true", "nexa.io/last-workload-org": "evil-corp"},
			wantPass:   true,
		},
		{
			name:       "high privacy + running pod from same org — accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			existingPods: []*v1.Pod{
				nt.MakePod("existing-pod", map[string]string{"nexa.io/org": "acme"}),
			},
			wantPass: true,
		},
		{
			name:       "high privacy + running pod from different org — reject",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			existingPods: []*v1.Pod{
				nt.MakePod("evil-pod", map[string]string{"nexa.io/org": "evil-corp"}),
			},
			wantPass:   false,
			wantReason: "org isolation",
		},
		{
			name:       "high privacy + running pod with no org label — accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			existingPods: []*v1.Pod{
				nt.MakePod("unlabeled-pod", nil),
			},
			wantPass: true,
		},
		{
			name:       "empty privacy label — treated as no preference",
			podLabels:  map[string]string{"nexa.io/privacy": ""},
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
		{
			name:       "actionable reason for wiped failure includes fix",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{},
			wantPass:   false,
			wantReason: "run node wipe procedure",
		},
	}

	p := NewWithProvider(enabledPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node, tt.existingPods...)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)

			if tt.wantPass {
				if status != nil && !status.IsSuccess() {
					t.Errorf("Filter() rejected node, want accept: %v", status.Message())
				}
			} else {
				if status == nil || status.IsSuccess() {
					t.Error("Filter() accepted node, want reject")
				} else if status.Code() != fwk.Unschedulable {
					t.Errorf("Filter() code = %v, want Unschedulable", status.Code())
				} else if tt.wantReason != "" && !strings.Contains(status.Message(), tt.wantReason) {
					t.Errorf("Filter() reason = %q, want substring %q", status.Message(), tt.wantReason)
				}
			}
		})
	}
}

func TestFilterDisabledPlugin(t *testing.T) {
	p := NewWithProvider(&policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{Enabled: false}}})
	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "high"})
	node := nt.MakeNode("test-node", map[string]string{})
	nodeInfo := nt.MakeNodeInfo(node)

	status := p.Filter(context.Background(), nil, pod, nodeInfo)
	if status != nil && !status.IsSuccess() {
		t.Errorf("disabled plugin should accept all nodes, got: %v", status.Message())
	}
}

func TestFilterDefaultPrivacy(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{
		Enabled:        true,
		DefaultPrivacy: "high",
	}}}
	p := NewWithProvider(provider)

	t.Run("unlabeled pod uses default privacy — rejects unwiped node", func(t *testing.T) {
		pod := nt.MakePod("test-pod", nil)
		node := nt.MakeNode("test-node", map[string]string{})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status == nil || status.IsSuccess() {
			t.Error("Filter() should reject unwiped node when default privacy is high")
		}
	})

	t.Run("unlabeled pod uses default privacy — accepts wiped node", func(t *testing.T) {
		pod := nt.MakePod("test-pod", nil)
		node := nt.MakeNode("test-node", map[string]string{"nexa.io/wiped": "true"})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status != nil && !status.IsSuccess() {
			t.Errorf("Filter() should accept wiped node with default high privacy, got: %v", status.Message())
		}
	})

	t.Run("explicit pod label overrides default", func(t *testing.T) {
		pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "standard"})
		node := nt.MakeNode("test-node", map[string]string{})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status != nil && !status.IsSuccess() {
			t.Errorf("explicit standard label should override default high, got: %v", status.Message())
		}
	})
}

func TestFilterStrictOrgIsolation(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{
		Enabled:            true,
		StrictOrgIsolation: true,
	}}}
	p := NewWithProvider(provider)

	t.Run("standard pod with org — rejects cross-org node", func(t *testing.T) {
		pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "standard", "nexa.io/org": "acme"})
		node := nt.MakeNode("test-node", map[string]string{"nexa.io/last-workload-org": "evil-corp"})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status == nil || status.IsSuccess() {
			t.Error("strict org isolation should reject cross-org node for standard pod")
		}
		if status != nil && !strings.Contains(status.Message(), "strict org isolation") {
			t.Errorf("reason should mention strict org isolation, got: %q", status.Message())
		}
	})

	t.Run("standard pod with org — accepts same-org node", func(t *testing.T) {
		pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "standard", "nexa.io/org": "acme"})
		node := nt.MakeNode("test-node", map[string]string{"nexa.io/last-workload-org": "acme"})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status != nil && !status.IsSuccess() {
			t.Errorf("strict org isolation should accept same-org node, got: %v", status.Message())
		}
	})

	t.Run("standard pod with org — rejects node with cross-org running pod", func(t *testing.T) {
		pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "standard", "nexa.io/org": "acme"})
		node := nt.MakeNode("test-node", map[string]string{})
		existingPod := nt.MakePod("evil-pod", map[string]string{"nexa.io/org": "evil-corp"})
		nodeInfo := nt.MakeNodeInfo(node, existingPod)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status == nil || status.IsSuccess() {
			t.Error("strict org isolation should reject node with cross-org running pod")
		}
	})

	t.Run("pod without org label — not affected by strict isolation", func(t *testing.T) {
		pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "standard"})
		node := nt.MakeNode("test-node", map[string]string{"nexa.io/last-workload-org": "evil-corp"})
		nodeInfo := nt.MakeNodeInfo(node)

		status := p.Filter(context.Background(), nil, pod, nodeInfo)
		if status != nil && !status.IsSuccess() {
			t.Errorf("pod without org label should not be affected by strict isolation, got: %v", status.Message())
		}
	})
}

func TestFilterPolicyError(t *testing.T) {
	p := NewWithProvider(&policy.StaticProvider{Err: errors.New("config unavailable")})
	pod := nt.MakePod("test-pod", nil)
	node := nt.MakeNode("test-node", nil)
	nodeInfo := nt.MakeNodeInfo(node)

	status := p.Filter(context.Background(), nil, pod, nodeInfo)
	if status == nil || status.Code() != fwk.Error {
		t.Error("policy error should produce Error status (fail closed)")
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name       string
		podLabels  map[string]string
		nodeLabels map[string]string
		wantScore  int64
	}{
		{
			name:       "no privacy label — score 0",
			podLabels:  nil,
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			wantScore:  0,
		},
		{
			name:       "standard privacy — score 0",
			podLabels:  map[string]string{"nexa.io/privacy": "standard"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			wantScore:  0,
		},
		{
			name:       "high privacy + wiped node — score 100",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/wiped": "true"},
			wantScore:  framework.MaxNodeScore,
		},
		{
			name:       "high privacy + not wiped + same org — score 50",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/last-workload-org": "acme"},
			wantScore:  framework.MaxNodeScore / 2,
		},
		{
			name:       "high privacy + not wiped + no org history — score 50",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{},
			wantScore:  framework.MaxNodeScore / 2,
		},
		{
			name:       "high privacy + not wiped + different org — score 0",
			podLabels:  map[string]string{"nexa.io/privacy": "high", "nexa.io/org": "acme"},
			nodeLabels: map[string]string{"nexa.io/last-workload-org": "evil-corp"},
			wantScore:  0,
		},
		{
			name:       "high privacy + not wiped + no pod org — score 0",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{},
			wantScore:  0,
		},
	}

	p := NewWithProvider(enabledPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			score, status := p.Score(context.Background(), nil, pod, nodeInfo)
			if status != nil && !status.IsSuccess() {
				t.Errorf("Score() status = %v, want success", status.Message())
			}
			if score != tt.wantScore {
				t.Errorf("Score() = %d, want %d", score, tt.wantScore)
			}
		})
	}
}

func TestScoreDisabledPlugin(t *testing.T) {
	p := NewWithProvider(&policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{Enabled: false}}})
	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "high"})
	node := nt.MakeNode("test-node", map[string]string{"nexa.io/wiped": "true"})
	nodeInfo := nt.MakeNodeInfo(node)

	score, status := p.Score(context.Background(), nil, pod, nodeInfo)
	if status != nil && !status.IsSuccess() {
		t.Errorf("disabled plugin score status = %v, want success", status.Message())
	}
	if score != 0 {
		t.Errorf("disabled plugin Score() = %d, want 0", score)
	}
}

func TestFilterCooldown(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fixedNow := func() time.Time { return now }

	provider := &policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{
		Enabled:       true,
		CooldownHours: 24,
	}}}

	tests := []struct {
		name       string
		nodeLabels map[string]string
		wantPass   bool
		wantReason string
	}{
		{
			name: "recent wipe — accept",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": now.Add(-2 * time.Hour).Format(time.RFC3339),
			},
			wantPass: true,
		},
		{
			name: "stale wipe — reject",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": now.Add(-48 * time.Hour).Format(time.RFC3339),
			},
			wantPass:   false,
			wantReason: "exceeds cooldown",
		},
		{
			name: "missing wipe-timestamp — reject (fail-closed)",
			nodeLabels: map[string]string{
				"nexa.io/wiped": "true",
			},
			wantPass:   false,
			wantReason: "missing nexa.io/wipe-timestamp",
		},
		{
			name: "malformed wipe-timestamp — reject",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": "not-a-timestamp",
			},
			wantPass:   false,
			wantReason: "malformed wipe-timestamp",
		},
		{
			name: "future timestamp (clock skew) — accept",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": now.Add(1 * time.Hour).Format(time.RFC3339),
			},
			wantPass: true,
		},
		{
			name: "exactly at cooldown boundary — accept",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": now.Add(-24 * time.Hour).Format(time.RFC3339),
			},
			wantPass: true,
		},
		{
			name: "one second past cooldown — reject",
			nodeLabels: map[string]string{
				"nexa.io/wiped":          "true",
				"nexa.io/wipe-timestamp": now.Add(-24*time.Hour - time.Second).Format(time.RFC3339),
			},
			wantPass:   false,
			wantReason: "exceeds cooldown",
		},
	}

	p := NewWithProvider(provider, fixedNow)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "high"})
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)
			if tt.wantPass {
				if status != nil && !status.IsSuccess() {
					t.Errorf("expected accept, got reject: %s", status.Message())
				}
			} else {
				if status == nil || status.IsSuccess() {
					t.Error("expected reject, got accept")
				} else if status.Code() != fwk.Unschedulable {
					t.Errorf("code = %v, want Unschedulable", status.Code())
				} else if tt.wantReason != "" && !strings.Contains(status.Message(), tt.wantReason) {
					t.Errorf("reason %q not found in %q", tt.wantReason, status.Message())
				}
			}
		})
	}
}

func TestFilterCooldownDisabled(t *testing.T) {
	// CooldownHours=0 means disabled — existing behavior unchanged.
	provider := &policy.StaticProvider{P: &policy.Policy{Privacy: policy.PrivacyPolicy{
		Enabled:       true,
		CooldownHours: 0,
	}}}
	p := NewWithProvider(provider)

	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/privacy": "high"})
	node := nt.MakeNode("test-node", map[string]string{"nexa.io/wiped": "true"})
	nodeInfo := nt.MakeNodeInfo(node)

	status := p.Filter(context.Background(), nil, pod, nodeInfo)
	if status != nil && !status.IsSuccess() {
		t.Errorf("cooldown disabled should accept wiped node without timestamp, got: %s", status.Message())
	}
}
