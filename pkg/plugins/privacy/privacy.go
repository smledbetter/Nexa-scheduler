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

	"github.com/nexascheduler/nexa/pkg/metrics"
	"github.com/nexascheduler/nexa/pkg/policy"
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
	policy policy.Provider
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
	pol, err := p.policy.GetPolicy()
	if err != nil {
		recordPolicyEval(Name, "error")
		recordFilter(Name, "error")
		return fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to read policy: %v", err))
	}
	recordPolicyEval(Name, "success")

	if !pol.Privacy.Enabled {
		recordFilter(Name, "accepted")
		return nil
	}

	privacyLevel := podLabelWithDefault(pod, labelPrivacy, pol.Privacy.DefaultPrivacy)

	// Strict org isolation applies org checks to ALL pods, not just high-privacy.
	if pol.Privacy.StrictOrgIsolation {
		podOrg := podLabel(pod, labelOrg)
		if podOrg != "" {
			node := nodeInfo.Node()
			lastOrg := node.Labels[labelLastWorkload]
			if lastOrg != "" && lastOrg != podOrg {
				recordIsolationViolation("strict_org")
				recordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s last workload org %q does not match pod org %q; strict org isolation is enabled",
					node.Name, lastOrg, podOrg,
				))
			}
			for _, pi := range nodeInfo.GetPods() {
				existingPod := pi.GetPod()
				existingOrg := podLabel(existingPod, labelOrg)
				if existingOrg != "" && existingOrg != podOrg {
					recordIsolationViolation("strict_org")
					recordFilter(Name, "rejected")
					return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
						"node %s has pod %s from org %q; strict org isolation rejects pod from org %q",
						node.Name, existingPod.Name, existingOrg, podOrg,
					))
				}
			}
		}
	}

	if privacyLevel != privacyHigh {
		recordFilter(Name, "accepted")
		return nil
	}

	node := nodeInfo.Node()

	// Check 1: Node must be wiped.
	if node.Labels[labelWiped] != "true" {
		recordIsolationViolation("node_not_wiped")
		recordFilter(Name, "rejected")
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s is not wiped (nexa.io/wiped != true); run node wipe procedure before scheduling high-privacy workloads",
			node.Name,
		))
	}

	podOrg := podLabel(pod, labelOrg)
	if podOrg == "" {
		recordFilter(Name, "accepted")
		return nil
	}

	// Check 2: Node's last workload org must be compatible.
	lastOrg := node.Labels[labelLastWorkload]
	if lastOrg != "" && lastOrg != podOrg {
		recordIsolationViolation("cross_org")
		recordFilter(Name, "rejected")
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
			recordIsolationViolation("cross_org")
			recordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s has pod %s from org %q; high-privacy pod from org %q requires org isolation",
				node.Name, existingPod.Name, existingOrg, podOrg,
			))
		}
	}

	recordFilter(Name, "accepted")
	return nil
}

// Score ranks nodes by cleanliness and privacy suitability.
//
//	Wiped node, same or no org history: framework.MaxNodeScore (100)
//	Not wiped, same org:                framework.MaxNodeScore / 2 (50)
//	No privacy label on pod:            0
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	pol, err := p.policy.GetPolicy()
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to read policy: %v", err))
	}

	if !pol.Privacy.Enabled {
		return 0, nil
	}

	privacyLevel := podLabelWithDefault(pod, labelPrivacy, pol.Privacy.DefaultPrivacy)
	if privacyLevel != privacyHigh {
		return 0, nil
	}

	node := nodeInfo.Node()
	wiped := node.Labels[labelWiped] == "true"

	if wiped {
		recordScore(Name, float64(framework.MaxNodeScore))
		return framework.MaxNodeScore, nil
	}

	// Not wiped — partial score if same org.
	podOrg := podLabel(pod, labelOrg)
	lastOrg := node.Labels[labelLastWorkload]
	if podOrg != "" && (lastOrg == "" || lastOrg == podOrg) {
		recordScore(Name, float64(framework.MaxNodeScore/2))
		return framework.MaxNodeScore / 2, nil
	}

	recordScore(Name, 0)
	return 0, nil
}

// ScoreExtensions returns nil since scores are already in the 0-100 range.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// NewWithProvider creates a Privacy plugin with the given policy provider.
// Used in tests and integration tests to inject a StaticProvider.
func NewWithProvider(provider policy.Provider) *Plugin {
	return &Plugin{policy: provider}
}

// New creates a new Privacy plugin with a ConfigMap-backed policy provider.
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	provider := policy.NewConfigMapProvider(
		h.SharedInformerFactory(),
		policy.DefaultNamespace,
		policy.DefaultConfigMapName,
	)
	return &Plugin{handle: h, policy: provider}, nil
}

// recordFilter increments the filter result counter if metrics are registered.
func recordFilter(plugin, result string) {
	if metrics.FilterResults != nil {
		metrics.FilterResults.WithLabelValues(plugin, result).Inc()
	}
}

// recordPolicyEval increments the policy evaluation counter if metrics are registered.
func recordPolicyEval(plugin, result string) {
	if metrics.PolicyEvaluations != nil {
		metrics.PolicyEvaluations.WithLabelValues(plugin, result).Inc()
	}
}

// recordScore observes a score value if metrics are registered.
func recordScore(plugin string, score float64) {
	if metrics.ScoreDistribution != nil {
		metrics.ScoreDistribution.WithLabelValues(plugin).Observe(score)
	}
}

// recordIsolationViolation increments the isolation violation counter if metrics are registered.
func recordIsolationViolation(reason string) {
	if metrics.IsolationViolations != nil {
		metrics.IsolationViolations.WithLabelValues(reason).Inc()
	}
}

// podLabel returns the value of a label on a pod, or "" if absent.
func podLabel(pod *v1.Pod, key string) string {
	if pod.Labels == nil {
		return ""
	}
	return pod.Labels[key]
}

// podLabelWithDefault returns the pod's label value, or the default if absent/empty.
func podLabelWithDefault(pod *v1.Pod, key, defaultVal string) string {
	if pod.Labels != nil {
		if v := pod.Labels[key]; v != "" {
			return v
		}
	}
	return defaultVal
}
