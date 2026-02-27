package policy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// fakeProviderHandle implements ProviderHandle for testing without a real cluster.
type fakeProviderHandle struct {
	kubeConfig *rest.Config
	factory    informers.SharedInformerFactory
}

func (f *fakeProviderHandle) KubeConfig() *rest.Config {
	return f.kubeConfig
}

func (f *fakeProviderHandle) SharedInformerFactory() informers.SharedInformerFactory {
	return f.factory
}

// newFakeAPIServer creates an httptest server that responds to dynamic API list/watch
// requests with empty lists, allowing the dynamic informer to sync immediately.
func newFakeAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := &unstructured.UnstructuredList{
			Object: map[string]interface{}{
				"apiVersion": CRDGroup + "/" + CRDVersion,
				"kind":       "NexaPolicyList",
				"metadata": map[string]interface{}{
					"resourceVersion": "1",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFakeProviderHandle(t *testing.T) *fakeProviderHandle {
	t.Helper()
	srv := newFakeAPIServer(t)
	return &fakeProviderHandle{
		kubeConfig: &rest.Config{
			Host: srv.URL,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
		},
		factory: informers.NewSharedInformerFactory(fake.NewSimpleClientset(), 0),
	}
}

func TestBuildCompositeProvider(t *testing.T) {
	t.Run("returns non-nil provider", func(t *testing.T) {
		defer ResetSharedProvider()
		h := newFakeProviderHandle(t)
		provider, err := buildCompositeProvider(h)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
		// Should be a CompositeProvider wrapping CRD + ConfigMap.
		if _, ok := provider.(*CompositeProvider); !ok {
			t.Errorf("expected *CompositeProvider, got %T", provider)
		}
	})

	t.Run("broken KubeConfig falls back to ConfigMap-only", func(t *testing.T) {
		defer ResetSharedProvider()
		h := &fakeProviderHandle{
			kubeConfig: &rest.Config{Host: "://invalid"},
			factory:    informers.NewSharedInformerFactory(fake.NewSimpleClientset(), 0),
		}
		provider, err := buildCompositeProvider(h)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if provider == nil {
			t.Fatal("expected non-nil provider (ConfigMap fallback)")
		}
		if _, ok := provider.(*ConfigMapProvider); !ok {
			t.Errorf("expected *ConfigMapProvider fallback, got %T", provider)
		}
	})
}

func TestNewCompositeProviderFromHandle_Singleton(t *testing.T) {
	defer ResetSharedProvider()
	h := newFakeProviderHandle(t)

	p1, err1 := NewCompositeProviderFromHandle(h)
	p2, err2 := NewCompositeProviderFromHandle(h)

	if err1 != nil {
		t.Fatalf("first call error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("second call error: %v", err2)
	}
	if p1 != p2 {
		t.Error("expected same provider instance from singleton, got different pointers")
	}
}

func TestResetSharedProvider(t *testing.T) {
	defer ResetSharedProvider()
	h := newFakeProviderHandle(t)

	p1, err := NewCompositeProviderFromHandle(h)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	ResetSharedProvider()

	p2, err := NewCompositeProviderFromHandle(h)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if p1 == p2 {
		t.Error("expected different provider instances after reset, got same pointer")
	}
}
