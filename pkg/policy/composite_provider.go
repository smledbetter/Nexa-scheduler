package policy

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

// CompositeProvider tries CRD first, falls back to ConfigMap.
// If the CRD exists but is malformed, it fails closed (no fallback).
// Fallback only occurs when the CRD resource is not found.
type CompositeProvider struct {
	crd       Provider
	configMap Provider
}

// NewCompositeProvider creates a provider that tries CRD first, then ConfigMap.
func NewCompositeProvider(crd, configMap Provider) *CompositeProvider {
	return &CompositeProvider{crd: crd, configMap: configMap}
}

// GetPolicy reads policy from CRD if available, otherwise from ConfigMap.
func (c *CompositeProvider) GetPolicy() (*Policy, error) {
	pol, err := c.crd.GetPolicy()
	if err == nil {
		klog.V(5).InfoS("policy loaded from NexaPolicy CRD")
		return pol, nil
	}

	if !apierrors.IsNotFound(err) {
		// CRD exists but is malformed — fail closed, do NOT fall back.
		return nil, fmt.Errorf("CRD policy error (not falling back to ConfigMap): %w", err)
	}

	// CRD not found — fall back to ConfigMap.
	klog.V(5).InfoS("NexaPolicy CRD not found, falling back to ConfigMap")
	return c.configMap.GetPolicy()
}
