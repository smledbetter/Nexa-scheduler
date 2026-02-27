package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStaticProvider(t *testing.T) {
	t.Run("returns policy", func(t *testing.T) {
		pol := &Policy{Region: RegionPolicy{Enabled: true}}
		provider := &StaticProvider{P: pol}

		got, err := provider.GetPolicy()
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}
		if !got.Region.Enabled {
			t.Error("Region.Enabled = false, want true")
		}
	})

	t.Run("returns error", func(t *testing.T) {
		provider := &StaticProvider{Err: errors.New("config unavailable")}

		_, err := provider.GetPolicy()
		if err == nil {
			t.Fatal("GetPolicy() returned nil error, want error")
		}
		if err.Error() != "config unavailable" {
			t.Errorf("GetPolicy() error = %q, want %q", err.Error(), "config unavailable")
		}
	})

	t.Run("returns nil policy and nil error", func(t *testing.T) {
		provider := &StaticProvider{}

		got, err := provider.GetPolicy()
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}
		if got != nil {
			t.Errorf("GetPolicy() = %v, want nil", got)
		}
	})
}

// newTestProvider creates a ConfigMapProvider backed by a fake clientset,
// starts the informer, and waits for cache sync. The provider must be created
// before starting the factory so the ConfigMap informer gets registered.
func newTestProvider(t *testing.T, objects ...corev1.ConfigMap) *ConfigMapProvider {
	t.Helper()
	client := fake.NewSimpleClientset()
	for i := range objects {
		_, err := client.CoreV1().ConfigMaps(objects[i].Namespace).Create(
			context.Background(), &objects[i], metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create ConfigMap: %v", err)
		}
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	provider := NewConfigMapProvider(factory, DefaultNamespace, DefaultConfigMapName)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	return provider
}

func TestConfigMapProvider(t *testing.T) {
	t.Run("returns parsed policy from valid ConfigMap", func(t *testing.T) {
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DefaultConfigMapName,
				Namespace: DefaultNamespace,
			},
			Data: map[string]string{
				ConfigMapKey: `{"regionPolicy":{"enabled":true,"defaultRegion":"us-west1"},"privacyPolicy":{"enabled":true,"defaultPrivacy":"standard"}}`,
			},
		}
		provider := newTestProvider(t, cm)

		got, err := provider.GetPolicy()
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}
		if !got.Region.Enabled {
			t.Error("Region.Enabled = false, want true")
		}
		if got.Region.DefaultRegion != "us-west1" {
			t.Errorf("Region.DefaultRegion = %q, want %q", got.Region.DefaultRegion, "us-west1")
		}
		if !got.Privacy.Enabled {
			t.Error("Privacy.Enabled = false, want true")
		}
		if got.Privacy.DefaultPrivacy != "standard" {
			t.Errorf("Privacy.DefaultPrivacy = %q, want %q", got.Privacy.DefaultPrivacy, "standard")
		}
	})

	t.Run("returns error for missing ConfigMap", func(t *testing.T) {
		provider := newTestProvider(t)

		_, err := provider.GetPolicy()
		if err == nil {
			t.Fatal("GetPolicy() returned nil error, want error")
		}
		if !strings.Contains(err.Error(), "failed to get ConfigMap") {
			t.Errorf("GetPolicy() error = %q, want substring %q", err.Error(), "failed to get ConfigMap")
		}
	})

	t.Run("returns error for malformed JSON", func(t *testing.T) {
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DefaultConfigMapName,
				Namespace: DefaultNamespace,
			},
			Data: map[string]string{
				ConfigMapKey: `{not valid json}`,
			},
		}
		provider := newTestProvider(t, cm)

		_, err := provider.GetPolicy()
		if err == nil {
			t.Fatal("GetPolicy() returned nil error, want parse error")
		}
		if !strings.Contains(err.Error(), "failed to parse") {
			t.Errorf("GetPolicy() error = %q, want substring %q", err.Error(), "failed to parse")
		}
	})

	t.Run("returns error for missing policy.json key", func(t *testing.T) {
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DefaultConfigMapName,
				Namespace: DefaultNamespace,
			},
			Data: map[string]string{
				"wrong-key": `{"regionPolicy":{"enabled":true}}`,
			},
		}
		provider := newTestProvider(t, cm)

		_, err := provider.GetPolicy()
		if err == nil {
			t.Fatal("GetPolicy() returned nil error, want error")
		}
		if !strings.Contains(err.Error(), "missing required key") {
			t.Errorf("GetPolicy() error = %q, want substring %q", err.Error(), "missing required key")
		}
	})
}
