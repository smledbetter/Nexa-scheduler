// Package confidential implements scheduling for TEE-capable (Trusted Execution Environment) nodes.
// Pods with nexa.io/confidential=required are only placed on nodes with hardware TEE support.
// Optionally enforces disk encryption, runtimeClass, remote attestation, and TEE-for-high-privacy policies.
package confidential

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/metrics"
	"github.com/nexascheduler/nexa/pkg/nodestate"
	"github.com/nexascheduler/nexa/pkg/plugins"
	"github.com/nexascheduler/nexa/pkg/policy"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "NexaConfidential"

	// Label keys.
	labelConfidential = "nexa.io/confidential"
	labelTEE          = "nexa.io/tee"
	labelTEEType      = "nexa.io/tee-type"
	labelDiskEncrypt  = "nexa.io/disk-encrypted"
	labelPrivacy      = "nexa.io/privacy"

	// Label values.
	confidentialRequired = "required"
	teeNone              = "none"
	privacyHigh          = "high"
)

// Plugin implements confidential compute filtering and scoring based on TEE capabilities.
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

// Filter enforces confidential compute requirements:
//  1. Pods with nexa.io/confidential=required need TEE-capable nodes (nexa.io/tee != none and != "").
//  2. If policy.RequireEncryptedDisk, confidential pods need nexa.io/disk-encrypted=true.
//  3. If policy.RequireRuntimeClass is set, the pod must have a matching runtimeClassName.
//  4. If policy.RequireAttestation, node must have nexa.io/tee-attested=true.
//     4b. If AttestationMaxAgeHours > 0, attestation timestamp must be fresh.
//     4c. If AttestationTrustAnchor is set, node trust anchor must match.
//  5. If policy.RequireTEEForHigh, privacy=high pods also require TEE nodes.
func (p *Plugin) Filter(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) *fwk.Status {
	pol, status := p.GetPolicyOrFail()
	if status != nil {
		return status
	}

	if !pol.Confidential.Enabled {
		metrics.RecordFilter(Name, "accepted")
		return nil
	}

	node := nodeInfo.Node()
	isConfidentialPod := plugins.PodLabel(pod, labelConfidential) == confidentialRequired
	isHighPrivacy := plugins.PodLabel(pod, labelPrivacy) == privacyHigh

	// Determine if this pod needs TEE enforcement.
	needsTEE := isConfidentialPod || (isHighPrivacy && pol.Confidential.RequireTEEForHigh)
	if !needsTEE {
		metrics.RecordFilter(Name, "accepted")
		return nil
	}

	// Check 1: Node must have TEE capability.
	tee := node.Labels[labelTEE]
	if tee == "" || tee == teeNone {
		metrics.RecordFilter(Name, "rejected")
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s has no TEE capability (nexa.io/tee=%q); confidential workloads require a TEE-capable node (tdx, sev-snp)",
			node.Name, tee,
		))
	}

	// Check 2: Disk encryption if required by policy.
	if pol.Confidential.RequireEncryptedDisk && node.Labels[labelDiskEncrypt] != "true" {
		metrics.RecordFilter(Name, "rejected")
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
			"node %s does not have disk encryption (nexa.io/disk-encrypted != true); policy requires encrypted disk for confidential workloads",
			node.Name,
		))
	}

	// Check 3: RuntimeClass if required by policy (applies only to confidential=required pods).
	if isConfidentialPod && pol.Confidential.RequireRuntimeClass != "" {
		rc := podRuntimeClass(pod)
		if rc != pol.Confidential.RequireRuntimeClass {
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"pod runtimeClassName %q does not match required %q; set spec.runtimeClassName to %q for confidential workloads",
				rc, pol.Confidential.RequireRuntimeClass, pol.Confidential.RequireRuntimeClass,
			))
		}
	}

	// Check 4: Remote attestation if required by policy.
	if pol.Confidential.RequireAttestation {
		attested := node.Labels[nodestate.LabelTEEAttested]
		if attested != "true" {
			metrics.RecordFilter(Name, "rejected")
			return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
				"node %s has not passed remote attestation (nexa.io/tee-attested=%q); verify the node with the attestation service or disable requireAttestation in policy",
				node.Name, attested,
			))
		}

		// Check 4b: Attestation freshness.
		if pol.Confidential.AttestationMaxAgeHours > 0 {
			tsStr := node.Labels[nodestate.LabelTEEAttestationTime]
			if tsStr == "" {
				metrics.RecordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s is missing nexa.io/tee-attestation-time; attestation freshness policy requires a timestamp — re-run attestation verification",
					node.Name,
				))
			}
			ts, err := time.Parse(time.RFC3339, tsStr)
			if err != nil {
				metrics.RecordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s has malformed tee-attestation-time %q; expected RFC3339 format",
					node.Name, tsStr,
				))
			}
			maxAge := time.Duration(pol.Confidential.AttestationMaxAgeHours) * time.Hour
			if p.nowFunc().Sub(ts) > maxAge {
				metrics.RecordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s attestation expired (tee-attestation-time=%s, max age %dh); re-verify with attestation service or increase attestationMaxAgeHours",
					node.Name, tsStr, pol.Confidential.AttestationMaxAgeHours,
				))
			}
		}

		// Check 4c: Trust anchor match.
		if pol.Confidential.AttestationTrustAnchor != "" {
			nodeAnchor := node.Labels[nodestate.LabelTEETrustAnchor]
			if nodeAnchor != pol.Confidential.AttestationTrustAnchor {
				metrics.RecordFilter(Name, "rejected")
				return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf(
					"node %s trust anchor %q does not match policy requirement %q; verify the node with the correct attestation service",
					node.Name, nodeAnchor, pol.Confidential.AttestationTrustAnchor,
				))
			}
		}
	}

	metrics.RecordFilter(Name, "accepted")
	return nil
}

// Score ranks nodes by TEE suitability for confidential and high-privacy workloads.
//
//	Exact TEE type match:    framework.MaxNodeScore (100)
//	Any TEE (type mismatch): framework.MaxNodeScore / 2 (50)
//	No TEE or no preference: 0
func (p *Plugin) Score(_ context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	pol, err := p.Policy.GetPolicy()
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to read policy: %v", err))
	}

	if !pol.Confidential.Enabled {
		return 0, nil
	}

	isConfidentialPod := plugins.PodLabel(pod, labelConfidential) == confidentialRequired
	isHighPrivacy := plugins.PodLabel(pod, labelPrivacy) == privacyHigh
	needsScoring := isConfidentialPod || (isHighPrivacy && pol.Confidential.RequireTEEForHigh)
	if !needsScoring {
		return 0, nil
	}

	node := nodeInfo.Node()
	nodeTEE := node.Labels[labelTEE]
	if nodeTEE == "" || nodeTEE == teeNone {
		metrics.RecordScore(Name, 0)
		return 0, nil
	}

	// Pod specifies a preferred TEE type — exact match scores highest.
	wantTEE := plugins.PodLabel(pod, labelTEEType)
	if wantTEE == "" {
		wantTEE = pol.Confidential.DefaultTEEType
	}

	if wantTEE != "" && nodeTEE == wantTEE {
		metrics.RecordScore(Name, float64(framework.MaxNodeScore))
		return framework.MaxNodeScore, nil
	}

	// Node has TEE but not the preferred type (or no preference specified).
	if wantTEE != "" {
		// Has TEE but wrong type — partial score.
		metrics.RecordScore(Name, float64(framework.MaxNodeScore/2))
		return framework.MaxNodeScore / 2, nil
	}

	// No TEE type preference — any TEE is perfect.
	metrics.RecordScore(Name, float64(framework.MaxNodeScore))
	return framework.MaxNodeScore, nil
}

// NewWithProvider creates a Confidential plugin with the given policy provider.
// Used in tests to inject a StaticProvider.
// An optional nowFunc overrides time.Now for deterministic attestation freshness tests.
func NewWithProvider(provider policy.Provider, nowFunc ...func() time.Time) *Plugin {
	nf := time.Now
	if len(nowFunc) > 0 && nowFunc[0] != nil {
		nf = nowFunc[0]
	}
	return &Plugin{Base: plugins.Base{Policy: provider, PluginName: Name}, nowFunc: nf}
}

// New creates a new Confidential plugin with a composite policy provider (CRD + ConfigMap fallback).
func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	base, err := plugins.NewBase(Name, h)
	if err != nil {
		return nil, err
	}
	return &Plugin{Base: base, nowFunc: time.Now}, nil
}

// podRuntimeClass returns the pod's runtimeClassName, or "" if not set.
func podRuntimeClass(pod *v1.Pod) string {
	if pod.Spec.RuntimeClassName != nil {
		return *pod.Spec.RuntimeClassName
	}
	return ""
}
