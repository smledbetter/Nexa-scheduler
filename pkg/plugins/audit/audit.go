package audit

import (
	"context"
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/policy"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "NexaAudit"

	// Label keys read from pods for audit logging (scheduling metadata only).
	labelPrivacy = "nexa.io/privacy"
	labelRegion  = "nexa.io/region"
	labelZone    = "nexa.io/zone"
	labelOrg     = "nexa.io/org"
)

// Plugin implements PostBind (success logging) and PostFilter (failure logging).
type Plugin struct {
	handle framework.Handle
	policy policy.Provider
	logger *Logger
}

var _ framework.PostBindPlugin = (*Plugin)(nil)
var _ framework.PostFilterPlugin = (*Plugin)(nil)

// Name returns the name of the plugin.
func (p *Plugin) Name() string {
	return Name
}

// PostBind logs a successful pod placement as structured JSON.
// Only scheduling metadata is logged — no env vars, secrets, or service account tokens.
func (p *Plugin) PostBind(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeName string) {
	pol := p.policySnapshot()
	p.logger.LogDecision(DecisionEntry{
		Event:  "scheduled",
		Pod:    podRef(pod),
		Node:   nodeName,
		Policy: pol,
	})
}

// PostFilter logs a scheduling failure when all nodes are rejected.
// INFO: summary with total filtered count. DEBUG: per-node rejection reasons.
// Returns (nil, Unschedulable) — informational only, does not trigger preemption.
func (p *Plugin) PostFilter(ctx context.Context, _ fwk.CycleState, pod *v1.Pod, filteredNodeStatusMap framework.NodeToStatusReader) (*framework.PostFilterResult, *fwk.Status) {
	pol := p.policySnapshot()

	// Collect per-node filter reasons from the status map.
	var filters []FilterResult
	lister := p.handle.SnapshotSharedLister().NodeInfos()
	for _, code := range []fwk.Code{fwk.Unschedulable, fwk.UnschedulableAndUnresolvable} {
		nodes, err := filteredNodeStatusMap.NodesForStatusCode(lister, code)
		if err != nil {
			continue
		}
		for _, ni := range nodes {
			nodeName := ni.Node().Name
			status := filteredNodeStatusMap.Get(nodeName)
			reason := ""
			if status != nil {
				reason = status.Message()
			}
			filters = append(filters, FilterResult{
				Node:   nodeName,
				Reason: reason,
			})
		}
	}

	// INFO: summary.
	p.logger.LogDecision(DecisionEntry{
		Event:   "scheduling_failed",
		Pod:     podRef(pod),
		Policy:  pol,
		Filters: filters,
	})

	// DEBUG: per-node detail (only when debug mode is on).
	if len(filters) > 0 {
		p.logger.LogFilterDetail(DecisionEntry{
			Event:   "filter_details",
			Pod:     podRef(pod),
			Policy:  pol,
			Filters: filters,
		})
	}

	return nil, fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
		"audit: %d node(s) filtered for pod %s/%s",
		len(filters), pod.Namespace, pod.Name,
	))
}

// NewWithLogger creates an Audit plugin with the given policy provider and logger.
// Used in tests to inject a StaticProvider and capture log output.
func NewWithLogger(handle framework.Handle, provider policy.Provider, logger *Logger) *Plugin {
	return &Plugin{handle: handle, policy: provider, logger: logger}
}

// New creates a new Audit plugin with a ConfigMap-backed policy provider
// and a Logger writing JSON lines to stderr.
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	provider := policy.NewConfigMapProvider(
		h.SharedInformerFactory(),
		policy.DefaultNamespace,
		policy.DefaultConfigMapName,
	)
	logger := NewLogger(os.Stderr, false)
	return &Plugin{handle: h, policy: provider, logger: logger}, nil
}

// policySnapshot reads the current policy state for inclusion in audit entries.
// If the provider returns an error, an empty snapshot is returned (logging still works).
func (p *Plugin) policySnapshot() PolicySnapshot {
	pol, err := p.policy.GetPolicy()
	if err != nil {
		return PolicySnapshot{}
	}
	return PolicySnapshot{
		RegionEnabled:  pol.Region.Enabled,
		PrivacyEnabled: pol.Privacy.Enabled,
	}
}

// podRef extracts scheduling-relevant metadata from a pod.
// Only label keys used by Nexa scheduling decisions are included.
func podRef(pod *v1.Pod) PodRef {
	return PodRef{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Privacy:   podLabel(pod, labelPrivacy),
		Region:    podLabel(pod, labelRegion),
		Zone:      podLabel(pod, labelZone),
		Org:       podLabel(pod, labelOrg),
	}
}

// podLabel returns the value of a label on a pod, or "" if absent.
func podLabel(pod *v1.Pod, key string) string {
	if pod.Labels == nil {
		return ""
	}
	return pod.Labels[key]
}
