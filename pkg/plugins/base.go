// Package plugins provides shared types and helpers for Nexa scheduler plugins.
// All Filter/Score plugins embed Base to eliminate boilerplate for policy access,
// metric recording, and common label helpers.
package plugins

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/metrics"
	"github.com/nexascheduler/nexa/pkg/policy"
)

// Base provides shared fields and methods for Nexa scheduler plugins.
// Embed this in plugin structs to get policy access, metric recording,
// and default ScoreExtensions.
type Base struct {
	Handle     framework.Handle
	Policy     policy.Provider
	PluginName string
}

// NewBase creates a Base with a composite policy provider wired from the framework Handle.
func NewBase(name string, h framework.Handle) (Base, error) {
	provider, err := policy.NewCompositeProviderFromHandle(h)
	if err != nil {
		return Base{}, fmt.Errorf("failed to create policy provider: %w", err)
	}
	return Base{Handle: h, Policy: provider, PluginName: name}, nil
}

// GetPolicyOrFail fetches the current policy and records metrics.
// On error it returns a framework Error status with both policy eval and filter
// metrics recorded as "error". On success it records policy eval as "success".
func (b *Base) GetPolicyOrFail() (*policy.Policy, *fwk.Status) {
	pol, err := b.Policy.GetPolicy()
	if err != nil {
		metrics.RecordPolicyEval(b.PluginName, "error")
		metrics.RecordFilter(b.PluginName, "error")
		return nil, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to read policy: %v", err))
	}
	metrics.RecordPolicyEval(b.PluginName, "success")
	return pol, nil
}

// ScoreExtensions returns nil since all Nexa plugins produce scores in the 0-100 range
// and do not need normalization.
func (b *Base) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// PodLabel returns the value of a label on a pod, or "" if absent.
func PodLabel(pod *v1.Pod, key string) string {
	if pod.Labels == nil {
		return ""
	}
	return pod.Labels[key]
}

// PodLabelWithDefault returns the pod's label value, or the default if absent/empty.
func PodLabelWithDefault(pod *v1.Pod, key, defaultVal string) string {
	if pod.Labels != nil {
		if v := pod.Labels[key]; v != "" {
			return v
		}
	}
	return defaultVal
}
