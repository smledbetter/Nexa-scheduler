// Package region implements region and zone affinity scheduling.
// In this initial scaffold, the plugin is a no-op that accepts all nodes.
package region

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "NexaRegion"

// Plugin implements region and zone affinity filtering and scoring.
type Plugin struct {
	handle framework.Handle
}

var _ framework.FilterPlugin = (*Plugin)(nil)
var _ framework.ScorePlugin = (*Plugin)(nil)

// Name returns the name of the plugin.
func (p *Plugin) Name() string {
	return Name
}

// Filter evaluates whether a node is suitable for the pod based on region/zone labels.
// Currently a no-op: accepts all nodes.
func (p *Plugin) Filter(_ context.Context, _ fwk.CycleState, _ *v1.Pod, _ fwk.NodeInfo) *fwk.Status {
	return nil // nil means success
}

// Score ranks nodes by region/zone match quality.
// Currently a no-op: all nodes score 0.
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, _ *v1.Pod, _ fwk.NodeInfo) (int64, *fwk.Status) {
	return 0, nil
}

// ScoreExtensions returns nil since no normalization is needed.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New creates a new Region plugin.
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	return &Plugin{handle: h}, nil
}
