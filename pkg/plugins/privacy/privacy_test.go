package privacy

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Compile-time interface compliance checks.
var _ framework.FilterPlugin = (*Plugin)(nil)
var _ framework.ScorePlugin = (*Plugin)(nil)

func TestName(t *testing.T) {
	p := &Plugin{}
	if got := p.Name(); got != Name {
		t.Errorf("Name() = %q, want %q", got, Name)
	}
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
	}{
		{
			name: "pod with no privacy labels",
			pod:  &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod"}},
		},
		{
			name: "pod with high privacy",
			pod: &v1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name:   "private-pod",
				Labels: map[string]string{"nexa.io/privacy": "high"},
			}},
		},
		{
			name: "pod with org label",
			pod: &v1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name:   "org-pod",
				Labels: map[string]string{"nexa.io/org": "acme-corp"},
			}},
		},
	}

	p := &Plugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := p.Filter(context.Background(), nil, tt.pod, nil)
			if status != nil {
				t.Errorf("Filter() = %v, want nil (success)", status)
			}
		})
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name      string
		pod       *v1.Pod
		wantScore int64
	}{
		{
			name:      "pod with no labels scores 0",
			pod:       &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod"}},
			wantScore: 0,
		},
		{
			name: "pod with high privacy scores 0",
			pod: &v1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name:   "private-pod",
				Labels: map[string]string{"nexa.io/privacy": "high"},
			}},
			wantScore: 0,
		},
	}

	p := &Plugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, status := p.Score(context.Background(), nil, tt.pod, nil)
			if status != nil {
				t.Errorf("Score() status = %v, want nil (success)", status)
			}
			if score != tt.wantScore {
				t.Errorf("Score() = %d, want %d", score, tt.wantScore)
			}
		})
	}
}

func TestScoreExtensions(t *testing.T) {
	p := &Plugin{}
	if ext := p.ScoreExtensions(); ext != nil {
		t.Errorf("ScoreExtensions() = %v, want nil", ext)
	}
}

func TestNew(t *testing.T) {
	plugin, err := New(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if plugin == nil {
		t.Fatal("New() returned nil plugin")
	}
	if plugin.Name() != Name {
		t.Errorf("New() plugin name = %q, want %q", plugin.Name(), Name)
	}
}
