package nodestate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
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
		name          string
		node          *v1.Node
		pods          []*v1.Pod
		wantLabels    map[string]string
		wantNoPatches bool
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
			}
		})
	}
}

// patchGetter is the interface for accessing patch data from a fake client action.
type patchGetter interface {
	GetPatch() []byte
}
