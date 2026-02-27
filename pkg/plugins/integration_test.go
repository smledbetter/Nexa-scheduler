package plugins_test

import (
	"context"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fwk "k8s.io/kube-scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/plugins/privacy"
	"github.com/nexascheduler/nexa/pkg/plugins/region"

	"github.com/nexascheduler/nexa/pkg/policy"
	nt "github.com/nexascheduler/nexa/pkg/testing"
)

// bothPlugins returns region and privacy plugins sharing the same policy provider.
func bothPlugins(provider policy.Provider) (*region.Plugin, *privacy.Plugin) {
	return region.NewWithProvider(provider), privacy.NewWithProvider(provider)
}

func TestIntegration_BothPluginsAccept(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}}
	rp, pp := bothPlugins(provider)

	pod := nt.MakePod("test-pod", map[string]string{
		"nexa.io/region":  "us-west1",
		"nexa.io/privacy": "high",
	})
	node := nt.MakeNode("test-node", map[string]string{
		"nexa.io/region": "us-west1",
		"nexa.io/wiped":  "true",
	})
	nodeInfo := nt.MakeNodeInfo(node)

	regionStatus := rp.Filter(context.Background(), nil, pod, nodeInfo)
	if regionStatus != nil && !regionStatus.IsSuccess() {
		t.Errorf("Region Filter rejected, want accept: %v", regionStatus.Message())
	}

	privacyStatus := pp.Filter(context.Background(), nil, pod, nodeInfo)
	if privacyStatus != nil && !privacyStatus.IsSuccess() {
		t.Errorf("Privacy Filter rejected, want accept: %v", privacyStatus.Message())
	}
}

func TestIntegration_RegionRejectsPrivacyPasses(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}}
	rp, pp := bothPlugins(provider)

	pod := nt.MakePod("test-pod", map[string]string{
		"nexa.io/region":  "us-west1",
		"nexa.io/privacy": "high",
	})
	node := nt.MakeNode("test-node", map[string]string{
		"nexa.io/region": "eu-west1", // region mismatch
		"nexa.io/wiped":  "true",     // privacy passes
	})
	nodeInfo := nt.MakeNodeInfo(node)

	regionStatus := rp.Filter(context.Background(), nil, pod, nodeInfo)
	if regionStatus == nil || regionStatus.IsSuccess() {
		t.Error("Region Filter should reject mismatched region")
	}

	privacyStatus := pp.Filter(context.Background(), nil, pod, nodeInfo)
	if privacyStatus != nil && !privacyStatus.IsSuccess() {
		t.Errorf("Privacy Filter should accept wiped node regardless of region: %v", privacyStatus.Message())
	}
}

func TestIntegration_PrivacyRejectsRegionPasses(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}}
	rp, pp := bothPlugins(provider)

	pod := nt.MakePod("test-pod", map[string]string{
		"nexa.io/region":  "us-west1",
		"nexa.io/privacy": "high",
	})
	node := nt.MakeNode("test-node", map[string]string{
		"nexa.io/region": "us-west1", // region matches
		// missing wiped label — privacy rejects
	})
	nodeInfo := nt.MakeNodeInfo(node)

	regionStatus := rp.Filter(context.Background(), nil, pod, nodeInfo)
	if regionStatus != nil && !regionStatus.IsSuccess() {
		t.Errorf("Region Filter should accept matching region: %v", regionStatus.Message())
	}

	privacyStatus := pp.Filter(context.Background(), nil, pod, nodeInfo)
	if privacyStatus == nil || privacyStatus.IsSuccess() {
		t.Error("Privacy Filter should reject unwiped node")
	}
}

func TestIntegration_SharedPolicyError(t *testing.T) {
	provider := &policy.StaticProvider{Err: fwk.NewStatus(fwk.Error, "config unavailable").AsError()}
	rp, pp := bothPlugins(provider)

	pod := nt.MakePod("test-pod", nil)
	node := nt.MakeNode("test-node", nil)
	nodeInfo := nt.MakeNodeInfo(node)

	regionStatus := rp.Filter(context.Background(), nil, pod, nodeInfo)
	if regionStatus == nil || regionStatus.Code() != fwk.Error {
		t.Error("Region Filter should fail closed on policy error")
	}

	privacyStatus := pp.Filter(context.Background(), nil, pod, nodeInfo)
	if privacyStatus == nil || privacyStatus.Code() != fwk.Error {
		t.Error("Privacy Filter should fail closed on policy error")
	}
}

// notFoundProvider simulates a CRD provider where the CRD is not installed.
type notFoundProvider struct{}

func (n *notFoundProvider) GetPolicy() (*policy.Policy, error) {
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "nexa.io", Resource: "nexapolicies"}, "default")
}

// brokenCRDProvider simulates a CRD that exists but has invalid content.
type brokenCRDProvider struct{}

func (b *brokenCRDProvider) GetPolicy() (*policy.Policy, error) {
	return nil, fmt.Errorf("NexaPolicy validation failed: invalid field")
}

func TestIntegration_CompositeProvider_CRDPreferred(t *testing.T) {
	crdPolicy := &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true, DefaultRegion: "eu-west1"},
		Privacy: policy.PrivacyPolicy{Enabled: true, DefaultPrivacy: "high"},
	}
	cmPolicy := &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true, DefaultRegion: "us-east1"},
		Privacy: policy.PrivacyPolicy{Enabled: true, DefaultPrivacy: "standard"},
	}

	// CRD is available — should use CRD policy.
	composite := policy.NewCompositeProvider(
		&policy.StaticProvider{P: crdPolicy},
		&policy.StaticProvider{P: cmPolicy},
	)
	rp, pp := bothPlugins(composite)

	// Pod without region label should get CRD's defaultRegion (eu-west1).
	pod := nt.MakePod("test-pod", map[string]string{
		"nexa.io/privacy": "high",
	})
	euNode := nt.MakeNode("eu-node", map[string]string{
		"nexa.io/region": "eu-west1",
		"nexa.io/wiped":  "true",
	})
	usNode := nt.MakeNode("us-node", map[string]string{
		"nexa.io/region": "us-east1",
		"nexa.io/wiped":  "true",
	})

	// EU node should pass region filter (CRD default = eu-west1).
	status := rp.Filter(context.Background(), nil, pod, nt.MakeNodeInfo(euNode))
	if status != nil && !status.IsSuccess() {
		t.Errorf("expected EU node to pass with CRD policy, got: %v", status.Message())
	}

	// US node should be rejected by region filter.
	status = rp.Filter(context.Background(), nil, pod, nt.MakeNodeInfo(usNode))
	if status == nil || status.IsSuccess() {
		t.Error("expected US node to be rejected when CRD sets defaultRegion=eu-west1")
	}

	// Privacy should work with CRD policy.
	status = pp.Filter(context.Background(), nil, pod, nt.MakeNodeInfo(euNode))
	if status != nil && !status.IsSuccess() {
		t.Errorf("expected wiped EU node to pass privacy with CRD policy, got: %v", status.Message())
	}
}

func TestIntegration_CompositeProvider_FallsBackToConfigMap(t *testing.T) {
	cmPolicy := &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true, DefaultRegion: "us-east1"},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}

	// CRD not found — should fall back to ConfigMap.
	composite := policy.NewCompositeProvider(
		&notFoundProvider{},
		&policy.StaticProvider{P: cmPolicy},
	)
	rp, _ := bothPlugins(composite)

	pod := nt.MakePod("test-pod", nil)
	usNode := nt.MakeNode("us-node", map[string]string{
		"nexa.io/region": "us-east1",
	})

	// US node should pass (ConfigMap default = us-east1).
	status := rp.Filter(context.Background(), nil, pod, nt.MakeNodeInfo(usNode))
	if status != nil && !status.IsSuccess() {
		t.Errorf("expected US node to pass with ConfigMap fallback, got: %v", status.Message())
	}
}

func TestIntegration_CompositeProvider_BrokenCRDFailsClosed(t *testing.T) {
	cmPolicy := &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}

	// CRD exists but is malformed — should fail closed, NOT fall back.
	composite := policy.NewCompositeProvider(
		&brokenCRDProvider{},
		&policy.StaticProvider{P: cmPolicy},
	)
	rp, pp := bothPlugins(composite)

	pod := nt.MakePod("test-pod", nil)
	node := nt.MakeNode("test-node", nil)
	nodeInfo := nt.MakeNodeInfo(node)

	regionStatus := rp.Filter(context.Background(), nil, pod, nodeInfo)
	if regionStatus == nil || regionStatus.Code() != fwk.Error {
		t.Error("expected region filter to fail closed when CRD is broken")
	}

	privacyStatus := pp.Filter(context.Background(), nil, pod, nodeInfo)
	if privacyStatus == nil || privacyStatus.Code() != fwk.Error {
		t.Error("expected privacy filter to fail closed when CRD is broken")
	}
}

func TestIntegration_ScoresCompose(t *testing.T) {
	provider := &policy.StaticProvider{P: &policy.Policy{
		Region:  policy.RegionPolicy{Enabled: true},
		Privacy: policy.PrivacyPolicy{Enabled: true},
	}}
	rp, pp := bothPlugins(provider)

	pod := nt.MakePod("test-pod", map[string]string{
		"nexa.io/region":  "us-west1",
		"nexa.io/zone":    "us-west1-a",
		"nexa.io/privacy": "high",
	})
	node := nt.MakeNode("test-node", map[string]string{
		"nexa.io/region": "us-west1",
		"nexa.io/zone":   "us-west1-a",
		"nexa.io/wiped":  "true",
	})
	nodeInfo := nt.MakeNodeInfo(node)

	regionScore, regionStatus := rp.Score(context.Background(), nil, pod, nodeInfo)
	if regionStatus != nil && !regionStatus.IsSuccess() {
		t.Errorf("Region Score error: %v", regionStatus.Message())
	}
	if regionScore != 100 {
		t.Errorf("Region Score = %d, want 100 (zone match)", regionScore)
	}

	privacyScore, privacyStatus := pp.Score(context.Background(), nil, pod, nodeInfo)
	if privacyStatus != nil && !privacyStatus.IsSuccess() {
		t.Errorf("Privacy Score error: %v", privacyStatus.Message())
	}
	if privacyScore != 100 {
		t.Errorf("Privacy Score = %d, want 100 (wiped node)", privacyScore)
	}
}
