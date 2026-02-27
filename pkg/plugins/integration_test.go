package plugins_test

import (
	"context"
	"testing"

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
