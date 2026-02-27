package region

import (
	"context"
	"strings"
	"testing"

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
		name       string
		podLabels  map[string]string
		nodeLabels map[string]string
		wantPass   bool
		wantReason string // substring to match in rejection reason
	}{
		{
			name:       "no pod labels — accept all nodes",
			podLabels:  nil,
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantPass:   true,
		},
		{
			name:       "empty region label — treated as no preference",
			podLabels:  map[string]string{"nexa.io/region": ""},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantPass:   true,
		},
		{
			name:       "region match — accept",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantPass:   true,
		},
		{
			name:       "region mismatch — reject",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: map[string]string{"nexa.io/region": "eu-west1"},
			wantPass:   false,
			wantReason: "does not match required region",
		},
		{
			name:       "node missing region label — reject",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: nil,
			wantPass:   false,
			wantReason: "does not match required region",
		},
		{
			name:       "zone match — accept",
			podLabels:  map[string]string{"nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/zone": "us-west1-a"},
			wantPass:   true,
		},
		{
			name:       "zone mismatch — reject",
			podLabels:  map[string]string{"nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/zone": "us-west1-b"},
			wantPass:   false,
			wantReason: "does not match required zone",
		},
		{
			name:       "region match + zone match — accept",
			podLabels:  map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			wantPass:   true,
		},
		{
			name:       "region match + zone mismatch — reject",
			podLabels:  map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-b"},
			wantPass:   false,
			wantReason: "does not match required zone",
		},
		{
			name:       "special characters in region label — no panic",
			podLabels:  map[string]string{"nexa.io/region": "us-west1/special;chars"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantPass:   false,
			wantReason: "does not match required region",
		},
		{
			name:       "actionable reason includes fix suggestion",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: map[string]string{"nexa.io/region": "eu-west1"},
			wantPass:   false,
			wantReason: "add label nexa.io/region=us-west1",
		},
	}

	p := &Plugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

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
			name:       "no pod labels — score 0",
			podLabels:  nil,
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantScore:  0,
		},
		{
			name:       "region match only — score 50",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
			wantScore:  framework.MaxNodeScore / 2,
		},
		{
			name:       "zone match only — score 100",
			podLabels:  map[string]string{"nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/zone": "us-west1-a"},
			wantScore:  framework.MaxNodeScore,
		},
		{
			name:       "region + zone match — score 100",
			podLabels:  map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			wantScore:  framework.MaxNodeScore,
		},
		{
			name:       "region match + zone mismatch — score 50",
			podLabels:  map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-a"},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1", "nexa.io/zone": "us-west1-b"},
			wantScore:  framework.MaxNodeScore / 2,
		},
		{
			name:       "region mismatch — score 0",
			podLabels:  map[string]string{"nexa.io/region": "us-west1"},
			nodeLabels: map[string]string{"nexa.io/region": "eu-west1"},
			wantScore:  0,
		},
		{
			name:       "empty region label — score 0 (no preference)",
			podLabels:  map[string]string{"nexa.io/region": ""},
			nodeLabels: map[string]string{"nexa.io/region": "us-west1"},
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
