package policy

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

const (
	// CRDGroup is the API group for NexaPolicy resources.
	CRDGroup = "nexa.io"
	// CRDVersion is the API version for NexaPolicy resources.
	CRDVersion = "v1alpha1"
	// CRDResource is the plural resource name for NexaPolicy.
	CRDResource = "nexapolicies"
	// DefaultCRDName is the expected NexaPolicy resource name.
	DefaultCRDName = "default"
)

// NexaPolicyGVR is the GroupVersionResource for NexaPolicy custom resources.
var NexaPolicyGVR = schema.GroupVersionResource{
	Group:    CRDGroup,
	Version:  CRDVersion,
	Resource: CRDResource,
}

// CRDProvider reads the scheduling policy from a NexaPolicy custom resource
// via a dynamic informer cache. Each call reads from the local cache (no network call).
type CRDProvider struct {
	lister cache.GenericNamespaceLister
	name   string
}

// NewCRDProvider creates a Provider that reads from a NexaPolicy CRD in the given namespace.
// The lister must come from a started and synced dynamic informer.
func NewCRDProvider(lister cache.GenericNamespaceLister, name string) *CRDProvider {
	return &CRDProvider{lister: lister, name: name}
}

// GetPolicy reads the NexaPolicy CRD from the informer cache, extracts .spec,
// unmarshals it into a Policy, and validates it. Returns an error if the resource
// is missing, malformed, or invalid (fail closed).
func (p *CRDProvider) GetPolicy() (*Policy, error) {
	obj, err := p.lister.Get(p.name)
	if err != nil {
		return nil, fmt.Errorf("failed to get NexaPolicy %s: %w", p.name, err)
	}
	return parseCRDObject(obj)
}

// parseCRDObject extracts .spec from an unstructured object and parses it into a Policy.
func parseCRDObject(obj runtime.Object) (*Policy, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected *unstructured.Unstructured, got %T", obj)
	}

	spec, ok := u.Object["spec"]
	if !ok {
		return nil, fmt.Errorf("NexaPolicy %s has no .spec field", u.GetName())
	}

	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal NexaPolicy spec: %w", err)
	}

	var pol Policy
	if err := json.Unmarshal(specBytes, &pol); err != nil {
		return nil, fmt.Errorf("failed to unmarshal NexaPolicy spec: %w", err)
	}

	if err := Validate(&pol); err != nil {
		return nil, fmt.Errorf("NexaPolicy validation failed: %w", err)
	}

	return &pol, nil
}
