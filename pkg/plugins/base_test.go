package plugins_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwk "k8s.io/kube-scheduler/framework"

	"github.com/nexascheduler/nexa/pkg/plugins"
	"github.com/nexascheduler/nexa/pkg/policy"
)

func TestGetPolicyOrFail_Success(t *testing.T) {
	base := plugins.Base{
		Policy:     &policy.StaticProvider{P: &policy.Policy{}},
		PluginName: "TestPlugin",
	}
	pol, status := base.GetPolicyOrFail()
	if status != nil {
		t.Fatalf("expected success, got status: %v", status.Message())
	}
	if pol == nil {
		t.Fatal("expected non-nil policy")
	}
}

func TestGetPolicyOrFail_Error(t *testing.T) {
	base := plugins.Base{
		Policy:     &policy.StaticProvider{Err: fwk.NewStatus(fwk.Error, "broken").AsError()},
		PluginName: "TestPlugin",
	}
	pol, status := base.GetPolicyOrFail()
	if pol != nil {
		t.Fatal("expected nil policy on error")
	}
	if status == nil || status.Code() != fwk.Error {
		t.Fatal("expected Error status")
	}
}

func TestScoreExtensions(t *testing.T) {
	base := plugins.Base{}
	if base.ScoreExtensions() != nil {
		t.Error("expected nil ScoreExtensions")
	}
}

func TestPodLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		key    string
		want   string
	}{
		{"present", map[string]string{"k": "v"}, "k", "v"},
		{"absent", map[string]string{"k": "v"}, "other", ""},
		{"nil labels", nil, "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			if got := plugins.PodLabel(pod, tt.key); got != tt.want {
				t.Errorf("PodLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPodLabelWithDefault(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		key        string
		defaultVal string
		want       string
	}{
		{"present", map[string]string{"k": "v"}, "k", "def", "v"},
		{"absent uses default", map[string]string{}, "k", "def", "def"},
		{"empty uses default", map[string]string{"k": ""}, "k", "def", "def"},
		{"nil labels uses default", nil, "k", "def", "def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			if got := plugins.PodLabelWithDefault(pod, tt.key, tt.defaultVal); got != tt.want {
				t.Errorf("PodLabelWithDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}
