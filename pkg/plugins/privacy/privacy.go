// Package privacy implements privacy-aware scheduling with node cleanliness checks.
// In this initial scaffold, the plugin is a no-op that accepts all nodes.
package privacy

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "NexaPrivacy"

// Plugin implements privacy-aware filtering and scoring based on node cleanliness.
type Plugin struct {
	handle framework.Handle
}

var _ framework.FilterPlugin = (*Plugin)(nil)
var _ framework.ScorePlugin = (*Plugin)(nil)

// Name returns the name of the plugin.
func (p *Plugin) Name() string {
	return Name
}

// Filter evaluates whether a node meets privacy and cleanliness requirements.
// Currently a no-op: accepts all nodes.
func (p *Plugin) Filter(_ context.Context, _ fwk.CycleState, _ *v1.Pod, _ fwk.NodeInfo) *fwk.Status {
	return nil // nil means success
}

// Score ranks nodes by cleanliness and privacy suitability.
// Currently a no-op: all nodes score 0.
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, _ *v1.Pod, _ fwk.NodeInfo) (int64, *fwk.Status) {
	return 0, nil
}

// ScoreExtensions returns nil since no normalization is needed.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New creates a new Privacy plugin.
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	return &Plugin{handle: h}, nil
}
