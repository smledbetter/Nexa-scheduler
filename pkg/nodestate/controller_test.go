package nodestate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func makeTestNode(name string, labels map[string]string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func makeTestPod(name, nodeName, org string, phase v1.PodPhase, finishedAt time.Time) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
		Status: v1.PodStatus{
			Phase: phase,
		},
	}
	if org != "" {
		pod.Labels = map[string]string{LabelOrg: org}
	}
	if !finishedAt.IsZero() {
		pod.Status.ContainerStatuses = []v1.ContainerStatus{
			{
				State: v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						FinishedAt: metav1.NewTime(finishedAt),
					},
				},
			},
		}
	}
	return pod
}

func TestReconcileNode(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name                 string
		node                 *v1.Node
		pods                 []*v1.Pod
		wantLabels           map[string]string
		wantNoPatches        bool
		wantTimestampCleared bool
	}{
		{
			name: "pod completes — updates last workload org",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "acme",
			},
		},
		{
			name: "pod fails — updates last workload org",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "globex", v1.PodFailed, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "globex",
			},
		},
		{
			name: "wipe-on-complete triggers dirty marking",
			node: makeTestNode("node1", map[string]string{
				LabelWipeOnComplete: "true",
				LabelWiped:          "true",
			}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "acme",
				LabelWiped:           "false",
			},
		},
		{
			name: "pod without org label — no org update",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "", v1.PodSucceeded, now),
			},
			wantNoPatches: true,
		},
		{
			name: "running pod — no changes",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodRunning, time.Time{}),
			},
			wantNoPatches: true,
		},
		{
			name: "pod on different node — no changes",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node2", "acme", v1.PodSucceeded, now),
			},
			wantNoPatches: true,
		},
		{
			name: "label already matches — no patch",
			node: makeTestNode("node1", map[string]string{
				LabelLastWorkloadOrg: "acme",
			}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now),
			},
			wantNoPatches: true,
		},
		{
			name: "multiple pods — uses most recent org",
			node: makeTestNode("node1", map[string]string{}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now.Add(-time.Hour)),
				makeTestPod("pod2", "node1", "globex", v1.PodSucceeded, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "globex",
			},
		},
		{
			name: "wipe-on-complete clears wipe-timestamp",
			node: makeTestNode("node1", map[string]string{
				LabelWipeOnComplete: "true",
				LabelWiped:          "true",
				LabelWipeTimestamp:  "2026-03-01T10:00:00Z",
			}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "acme",
				LabelWiped:           "false",
			},
			wantTimestampCleared: true,
		},
		{
			name: "wipe-on-complete but already dirty — no wipe change",
			node: makeTestNode("node1", map[string]string{
				LabelWipeOnComplete: "true",
				LabelWiped:          "false",
			}),
			pods: []*v1.Pod{
				makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now),
			},
			wantLabels: map[string]string{
				LabelLastWorkloadOrg: "acme",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.node}
			for _, pod := range tt.pods {
				objects = append(objects, pod)
			}

			client := fake.NewSimpleClientset(objects...)
			factory := informers.NewSharedInformerFactory(client, 0)

			controller := NewController(client, factory)

			stopCh := make(chan struct{})
			factory.Start(stopCh)
			factory.WaitForCacheSync(stopCh)

			err := controller.reconcileNode(context.Background(), tt.node.Name)
			close(stopCh)
			if err != nil {
				t.Fatalf("reconcileNode returned error: %v", err)
			}

			// Check patch actions.
			actions := client.Actions()
			var patchActions []runtime.Object
			for _, a := range actions {
				if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
					patchActions = append(patchActions, nil) // count patches
				}
			}

			if tt.wantNoPatches {
				if len(patchActions) > 0 {
					t.Fatalf("expected no patches, got %d", len(patchActions))
				}
				return
			}

			if len(patchActions) == 0 {
				t.Fatal("expected a patch action, got none")
			}

			// Verify the patch content.
			for _, a := range actions {
				if a.GetVerb() != "patch" || a.GetResource().Resource != "nodes" {
					continue
				}
				patchAction := a.(patchGetter)
				var patch map[string]interface{}
				if err := json.Unmarshal(patchAction.GetPatch(), &patch); err != nil {
					t.Fatalf("unmarshal patch: %v", err)
				}
				metadata, _ := patch["metadata"].(map[string]interface{})
				gotLabels, _ := metadata["labels"].(map[string]interface{})

				for k, want := range tt.wantLabels {
					got, ok := gotLabels[k]
					if !ok {
						t.Errorf("missing label %q in patch", k)
						continue
					}
					if got != want {
						t.Errorf("label %q = %v, want %v", k, got, want)
					}
				}
				if tt.wantTimestampCleared {
					val, exists := gotLabels[LabelWipeTimestamp]
					if !exists {
						t.Error("expected wipe-timestamp in patch (set to null for removal)")
					} else if val != nil {
						t.Errorf("expected wipe-timestamp=null (removal), got %v", val)
					}
				}
			}
		})
	}
}

// patchGetter is the interface for accessing patch data from a fake client action.
type patchGetter interface {
	GetPatch() []byte
}

func TestProcessNextWorkItem(t *testing.T) {
	now := time.Now()

	t.Run("successful reconcile — item forgotten", func(t *testing.T) {
		node := makeTestNode("node1", map[string]string{})
		pod := makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, now)

		client := fake.NewSimpleClientset(node, pod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		controller.queue.Add("node1")
		result := controller.processNextWorkItem(context.Background())
		close(stopCh)

		if !result {
			t.Fatal("expected processNextWorkItem to return true")
		}
		if controller.queue.Len() != 0 {
			t.Errorf("expected empty queue after successful reconcile, got %d items", controller.queue.Len())
		}
	})

	t.Run("failed reconcile — item requeued", func(t *testing.T) {
		// No node "missing-node" in the fake client → reconcileNode will fail.
		client := fake.NewSimpleClientset()
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		controller.queue.Add("missing-node")
		result := controller.processNextWorkItem(context.Background())
		close(stopCh)

		if !result {
			t.Fatal("expected processNextWorkItem to return true (requeue, not shutdown)")
		}
		// AddRateLimited may have a delay, so check NumRequeues instead of Len().
		if controller.queue.NumRequeues("missing-node") == 0 {
			t.Error("expected item to have requeues after failed reconcile")
		}
	})

	t.Run("max retries exceeded — item dropped", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		// Simulate 5 prior failures by calling AddRateLimited repeatedly.
		for i := 0; i < 5; i++ {
			controller.queue.AddRateLimited("missing-node")
			// Drain and mark done so requeue count increments.
			item, _ := controller.queue.Get()
			controller.queue.Done(item)
		}

		// Now add and process — should hit max retries and drop.
		controller.queue.Add("missing-node")
		controller.processNextWorkItem(context.Background())
		close(stopCh)

		// After dropping, NumRequeues should be reset (Forget was called).
		if controller.queue.NumRequeues("missing-node") != 0 {
			t.Errorf("expected NumRequeues=0 after drop, got %d", controller.queue.NumRequeues("missing-node"))
		}
	})

	t.Run("shutdown — returns false", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		controller.queue.ShutDown()
		result := controller.processNextWorkItem(context.Background())

		if result {
			t.Fatal("expected processNextWorkItem to return false on shutdown")
		}
	})
}

// drainQueue returns all items currently in the queue (non-blocking).
func drainQueue(c *Controller) []string {
	var items []string
	for c.queue.Len() > 0 {
		item, shutdown := c.queue.Get()
		if shutdown {
			break
		}
		items = append(items, item)
		c.queue.Done(item)
	}
	return items
}

func TestEventHandlers(t *testing.T) {
	t.Run("pod transitions to Succeeded — enqueues node", func(t *testing.T) {
		runningPod := makeTestPod("pod1", "node1", "acme", v1.PodRunning, time.Time{})
		client := fake.NewSimpleClientset(runningPod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		// Update the pod to Succeeded via the fake client.
		succeededPod := runningPod.DeepCopy()
		succeededPod.Status.Phase = v1.PodSucceeded
		_, err := client.CoreV1().Pods("default").UpdateStatus(context.Background(), succeededPod, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("update pod status: %v", err)
		}

		// Wait for the event to propagate through the informer.
		time.Sleep(500 * time.Millisecond)
		close(stopCh)

		items := drainQueue(controller)
		if len(items) == 0 {
			t.Fatal("expected node1 to be enqueued after pod transition to Succeeded")
		}
		if items[0] != "node1" {
			t.Errorf("expected enqueued node name 'node1', got %q", items[0])
		}
	})

	t.Run("pod transitions to Failed — enqueues node", func(t *testing.T) {
		runningPod := makeTestPod("pod1", "node1", "acme", v1.PodRunning, time.Time{})
		client := fake.NewSimpleClientset(runningPod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		failedPod := runningPod.DeepCopy()
		failedPod.Status.Phase = v1.PodFailed
		_, err := client.CoreV1().Pods("default").UpdateStatus(context.Background(), failedPod, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("update pod status: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
		close(stopCh)

		items := drainQueue(controller)
		if len(items) == 0 {
			t.Fatal("expected node1 to be enqueued after pod transition to Failed")
		}
	})

	t.Run("pod deleted — enqueues node", func(t *testing.T) {
		pod := makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, time.Now())
		client := fake.NewSimpleClientset(pod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		err := client.CoreV1().Pods("default").Delete(context.Background(), "pod1", metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("delete pod: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
		close(stopCh)

		items := drainQueue(controller)
		if len(items) == 0 {
			t.Fatal("expected node1 to be enqueued after pod deletion")
		}
	})

	t.Run("running pod label update — no enqueue", func(t *testing.T) {
		runningPod := makeTestPod("pod1", "node1", "acme", v1.PodRunning, time.Time{})
		client := fake.NewSimpleClientset(runningPod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		// Update labels only (no phase change) — should NOT trigger enqueue.
		updatedPod := runningPod.DeepCopy()
		updatedPod.Labels["extra"] = "label"
		_, err := client.CoreV1().Pods("default").Update(context.Background(), updatedPod, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("update pod: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
		close(stopCh)

		items := drainQueue(controller)
		if len(items) != 0 {
			t.Errorf("expected no enqueue for label-only update, got %v", items)
		}
	})

	t.Run("pod with empty nodeName deleted — no enqueue", func(t *testing.T) {
		pod := makeTestPod("pod1", "", "acme", v1.PodSucceeded, time.Now())
		client := fake.NewSimpleClientset(pod)
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		stopCh := make(chan struct{})
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		err := client.CoreV1().Pods("default").Delete(context.Background(), "pod1", metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("delete pod: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
		close(stopCh)

		items := drainQueue(controller)
		if len(items) != 0 {
			t.Errorf("expected no enqueue for pod with empty nodeName, got %v", items)
		}
	})
}

func TestControllerRun(t *testing.T) {
	t.Run("starts and stops cleanly", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		factory.Start(ctx.Done())

		errCh := make(chan error, 1)
		go func() {
			errCh <- controller.Run(ctx, 1)
		}()

		err := <-errCh
		if err != nil {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	})

	t.Run("returns error on cache sync failure", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		factory := informers.NewSharedInformerFactory(client, 0)
		controller := NewController(client, factory)

		// Override hasSynced with a func that never returns true.
		controller.hasSynced = []cache.InformerSynced{func() bool { return false }}

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		err := controller.Run(ctx, 1)
		if err == nil {
			t.Fatal("expected error from Run when caches fail to sync")
		}
		if !strings.Contains(err.Error(), "failed to sync") {
			t.Errorf("expected error containing 'failed to sync', got %q", err.Error())
		}
	})
}

func TestReconcileNode_StartTimeFallback(t *testing.T) {
	// Pod is Succeeded but has no ContainerStatuses — podTerminationTime should
	// fall back to pod.Status.StartTime.
	startTime := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-no-containers",
			Namespace: "default",
			Labels:    map[string]string{LabelOrg: "acme"},
		},
		Spec: v1.PodSpec{
			NodeName: "node1",
		},
		Status: v1.PodStatus{
			Phase:     v1.PodSucceeded,
			StartTime: &metav1.Time{Time: startTime},
			// No ContainerStatuses — forces the StartTime fallback path.
		},
	}
	node := makeTestNode("node1", map[string]string{})

	client := fake.NewSimpleClientset(node, pod)
	factory := informers.NewSharedInformerFactory(client, 0)
	controller := NewController(client, factory)

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	err := controller.reconcileNode(context.Background(), "node1")
	close(stopCh)
	if err != nil {
		t.Fatalf("reconcileNode returned error: %v", err)
	}

	// Should have patched last-workload-org to "acme" using StartTime for ordering.
	var patched bool
	for _, a := range client.Actions() {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
			patched = true
			patchAction := a.(patchGetter)
			var patch map[string]interface{}
			if err := json.Unmarshal(patchAction.GetPatch(), &patch); err != nil {
				t.Fatalf("unmarshal patch: %v", err)
			}
			metadata := patch["metadata"].(map[string]interface{})
			labels := metadata["labels"].(map[string]interface{})
			if labels[LabelLastWorkloadOrg] != "acme" {
				t.Errorf("expected last-workload-org=acme, got %v", labels[LabelLastWorkloadOrg])
			}
		}
	}
	if !patched {
		t.Fatal("expected a patch action for org label update")
	}
}

func TestEventHandlers_DeleteTombstone(t *testing.T) {
	// When the informer misses a delete event, it delivers a
	// cache.DeletedFinalStateUnknown tombstone. Verify the handler extracts the pod.
	client := fake.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)
	controller := NewController(client, factory)

	pod := makeTestPod("tombstone-pod", "node-from-tombstone", "acme", v1.PodSucceeded, time.Now())
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "default/tombstone-pod",
		Obj: pod,
	}

	controller.handlePodDelete(tombstone)

	items := drainQueue(controller)
	found := false
	for _, item := range items {
		if item == "node-from-tombstone" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'node-from-tombstone' to be enqueued from tombstone delete, got %v", items)
	}
}

func TestEventHandlers_DeleteTombstoneNonPod(t *testing.T) {
	// A tombstone wrapping a non-pod object should be silently ignored.
	client := fake.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)
	controller := NewController(client, factory)

	tombstone := cache.DeletedFinalStateUnknown{
		Key: "default/not-a-pod",
		Obj: "not-a-pod-object",
	}

	controller.handlePodDelete(tombstone)

	items := drainQueue(controller)
	if len(items) != 0 {
		t.Errorf("expected no items enqueued for non-pod tombstone, got %v", items)
	}
}

func TestReconcileNode_PatchError(t *testing.T) {
	// Verify that reconcileNode returns an error when the API patch fails.
	node := makeTestNode("node1", map[string]string{})
	pod := makeTestPod("pod1", "node1", "acme", v1.PodSucceeded, time.Now())

	client := fake.NewSimpleClientset(node, pod)
	// Inject a reactor that fails all node patches.
	client.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated API error")
	})

	factory := informers.NewSharedInformerFactory(client, 0)
	controller := NewController(client, factory)

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	err := controller.reconcileNode(context.Background(), "node1")
	close(stopCh)

	if err == nil {
		t.Fatal("expected error from reconcileNode when patch fails")
	}
	if !strings.Contains(err.Error(), "simulated API error") {
		t.Errorf("expected error to contain 'simulated API error', got %q", err.Error())
	}
}

func TestControllerRun_MultipleWorkers(t *testing.T) {
	// Verify multiple workers can process items concurrently without panics or races.
	node1 := makeTestNode("node1", map[string]string{})
	node2 := makeTestNode("node2", map[string]string{})
	node3 := makeTestNode("node3", map[string]string{})
	pod1 := makeTestPod("pod1", "node1", "alpha", v1.PodSucceeded, time.Now())
	pod2 := makeTestPod("pod2", "node2", "beta", v1.PodSucceeded, time.Now())
	pod3 := makeTestPod("pod3", "node3", "gamma", v1.PodSucceeded, time.Now())

	client := fake.NewSimpleClientset(node1, node2, node3, pod1, pod2, pod3)
	factory := informers.NewSharedInformerFactory(client, 0)
	controller := NewController(client, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Add items before starting workers.
	controller.queue.Add("node1")
	controller.queue.Add("node2")
	controller.queue.Add("node3")

	errCh := make(chan error, 1)
	go func() {
		errCh <- controller.Run(ctx, 2) // 2 concurrent workers
	}()

	// Wait for items to be processed (all 3 nodes should be patched).
	time.Sleep(500 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// All 3 nodes should have been patched.
	var patchCount int
	for _, a := range client.Actions() {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "nodes" {
			patchCount++
		}
	}
	if patchCount < 3 {
		t.Errorf("expected at least 3 node patches from 2 workers, got %d", patchCount)
	}
}
