package policy

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func makeNexaPolicy(name, namespace string, spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": CRDGroup + "/" + CRDVersion,
			"kind":       "NexaPolicy",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

func newTestCRDProvider(t *testing.T, namespace string, objects ...*unstructured.Unstructured) *CRDProvider {
	t.Helper()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, obj := range objects {
		if err := indexer.Add(obj); err != nil {
			t.Fatalf("failed to add object to indexer: %v", err)
		}
	}
	lister := cache.NewGenericLister(indexer, schema.GroupResource{Group: CRDGroup, Resource: CRDResource}).ByNamespace(namespace)
	return NewCRDProvider(lister, DefaultCRDName)
}

func TestCRDProvider_GetPolicy(t *testing.T) {
	tests := []struct {
		name      string
		objects   []*unstructured.Unstructured
		wantErr   string
		wantCheck func(t *testing.T, pol *Policy)
	}{
		{
			name: "valid CRD returns parsed policy",
			objects: []*unstructured.Unstructured{
				makeNexaPolicy(DefaultCRDName, DefaultNamespace, map[string]interface{}{
					"regionPolicy": map[string]interface{}{
						"enabled":       true,
						"defaultRegion": "us-west1",
					},
					"privacyPolicy": map[string]interface{}{
						"enabled":        true,
						"defaultPrivacy": "standard",
					},
				}),
			},
			wantCheck: func(t *testing.T, pol *Policy) {
				t.Helper()
				if !pol.Region.Enabled {
					t.Error("expected Region.Enabled=true")
				}
				if pol.Region.DefaultRegion != "us-west1" {
					t.Errorf("expected DefaultRegion=us-west1, got %q", pol.Region.DefaultRegion)
				}
				if !pol.Privacy.Enabled {
					t.Error("expected Privacy.Enabled=true")
				}
				if pol.Privacy.DefaultPrivacy != "standard" {
					t.Errorf("expected DefaultPrivacy=standard, got %q", pol.Privacy.DefaultPrivacy)
				}
			},
		},
		{
			name:    "missing CRD returns NotFound error",
			objects: nil,
			wantErr: "failed to get NexaPolicy",
		},
		{
			name: "CRD with no spec field",
			objects: []*unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": CRDGroup + "/" + CRDVersion,
						"kind":       "NexaPolicy",
						"metadata": map[string]interface{}{
							"name":      DefaultCRDName,
							"namespace": DefaultNamespace,
						},
					},
				},
			},
			wantErr: "no .spec field",
		},
		{
			name: "CRD with invalid privacy level",
			objects: []*unstructured.Unstructured{
				makeNexaPolicy(DefaultCRDName, DefaultNamespace, map[string]interface{}{
					"regionPolicy": map[string]interface{}{
						"enabled": true,
					},
					"privacyPolicy": map[string]interface{}{
						"enabled":        true,
						"defaultPrivacy": "invalid-level",
					},
				}),
			},
			wantErr: "validation failed",
		},
		{
			name: "CRD with empty spec returns zero-value policy",
			objects: []*unstructured.Unstructured{
				makeNexaPolicy(DefaultCRDName, DefaultNamespace, map[string]interface{}{}),
			},
			wantCheck: func(t *testing.T, pol *Policy) {
				t.Helper()
				if pol.Region.Enabled {
					t.Error("expected Region.Enabled=false for empty spec")
				}
				if pol.Privacy.Enabled {
					t.Error("expected Privacy.Enabled=false for empty spec")
				}
			},
		},
		{
			name: "CRD with strict org isolation",
			objects: []*unstructured.Unstructured{
				makeNexaPolicy(DefaultCRDName, DefaultNamespace, map[string]interface{}{
					"regionPolicy": map[string]interface{}{
						"enabled": false,
					},
					"privacyPolicy": map[string]interface{}{
						"enabled":            true,
						"defaultPrivacy":     "high",
						"strictOrgIsolation": true,
					},
				}),
			},
			wantCheck: func(t *testing.T, pol *Policy) {
				t.Helper()
				if !pol.Privacy.StrictOrgIsolation {
					t.Error("expected StrictOrgIsolation=true")
				}
				if pol.Privacy.DefaultPrivacy != "high" {
					t.Errorf("expected DefaultPrivacy=high, got %q", pol.Privacy.DefaultPrivacy)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newTestCRDProvider(t, DefaultNamespace, tt.objects...)

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

func TestParseCRDObject(t *testing.T) {
	tests := []struct {
		name    string
		obj     runtime.Object
		wantErr string
	}{
		{
			name:    "non-unstructured object",
			obj:     &runtime.Unknown{},
			wantErr: "expected *unstructured.Unstructured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCRDObject(tt.obj)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
