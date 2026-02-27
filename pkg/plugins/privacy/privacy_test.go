package privacy

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	nt "github.com/nexascheduler/nexa/pkg/testing"
)

// Compile-time interface compliance checks.
var _ framework.FilterPlugin = (*Plugin)(nil)
var _ framework.ScorePlugin = (*Plugin)(nil)

func TestName(t *testing.T) {
	p := &Plugin{}
	if got := p.Name(); got != Name {
		t.Errorf("Name() = %q, want %q", got, Name)
	}
}

func TestNew(t *testing.T) {
	plugin, err := New(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if plugin == nil {
		t.Fatal("New() returned nil plugin")
	}
	if plugin.Name() != Name {
		t.Errorf("New() plugin name = %q, want %q", plugin.Name(), Name)
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

	p := &Plugin{}
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

	p := &Plugin{}
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
