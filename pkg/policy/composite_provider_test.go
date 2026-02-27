package policy

import (
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// notFoundProvider simulates a CRD provider where the resource does not exist.
type notFoundProvider struct{}

func (n *notFoundProvider) GetPolicy() (*Policy, error) {
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: CRDGroup, Resource: CRDResource}, DefaultCRDName)
}

// errorProvider simulates a provider that returns a non-NotFound error (malformed CRD).
type errorProvider struct {
	err error
}

func (e *errorProvider) GetPolicy() (*Policy, error) {
	return nil, e.err
}

func TestCompositeProvider_GetPolicy(t *testing.T) {
	crdPolicy := &Policy{
		Region:  RegionPolicy{Enabled: true, DefaultRegion: "eu-west1"},
		Privacy: PrivacyPolicy{Enabled: true, DefaultPrivacy: "high"},
	}
	cmPolicy := &Policy{
		Region:  RegionPolicy{Enabled: true, DefaultRegion: "us-east1"},
		Privacy: PrivacyPolicy{Enabled: true, DefaultPrivacy: "standard"},
	}

	tests := []struct {
		name      string
		crd       Provider
		configMap Provider
		wantErr   string
		wantCheck func(t *testing.T, pol *Policy)
	}{
		{
			name:      "CRD available — returns CRD policy",
			crd:       &StaticProvider{P: crdPolicy},
			configMap: &StaticProvider{P: cmPolicy},
			wantCheck: func(t *testing.T, pol *Policy) {
				t.Helper()
				if pol.Region.DefaultRegion != "eu-west1" {
					t.Errorf("expected CRD region eu-west1, got %q", pol.Region.DefaultRegion)
				}
			},
		},
		{
			name:      "CRD not found — falls back to ConfigMap",
			crd:       &notFoundProvider{},
			configMap: &StaticProvider{P: cmPolicy},
			wantCheck: func(t *testing.T, pol *Policy) {
				t.Helper()
				if pol.Region.DefaultRegion != "us-east1" {
					t.Errorf("expected ConfigMap region us-east1, got %q", pol.Region.DefaultRegion)
				}
			},
		},
		{
			name:      "CRD malformed — fail closed, no fallback",
			crd:       &errorProvider{err: fmt.Errorf("NexaPolicy validation failed: invalid field")},
			configMap: &StaticProvider{P: cmPolicy},
			wantErr:   "not falling back",
		},
		{
			name:      "CRD not found AND ConfigMap missing — returns ConfigMap error",
			crd:       &notFoundProvider{},
			configMap: &StaticProvider{Err: fmt.Errorf("ConfigMap not found")},
			wantErr:   "ConfigMap not found",
		},
		{
			name:      "CRD not found AND ConfigMap has error — returns ConfigMap error",
			crd:       &notFoundProvider{},
			configMap: &errorProvider{err: fmt.Errorf("failed to parse policy.json")},
			wantErr:   "failed to parse policy.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewCompositeProvider(tt.crd, tt.configMap)

			pol, err := provider.GetPolicy()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantCheck != nil {
				tt.wantCheck(t, pol)
			}
		})
	}
}
