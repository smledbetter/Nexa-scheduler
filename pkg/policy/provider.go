package policy

import (
	"fmt"

	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const (
	// DefaultNamespace is the namespace where the policy ConfigMap is expected.
	DefaultNamespace = "nexa-system"
	// DefaultConfigMapName is the name of the policy ConfigMap.
	DefaultConfigMapName = "nexa-scheduler-config"
)

// Provider is the interface for accessing the current scheduling policy.
// Implementations may read from a ConfigMap, a static value, or any other source.
type Provider interface {
	GetPolicy() (*Policy, error)
}

// ConfigMapProvider reads the policy from a ConfigMap via an informer lister.
// Each call reads from the local informer cache (no network call).
type ConfigMapProvider struct {
	lister corev1listers.ConfigMapNamespaceLister
	name   string
}

// NewConfigMapProvider creates a Provider that reads from a namespaced ConfigMap.
// The factory's informer cache must be synced before calling GetPolicy.
func NewConfigMapProvider(factory informers.SharedInformerFactory, namespace, name string) *ConfigMapProvider {
	return &ConfigMapProvider{
		lister: factory.Core().V1().ConfigMaps().Lister().ConfigMaps(namespace),
		name:   name,
	}
}

// GetPolicy reads the ConfigMap from the informer cache, parses it, and validates it.
// Returns an error if the ConfigMap is missing, malformed, or invalid (fail closed).
func (p *ConfigMapProvider) GetPolicy() (*Policy, error) {
	cm, err := p.lister.Get(p.name)
	if err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap %s: %w", p.name, err)
	}
	return Parse(cm.Data)
}

// StaticProvider returns a fixed policy. Used in tests.
type StaticProvider struct {
	P   *Policy
	Err error
}

// GetPolicy returns the static policy or error.
func (s *StaticProvider) GetPolicy() (*Policy, error) {
	return s.P, s.Err
}
