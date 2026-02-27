package policy

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	sharedProvider     Provider
	sharedProviderOnce sync.Once
	sharedProviderErr  error
)

// ProviderHandle is the subset of framework.Handle needed to create a policy provider.
// Defined here to avoid importing the framework package in the policy package.
type ProviderHandle interface {
	KubeConfig() *rest.Config
	SharedInformerFactory() informers.SharedInformerFactory
}

// NewCompositeProviderFromHandle creates a CompositeProvider that tries CRD first,
// then falls back to ConfigMap. All plugins share a single provider instance.
// The dynamic informer for CRDs is started and synced before returning.
func NewCompositeProviderFromHandle(h ProviderHandle) (Provider, error) {
	sharedProviderOnce.Do(func() {
		sharedProvider, sharedProviderErr = buildCompositeProvider(h)
	})
	return sharedProvider, sharedProviderErr
}

func buildCompositeProvider(h ProviderHandle) (Provider, error) {
	// Build ConfigMap provider from the shared informer factory.
	cmProvider := NewConfigMapProvider(h.SharedInformerFactory(), DefaultNamespace, DefaultConfigMapName)

	// Build dynamic client for CRD informer.
	dynClient, err := dynamic.NewForConfig(h.KubeConfig())
	if err != nil {
		klog.ErrorS(err, "failed to create dynamic client for NexaPolicy CRD, using ConfigMap only")
		return cmProvider, nil
	}

	// Create a filtered dynamic informer scoped to nexa-system namespace.
	dynFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		dynClient, 5*time.Minute, DefaultNamespace, nil,
	)

	// Start and sync the dynamic informer.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopCh := ctx.Done()

	dynFactory.ForResource(NexaPolicyGVR) // trigger informer creation
	dynFactory.Start(stopCh)
	synced := dynFactory.WaitForCacheSync(stopCh)

	if !synced[NexaPolicyGVR] {
		klog.InfoS("NexaPolicy CRD informer did not sync (CRD may not be installed), using ConfigMap with CRD fallback")
	}

	lister := dynFactory.ForResource(NexaPolicyGVR).Lister().ByNamespace(DefaultNamespace)
	crdProvider := NewCRDProvider(lister, DefaultCRDName)

	return NewCompositeProvider(crdProvider, cmProvider), nil
}

// ResetSharedProvider resets the shared provider singleton. Used in tests only.
func ResetSharedProvider() {
	sharedProviderOnce = sync.Once{}
	sharedProvider = nil
	sharedProviderErr = nil
}
