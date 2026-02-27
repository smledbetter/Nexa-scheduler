package nodestate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/attestation"
)

// AttestationController periodically verifies TEE nodes against a remote
// attestation service and patches node labels with the verification result.
type AttestationController struct {
	kubeClient kubernetes.Interface
	nodeLister corev1listers.NodeLister
	attester   attestation.Attester
	interval   time.Duration
}

// NewAttestationController creates a controller that verifies TEE nodes.
func NewAttestationController(
	client kubernetes.Interface,
	nodeLister corev1listers.NodeLister,
	attester attestation.Attester,
	interval time.Duration,
) *AttestationController {
	return &AttestationController{
		kubeClient: client,
		nodeLister: nodeLister,
		attester:   attester,
		interval:   interval,
	}
}

// Run starts the periodic attestation loop and blocks until ctx is cancelled.
func (c *AttestationController) Run(ctx context.Context) error {
	klog.InfoS("attestation controller starting", "interval", c.interval)

	// Run immediately on start, then periodically.
	c.verifyAllNodes(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.InfoS("attestation controller stopping")
			return nil
		case <-ticker.C:
			c.verifyAllNodes(ctx)
		}
	}
}

// verifyAllNodes lists TEE-capable nodes and verifies each one.
func (c *AttestationController) verifyAllNodes(ctx context.Context) {
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "failed to list nodes for attestation")
		return
	}

	for _, node := range nodes {
		tee := node.Labels["nexa.io/tee"]
		if tee == "" || tee == "none" {
			continue
		}

		if err := c.verifyNode(ctx, node.Name); err != nil {
			klog.ErrorS(err, "attestation verification failed", "node", node.Name)
		}
	}
}

// verifyNode verifies a single node and patches its attestation labels.
// Fail-closed: any error results in tee-attested=false.
func (c *AttestationController) verifyNode(ctx context.Context, nodeName string) error {
	result, err := c.attester.Verify(ctx, nodeName)

	patchLabels := make(map[string]interface{})

	if err != nil || result == nil {
		// Fail-closed: verification error → mark as unattested.
		patchLabels[LabelTEEAttested] = "false"
		klog.InfoS("attestation failed, marking node unattested", "node", nodeName, "error", err)
	} else if result.Attested {
		patchLabels[LabelTEEAttested] = "true"
		patchLabels[LabelTEEAttestationTime] = result.Timestamp.Format(time.RFC3339)
		if result.TrustAnchor != "" {
			patchLabels[LabelTEETrustAnchor] = result.TrustAnchor
		}
		klog.InfoS("attestation succeeded", "node", nodeName, "trustAnchor", result.TrustAnchor)
	} else {
		// Attestation service returned attested=false.
		patchLabels[LabelTEEAttested] = "false"
		klog.InfoS("attestation returned not-attested", "node", nodeName)
	}

	return c.patchNodeLabels(ctx, nodeName, patchLabels)
}

func (c *AttestationController) patchNodeLabels(ctx context.Context, nodeName string, nodeLabels map[string]interface{}) error {
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
		return fmt.Errorf("patch node %s attestation labels: %w", nodeName, err)
	}

	klog.InfoS("patched attestation labels", "node", nodeName, "labels", nodeLabels)
	return nil
}
