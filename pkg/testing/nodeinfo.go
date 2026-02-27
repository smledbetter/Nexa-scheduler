// Package testing provides test helpers for constructing Kubernetes scheduler
// framework types. These helpers are used by plugin unit tests.
package testing

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// MakeNodeInfo constructs a NodeInfo from a Node and optional existing pods.
// The returned value satisfies fwk.NodeInfo and can be passed directly to
// Filter/Score plugin methods.
func MakeNodeInfo(node *v1.Node, pods ...*v1.Pod) fwk.NodeInfo {
	ni := framework.NewNodeInfo(pods...)
	ni.SetNode(node)
	return ni
}

// MakeNode creates a *v1.Node with the given name and labels.
func MakeNode(name string, labels map[string]string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

// MakePod creates a *v1.Pod with the given name and labels.
func MakePod(name string, labels map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}
