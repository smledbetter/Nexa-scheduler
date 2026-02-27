// Package region implements region and zone affinity scheduling.
// Pods with nexa.io/region or nexa.io/zone labels are only placed on nodes
// with matching labels. Score prefers exact zone matches over region-only matches.
package region

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "NexaRegion"

	// Label keys for region and zone affinity.
	labelRegion = "nexa.io/region"
	labelZone   = "nexa.io/zone"
)

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

// Filter rejects nodes that do not match the pod's required region or zone.
// If the pod has no region/zone labels (or they are empty), all nodes pass.
func (p *Plugin) Filter(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) *fwk.Status {
	podRegion := podLabel(pod, labelRegion)
	podZone := podLabel(pod, labelZone)

	// No region/zone preference — accept all nodes.
	if podRegion == "" && podZone == "" {
		return nil
	}

	node := nodeInfo.Node()

	if podRegion != "" {
		nodeRegion := node.Labels[labelRegion]
		if nodeRegion != podRegion {
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s region %q does not match required region %q; add label %s=%s to the node",
				node.Name, nodeRegion, podRegion, labelRegion, podRegion,
			))
		}
	}

	if podZone != "" {
		nodeZone := node.Labels[labelZone]
		if nodeZone != podZone {
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s zone %q does not match required zone %q; add label %s=%s to the node",
				node.Name, nodeZone, podZone, labelZone, podZone,
			))
		}
	}

	return nil
}

// Score ranks nodes by region/zone match quality.
//
//	Exact zone + region match: framework.MaxNodeScore (100)
//	Region match only:         framework.MaxNodeScore / 2 (50)
//	No pod preference:         0
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	podRegion := podLabel(pod, labelRegion)
	podZone := podLabel(pod, labelZone)

	// No preference — neutral score.
	if podRegion == "" && podZone == "" {
		return 0, nil
	}

	node := nodeInfo.Node()
	var score int64

	// Region match.
	if podRegion != "" && node.Labels[labelRegion] == podRegion {
		score = framework.MaxNodeScore / 2
	}

	// Zone match upgrades to max score.
	if podZone != "" && node.Labels[labelZone] == podZone {
		score = framework.MaxNodeScore
	}

	return score, nil
}

// ScoreExtensions returns nil since scores are already in the 0-100 range.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New creates a new Region plugin.
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	return &Plugin{handle: h}, nil
}

// podLabel returns the value of a label on a pod, or "" if absent.
func podLabel(pod *v1.Pod, key string) string {
	if pod.Labels == nil {
		return ""
	}
	return pod.Labels[key]
}
