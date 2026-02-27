package nodestate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// Controller watches pod lifecycle events and updates node labels
// to reflect workload history and cleanliness state.
type Controller struct {
	kubeClient kubernetes.Interface
	podLister  corev1listers.PodLister
	nodeLister corev1listers.NodeLister
	queue      workqueue.TypedRateLimitingInterface[string]
	hasSynced  []cache.InformerSynced
}

// NewController creates a Node State Controller that watches pods and updates node labels.
func NewController(kubeClient kubernetes.Interface, factory informers.SharedInformerFactory) *Controller {
	podInformer := factory.Core().V1().Pods()
	nodeInformer := factory.Core().V1().Nodes()

	c := &Controller{
		kubeClient: kubeClient,
		podLister:  podInformer.Lister(),
		nodeLister: nodeInformer.Lister(),
		queue:      workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		hasSynced: []cache.InformerSynced{
			podInformer.Informer().HasSynced,
			nodeInformer.Informer().HasSynced,
		},
	}

	_, _ = podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok1 := oldObj.(*v1.Pod)
			newPod, ok2 := newObj.(*v1.Pod)
			if !ok1 || !ok2 {
				return
			}
			if isPodTerminated(newPod) && !isPodTerminated(oldPod) && newPod.Spec.NodeName != "" {
				c.queue.Add(newPod.Spec.NodeName)
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*v1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				pod, ok = tombstone.Obj.(*v1.Pod)
				if !ok {
					return
				}
			}
			if pod.Spec.NodeName != "" {
				c.queue.Add(pod.Spec.NodeName)
			}
		},
	})

	return c
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.queue.ShutDown()

	klog.InfoS("starting node state controller")

	if !cache.WaitForCacheSync(ctx.Done(), c.hasSynced...) {
		return fmt.Errorf("failed to sync informer caches")
	}
	klog.InfoS("informer caches synced")

	for i := 0; i < workers; i++ {
		go c.runWorker(ctx)
	}

	<-ctx.Done()
	klog.InfoS("shutting down node state controller")
	return nil
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	nodeName, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(nodeName)

	err := c.reconcileNode(ctx, nodeName)
	if err != nil {
		if c.queue.NumRequeues(nodeName) < 5 {
			klog.ErrorS(err, "reconcile failed, requeuing", "node", nodeName)
			c.queue.AddRateLimited(nodeName)
			return true
		}
		klog.ErrorS(err, "reconcile failed after max retries, dropping", "node", nodeName)
	}

	c.queue.Forget(nodeName)
	return true
}

func (c *Controller) reconcileNode(ctx context.Context, nodeName string) error {
	node, err := c.nodeLister.Get(nodeName)
	if err != nil {
		return fmt.Errorf("get node %s: %w", nodeName, err)
	}

	// Determine the org from the most recently terminated pod on this node.
	pods, err := c.podLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	var lastOrg string
	var latestTermination time.Time
	for _, pod := range pods {
		if pod.Spec.NodeName != nodeName || !isPodTerminated(pod) {
			continue
		}
		org := pod.Labels[LabelOrg]
		if org == "" {
			continue
		}
		termTime := podTerminationTime(pod)
		if termTime.After(latestTermination) {
			latestTermination = termTime
			lastOrg = org
		}
	}

	// Build the label patch.
	patchLabels := make(map[string]interface{})
	changed := false

	if lastOrg != "" && node.Labels[LabelLastWorkloadOrg] != lastOrg {
		patchLabels[LabelLastWorkloadOrg] = lastOrg
		changed = true
		klog.InfoS("updating last workload org", "node", nodeName, "org", lastOrg)
	}

	// If wipe-on-complete is set and a pod just finished, mark the node as dirty.
	if node.Labels[LabelWipeOnComplete] == "true" && node.Labels[LabelWiped] == "true" && !latestTermination.IsZero() {
		patchLabels[LabelWiped] = "false"
		patchLabels[LabelWipeTimestamp] = nil // remove stale timestamp
		changed = true
		klog.InfoS("marking node as dirty (wipe-on-complete)", "node", nodeName)
	}

	if !changed {
		return nil
	}

	return c.patchNodeLabels(ctx, nodeName, patchLabels)
}

func (c *Controller) patchNodeLabels(ctx context.Context, nodeName string, nodeLabels map[string]interface{}) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": nodeLabels,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	_, err = c.kubeClient.CoreV1().Nodes().Patch(
		ctx, nodeName, types.MergePatchType, patchBytes, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch node %s labels: %w", nodeName, err)
	}

	klog.InfoS("patched node labels", "node", nodeName, "labels", nodeLabels)
	return nil
}

func isPodTerminated(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed
}

func podTerminationTime(pod *v1.Pod) time.Time {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil && !cs.State.Terminated.FinishedAt.IsZero() {
			return cs.State.Terminated.FinishedAt.Time
		}
	}
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime.Time
	}
	return time.Time{}
}
