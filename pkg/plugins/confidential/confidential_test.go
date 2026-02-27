package confidential

import (
	"context"
	"errors"
	"strings"
	"testing"

	fwk "k8s.io/kube-scheduler/framework"

	nt "github.com/nexascheduler/nexa/pkg/testing"

	"github.com/nexascheduler/nexa/pkg/policy"
)

func enabledPolicy() policy.Provider {
	return &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{Enabled: true}}}
}

func TestName(t *testing.T) {
	p := NewWithProvider(enabledPolicy())
	if p.Name() != Name {
		t.Errorf("Name() = %q, want %q", p.Name(), Name)
	}
}

func TestScoreExtensions(t *testing.T) {
	p := NewWithProvider(enabledPolicy())
	if p.ScoreExtensions() != nil {
		t.Error("ScoreExtensions() should return nil")
	}
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name       string
		podLabels  map[string]string
		nodeLabels map[string]string
		wantPass   bool
		wantReason string
	}{
		{
			name:       "no confidential label -- accept any node",
			podLabels:  map[string]string{},
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
		{
			name:       "confidential=required + node has TDX -- accept",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   true,
		},
		{
			name:       "confidential=required + node has SEV-SNP -- accept",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "sev-snp"},
			wantPass:   true,
		},
		{
			name:       "confidential=required + node has tee=none -- reject",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "none"},
			wantPass:   false,
			wantReason: "no TEE capability",
		},
		{
			name:       "confidential=required + node has no tee label -- reject (fail-closed)",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{},
			wantPass:   false,
			wantReason: "no TEE capability",
		},
		{
			name:       "confidential=optional -- accept any node",
			podLabels:  map[string]string{"nexa.io/confidential": "optional"},
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
		{
			name:       "adversarial: node claims confidential=true but tee=none",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/confidential": "true", "nexa.io/tee": "none"},
			wantPass:   false,
			wantReason: "no TEE capability",
		},
	}

	p := NewWithProvider(enabledPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)
			if tt.wantPass {
				if !status.IsSuccess() {
					t.Errorf("expected accept, got reject: %s", status.Message())
				}
			} else {
				if status.IsSuccess() {
					t.Error("expected reject, got accept")
				}
				if status.Code() != fwk.Unschedulable {
					t.Errorf("expected Unschedulable, got %v", status.Code())
				}
				if tt.wantReason != "" && !strings.Contains(status.Message(), tt.wantReason) {
					t.Errorf("reason %q not found in message %q", tt.wantReason, status.Message())
				}
			}
		})
	}
}

func TestFilterDiskEncryption(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{
		Enabled:              true,
		RequireEncryptedDisk: true,
	}}}
	p := NewWithProvider(provider)

	tests := []struct {
		name       string
		podLabels  map[string]string
		nodeLabels map[string]string
		wantPass   bool
	}{
		{
			name:       "encrypted disk -- accept",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx", "nexa.io/disk-encrypted": "true"},
			wantPass:   true,
		},
		{
			name:       "no disk encryption -- reject",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx", "nexa.io/disk-encrypted": "false"},
			wantPass:   false,
		},
		{
			name:       "missing disk-encrypted label -- reject",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   false,
		},
		{
			name:       "non-confidential pod -- disk encryption not checked",
			podLabels:  map[string]string{},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)
			if tt.wantPass && !status.IsSuccess() {
				t.Errorf("expected accept, got reject: %s", status.Message())
			}
			if !tt.wantPass && status.IsSuccess() {
				t.Error("expected reject, got accept")
			}
		})
	}
}

func TestFilterRuntimeClass(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{
		Enabled:             true,
		RequireRuntimeClass: "kata-cc",
	}}}
	p := NewWithProvider(provider)

	tests := []struct {
		name         string
		podLabels    map[string]string
		runtimeClass string
		nodeLabels   map[string]string
		wantPass     bool
	}{
		{
			name:         "matching runtimeClass -- accept",
			podLabels:    map[string]string{"nexa.io/confidential": "required"},
			runtimeClass: "kata-cc",
			nodeLabels:   map[string]string{"nexa.io/tee": "tdx"},
			wantPass:     true,
		},
		{
			name:         "wrong runtimeClass -- reject",
			podLabels:    map[string]string{"nexa.io/confidential": "required"},
			runtimeClass: "runc",
			nodeLabels:   map[string]string{"nexa.io/tee": "tdx"},
			wantPass:     false,
		},
		{
			name:       "missing runtimeClass -- reject",
			podLabels:  map[string]string{"nexa.io/confidential": "required"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   false,
		},
		{
			name:       "non-confidential pod -- runtimeClass not checked",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			if tt.runtimeClass != "" {
				rc := tt.runtimeClass
				pod.Spec.RuntimeClassName = &rc
			}
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)
			if tt.wantPass && !status.IsSuccess() {
				t.Errorf("expected accept, got reject: %s", status.Message())
			}
			if !tt.wantPass && status.IsSuccess() {
				t.Error("expected reject, got accept")
			}
		})
	}
}

func TestFilterRequireTEEForHigh(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{
		Enabled:           true,
		RequireTEEForHigh: true,
	}}}
	p := NewWithProvider(provider)

	tests := []struct {
		name       string
		podLabels  map[string]string
		nodeLabels map[string]string
		wantPass   bool
	}{
		{
			name:       "high privacy + TEE node -- accept",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{"nexa.io/tee": "tdx"},
			wantPass:   true,
		},
		{
			name:       "high privacy + no TEE -- reject",
			podLabels:  map[string]string{"nexa.io/privacy": "high"},
			nodeLabels: map[string]string{},
			wantPass:   false,
		},
		{
			name:       "standard privacy -- accept without TEE",
			podLabels:  map[string]string{"nexa.io/privacy": "standard"},
			nodeLabels: map[string]string{},
			wantPass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			node := nt.MakeNode("test-node", tt.nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			status := p.Filter(context.Background(), nil, pod, nodeInfo)
			if tt.wantPass && !status.IsSuccess() {
				t.Errorf("expected accept, got reject: %s", status.Message())
			}
			if !tt.wantPass && status.IsSuccess() {
				t.Error("expected reject, got accept")
			}
		})
	}
}

func TestFilterDisabledPlugin(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{Enabled: false}}}
	p := NewWithProvider(provider)

	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/confidential": "required"})
	node := nt.MakeNode("test-node", map[string]string{})
	nodeInfo := nt.MakeNodeInfo(node)

	status := p.Filter(context.Background(), nil, pod, nodeInfo)
	if !status.IsSuccess() {
		t.Errorf("disabled plugin should accept all pods, got: %s", status.Message())
	}
}

func TestFilterPolicyError(t *testing.T) {
	provider := &policy.StaticProvider{Err: errors.New("config unavailable")}
	p := NewWithProvider(provider)

	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/confidential": "required"})
	node := nt.MakeNode("test-node", map[string]string{"nexa.io/tee": "tdx"})
	nodeInfo := nt.MakeNodeInfo(node)

	status := p.Filter(context.Background(), nil, pod, nodeInfo)
	if status.Code() != fwk.Error {
		t.Errorf("expected Error status, got %v", status.Code())
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name      string
		podLabels map[string]string
		nodeTEE   string
		wantScore int64
	}{
		{
			name:      "no confidential label -- score 0",
			podLabels: map[string]string{},
			nodeTEE:   "tdx",
			wantScore: 0,
		},
		{
			name:      "confidential + TEE type match -- score 100",
			podLabels: map[string]string{"nexa.io/confidential": "required", "nexa.io/tee-type": "tdx"},
			nodeTEE:   "tdx",
			wantScore: 100,
		},
		{
			name:      "confidential + TEE type mismatch -- score 50",
			podLabels: map[string]string{"nexa.io/confidential": "required", "nexa.io/tee-type": "sev-snp"},
			nodeTEE:   "tdx",
			wantScore: 50,
		},
		{
			name:      "confidential + no type preference -- score 100",
			podLabels: map[string]string{"nexa.io/confidential": "required"},
			nodeTEE:   "tdx",
			wantScore: 100,
		},
		{
			name:      "confidential + no TEE on node -- score 0",
			podLabels: map[string]string{"nexa.io/confidential": "required"},
			nodeTEE:   "",
			wantScore: 0,
		},
		{
			name:      "confidential + tee=none -- score 0",
			podLabels: map[string]string{"nexa.io/confidential": "required"},
			nodeTEE:   "none",
			wantScore: 0,
		},
	}

	p := NewWithProvider(enabledPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nt.MakePod("test-pod", tt.podLabels)
			nodeLabels := map[string]string{}
			if tt.nodeTEE != "" {
				nodeLabels["nexa.io/tee"] = tt.nodeTEE
			}
			node := nt.MakeNode("test-node", nodeLabels)
			nodeInfo := nt.MakeNodeInfo(node)

			score, status := p.Score(context.Background(), nil, pod, nodeInfo)
			if !status.IsSuccess() {
				t.Errorf("unexpected error: %s", status.Message())
			}
			if score != tt.wantScore {
				t.Errorf("score = %d, want %d", score, tt.wantScore)
			}
		})
	}
}

func TestScoreDefaultTEEType(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{
		Enabled:        true,
		DefaultTEEType: "tdx",
	}}}
	p := NewWithProvider(provider)

	// Pod has no tee-type label, but policy default is "tdx".
	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/confidential": "required"})

	// Node matches default type.
	node := nt.MakeNode("match-node", map[string]string{"nexa.io/tee": "tdx"})
	nodeInfo := nt.MakeNodeInfo(node)
	score, status := p.Score(context.Background(), nil, pod, nodeInfo)
	if !status.IsSuccess() {
		t.Errorf("unexpected error: %s", status.Message())
	}
	if score != 100 {
		t.Errorf("score = %d, want 100 (default TEE type match)", score)
	}

	// Node has different type.
	node2 := nt.MakeNode("other-node", map[string]string{"nexa.io/tee": "sev-snp"})
	nodeInfo2 := nt.MakeNodeInfo(node2)
	score2, _ := p.Score(context.Background(), nil, pod, nodeInfo2)
	if score2 != 50 {
		t.Errorf("score = %d, want 50 (TEE but wrong type)", score2)
	}
}

func TestScoreDisabledPlugin(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{Confidential: policy.ConfidentialPolicy{Enabled: false}}}
	p := NewWithProvider(provider)

	pod := nt.MakePod("test-pod", map[string]string{"nexa.io/confidential": "required"})
	node := nt.MakeNode("test-node", map[string]string{"nexa.io/tee": "tdx"})
	nodeInfo := nt.MakeNodeInfo(node)

	score, status := p.Score(context.Background(), nil, pod, nodeInfo)
	if !status.IsSuccess() {
		t.Errorf("unexpected error: %s", status.Message())
	}
	if score != 0 {
		t.Errorf("disabled plugin should score 0, got %d", score)
	}
}
