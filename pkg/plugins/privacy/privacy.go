// Package privacy implements privacy-aware scheduling with node cleanliness checks.
// Pods with nexa.io/privacy=high require clean, org-isolated nodes.
// Standard-privacy and unlabeled pods are accepted on any node.
package privacy

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/metrics"
	"github.com/nexascheduler/nexa/pkg/plugins"
	"github.com/nexascheduler/nexa/pkg/policy"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "NexaPrivacy"

	// Label keys.
	labelPrivacy       = "nexa.io/privacy"
	labelOrg           = "nexa.io/org"
	labelWiped         = "nexa.io/wiped"
	labelLastWorkload  = "nexa.io/last-workload-org"
	labelWipeTimestamp = "nexa.io/wipe-timestamp"

	// Privacy level that triggers strict filtering.
	privacyHigh = "high"
)

// Plugin implements privacy-aware filtering and scoring based on node cleanliness.
type Plugin struct {
	plugins.Base
	nowFunc func() time.Time
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
	pol, status := p.GetPolicyOrFail()
	if status != nil {
		return status
	}

	if !pol.Privacy.Enabled {
		metrics.RecordFilter(Name, "accepted")
		return nil
	}

	privacyLevel := plugins.PodLabelWithDefault(pod, labelPrivacy, pol.Privacy.DefaultPrivacy)

	// Strict org isolation applies org checks to ALL pods, not just high-privacy.
	if pol.Privacy.StrictOrgIsolation {
		podOrg := plugins.PodLabel(pod, labelOrg)
		if podOrg != "" {
			node := nodeInfo.Node()
			lastOrg := node.Labels[labelLastWorkload]
			if lastOrg != "" && lastOrg != podOrg {
				recordIsolationViolation("strict_org")
				metrics.RecordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s last workload org %q does not match pod org %q; strict org isolation is enabled",
					node.Name, lastOrg, podOrg,
				))
			}
			for _, pi := range nodeInfo.GetPods() {
				existingPod := pi.GetPod()
				existingOrg := plugins.PodLabel(existingPod, labelOrg)
				if existingOrg != "" && existingOrg != podOrg {
					recordIsolationViolation("strict_org")
					metrics.RecordFilter(Name, "rejected")
					return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
						"node %s has pod %s from org %q; strict org isolation rejects pod from org %q",
						node.Name, existingPod.Name, existingOrg, podOrg,
					))
				}
			}
		}
	}

	if privacyLevel != privacyHigh {
		metrics.RecordFilter(Name, "accepted")
		return nil
	}

	node := nodeInfo.Node()

	// Check 1: Node must be wiped.
	if node.Labels[labelWiped] != "true" {
		recordIsolationViolation("node_not_wiped")
		metrics.RecordFilter(Name, "rejected")
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s is not wiped (nexa.io/wiped != true); run node wipe procedure before scheduling high-privacy workloads",
			node.Name,
		))
	}

	// Check 1b: Wipe freshness (cooldown).
	if pol.Privacy.CooldownHours > 0 {
		tsStr := node.Labels[labelWipeTimestamp]
		if tsStr == "" {
			recordIsolationViolation("stale_wipe")
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s is missing nexa.io/wipe-timestamp; cooldown policy requires a wipe timestamp",
				node.Name,
			))
		}
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			recordIsolationViolation("stale_wipe")
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s has malformed wipe-timestamp %q; expected RFC3339 format",
				node.Name, tsStr,
			))
		}
		cooldown := time.Duration(pol.Privacy.CooldownHours) * time.Hour
		if p.nowFunc().Sub(ts) > cooldown {
			recordIsolationViolation("stale_wipe")
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s wipe-timestamp %s exceeds cooldown of %dh; re-wipe the node",
				node.Name, tsStr, pol.Privacy.CooldownHours,
			))
		}
	}

	podOrg := plugins.PodLabel(pod, labelOrg)
	if podOrg == "" {
		metrics.RecordFilter(Name, "accepted")
		return nil
	}

	// Check 2: Node's last workload org must be compatible.
	lastOrg := node.Labels[labelLastWorkload]
	if lastOrg != "" && lastOrg != podOrg {
		recordIsolationViolation("cross_org")
		metrics.RecordFilter(Name, "rejected")
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s last workload org %q does not match pod org %q; node must be wiped or used by the same org",
			node.Name, lastOrg, podOrg,
		))
	}

	// Check 3: No running pods from a different org.
	for _, pi := range nodeInfo.GetPods() {
		existingPod := pi.GetPod()
		existingOrg := plugins.PodLabel(existingPod, labelOrg)
		if existingOrg != "" && existingOrg != podOrg {
			recordIsolationViolation("cross_org")
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s has pod %s from org %q; high-privacy pod from org %q requires org isolation",
				node.Name, existingPod.Name, existingOrg, podOrg,
			))
		}
	}

	metrics.RecordFilter(Name, "accepted")
	return nil
}

// Score ranks nodes by cleanliness and privacy suitability.
//
//	Wiped node, same or no org history: framework.MaxNodeScore (100)
//	Not wiped, same org:                framework.MaxNodeScore / 2 (50)
//	No privacy label on pod:            0
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	pol, err := p.Policy.GetPolicy()
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to read policy: %v", err))
	}

	if !pol.Privacy.Enabled {
		return 0, nil
	}

	privacyLevel := plugins.PodLabelWithDefault(pod, labelPrivacy, pol.Privacy.DefaultPrivacy)
	if privacyLevel != privacyHigh {
		return 0, nil
	}

	node := nodeInfo.Node()
	wiped := node.Labels[labelWiped] == "true"

	if wiped {
		metrics.RecordScore(Name, float64(framework.MaxNodeScore))
		return framework.MaxNodeScore, nil
	}

	// Not wiped — partial score if same org.
	podOrg := plugins.PodLabel(pod, labelOrg)
	lastOrg := node.Labels[labelLastWorkload]
	if podOrg != "" && (lastOrg == "" || lastOrg == podOrg) {
		metrics.RecordScore(Name, float64(framework.MaxNodeScore/2))
		return framework.MaxNodeScore / 2, nil
	}

	metrics.RecordScore(Name, 0)
	return 0, nil
}

// NewWithProvider creates a Privacy plugin with the given policy provider.
// Used in tests and integration tests to inject a StaticProvider.
// An optional nowFunc overrides time.Now for deterministic cooldown tests.
func NewWithProvider(provider policy.Provider, nowFunc ...func() time.Time) *Plugin {
	nf := time.Now
	if len(nowFunc) > 0 && nowFunc[0] != nil {
		nf = nowFunc[0]
	}
	return &Plugin{Base: plugins.Base{Policy: provider, PluginName: Name}, nowFunc: nf}
}

// New creates a new Privacy plugin with a composite policy provider (CRD + ConfigMap fallback).
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	base, err := plugins.NewBase(Name, h)
	if err != nil {
		return nil, err
	}
	return &Plugin{Base: base, nowFunc: time.Now}, nil
}

// recordIsolationViolation increments the isolation violation counter if metrics are registered.
func recordIsolationViolation(reason string) {
	if metrics.IsolationViolations != nil {
		metrics.IsolationViolations.WithLabelValues(reason).Inc()
	}
}
