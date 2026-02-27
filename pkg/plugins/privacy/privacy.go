// Package privacy implements privacy-aware scheduling with node cleanliness checks.
// Pods with nexa.io/privacy=high require clean, org-isolated nodes.
// Standard-privacy and unlabeled pods are accepted on any node.
package privacy

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
	Name = "NexaPrivacy"

	// Label keys.
	labelPrivacy      = "nexa.io/privacy"
	labelOrg          = "nexa.io/org"
	labelWiped        = "nexa.io/wiped"
	labelLastWorkload = "nexa.io/last-workload-org"

	// Privacy level that triggers strict filtering.
	privacyHigh = "high"
)

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

// Filter enforces privacy requirements for high-privacy pods:
//  1. Node must be wiped (nexa.io/wiped=true).
//  2. Node's last workload org must match the pod's org (or be absent).
//  3. No running pods from a different org on the node.
//
// Standard-privacy and unlabeled pods pass all nodes.
func (p *Plugin) Filter(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) *fwk.Status {
	if podLabel(pod, labelPrivacy) != privacyHigh {
		return nil
	}

	node := nodeInfo.Node()

	// Check 1: Node must be wiped.
	if node.Labels[labelWiped] != "true" {
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s is not wiped (nexa.io/wiped != true); run node wipe procedure before scheduling high-privacy workloads",
			node.Name,
		))
	}

	podOrg := podLabel(pod, labelOrg)
	if podOrg == "" {
		return nil
	}

	// Check 2: Node's last workload org must be compatible.
	lastOrg := node.Labels[labelLastWorkload]
	if lastOrg != "" && lastOrg != podOrg {
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s last workload org %q does not match pod org %q; node must be wiped or used by the same org",
			node.Name, lastOrg, podOrg,
		))
	}

	// Check 3: No running pods from a different org.
	for _, pi := range nodeInfo.GetPods() {
		existingPod := pi.GetPod()
		existingOrg := podLabel(existingPod, labelOrg)
		if existingOrg != "" && existingOrg != podOrg {
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s has pod %s from org %q; high-privacy pod from org %q requires org isolation",
				node.Name, existingPod.Name, existingOrg, podOrg,
			))
		}
	}

	return nil
}

// Score ranks nodes by cleanliness and privacy suitability.
//
//	Wiped node, same or no org history: framework.MaxNodeScore (100)
//	Not wiped, same org:                framework.MaxNodeScore / 2 (50)
//	No privacy label on pod:            0
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	if podLabel(pod, labelPrivacy) != privacyHigh {
		return 0, nil
	}

	node := nodeInfo.Node()
	wiped := node.Labels[labelWiped] == "true"

	if wiped {
		return framework.MaxNodeScore, nil
	}

	// Not wiped — partial score if same org.
	podOrg := podLabel(pod, labelOrg)
	lastOrg := node.Labels[labelLastWorkload]
	if podOrg != "" && (lastOrg == "" || lastOrg == podOrg) {
		return framework.MaxNodeScore / 2, nil
	}

	return 0, nil
}

// ScoreExtensions returns nil since scores are already in the 0-100 range.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New creates a new Privacy plugin.
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
