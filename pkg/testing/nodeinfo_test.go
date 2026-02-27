package testing

import (
	"testing"
)

func TestMakeNode(t *testing.T) {
	labels := map[string]string{"nexa.io/region": "us-west1"}
	node := MakeNode("test-node", labels)

	if node.Name != "test-node" {
		t.Errorf("MakeNode() name = %q, want %q", node.Name, "test-node")
	}
	if node.Labels["nexa.io/region"] != "us-west1" {
		t.Errorf("MakeNode() region label = %q, want %q", node.Labels["nexa.io/region"], "us-west1")
	}
}

func TestMakeNodeNilLabels(t *testing.T) {
	node := MakeNode("bare-node", nil)
	if node.Name != "bare-node" {
		t.Errorf("MakeNode() name = %q, want %q", node.Name, "bare-node")
	}
	if node.Labels != nil {
		t.Errorf("MakeNode(nil) labels = %v, want nil", node.Labels)
	}
}

func TestMakePod(t *testing.T) {
	labels := map[string]string{"nexa.io/privacy": "high"}
	pod := MakePod("test-pod", labels)

	if pod.Name != "test-pod" {
		t.Errorf("MakePod() name = %q, want %q", pod.Name, "test-pod")
	}
	if pod.Labels["nexa.io/privacy"] != "high" {
		t.Errorf("MakePod() privacy label = %q, want %q", pod.Labels["nexa.io/privacy"], "high")
	}
}

func TestMakeNodeInfo(t *testing.T) {
	node := MakeNode("info-node", map[string]string{"nexa.io/region": "eu-west1"})
	ni := MakeNodeInfo(node)

	got := ni.Node()
	if got == nil {
		t.Fatal("MakeNodeInfo() Node() returned nil")
	}
	if got.Name != "info-node" {
		t.Errorf("MakeNodeInfo() Node().Name = %q, want %q", got.Name, "info-node")
	}
	if got.Labels["nexa.io/region"] != "eu-west1" {
		t.Errorf("MakeNodeInfo() region = %q, want %q", got.Labels["nexa.io/region"], "eu-west1")
	}
}

func TestMakeNodeInfoWithPods(t *testing.T) {
	node := MakeNode("multi-node", nil)
	pod1 := MakePod("pod-1", map[string]string{"nexa.io/org": "acme"})
	pod2 := MakePod("pod-2", map[string]string{"nexa.io/org": "acme"})

	ni := MakeNodeInfo(node, pod1, pod2)

	pods := ni.GetPods()
	if len(pods) != 2 {
		t.Errorf("MakeNodeInfo() pod count = %d, want 2", len(pods))
	}
}
