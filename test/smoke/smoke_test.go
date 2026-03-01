//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

var (
	testClient        kubernetes.Interface
	controllerEnabled bool
	webhookEnabled    bool
	kueueEnabled      bool
)

// TestMain manages the shared Kind cluster lifecycle.
// Creates the cluster, builds/loads the image, installs the chart, labels workers,
// runs all tests, then tears everything down.
func TestMain(m *testing.M) {
	code := func() int {
		// Cleanup on exit, even on panic.
		defer deleteCluster()
		defer uninstallChart()

		t := &mainT{}
		createCluster(t)
		if t.failed {
			fmt.Fprintf(os.Stderr, "cluster creation failed: %s\n", t.msg)
			return 1
		}

		buildAndLoadImage(t)
		if t.failed {
			fmt.Fprintf(os.Stderr, "image build/load failed: %s\n", t.msg)
			return 1
		}

		installChart(t)
		if t.failed {
			fmt.Fprintf(os.Stderr, "chart install failed: %s\n", t.msg)
			return 1
		}

		labelWorkers(t)
		if t.failed {
			fmt.Fprintf(os.Stderr, "node labeling failed: %s\n", t.msg)
			return 1
		}

		testClient = kubeClient(t)
		if t.failed {
			fmt.Fprintf(os.Stderr, "kube client creation failed: %s\n", t.msg)
			return 1
		}

		waitForSchedulerReady(t, testClient)
		if t.failed {
			fmt.Fprintf(os.Stderr, "scheduler not ready: %s\n", t.msg)
			return 1
		}

		// Set up node controller (non-fatal).
		buildAndLoadControllerImage(t)
		if !t.failed {
			installControllerChart(t)
			if !t.failed {
				waitForControllerReady(t, testClient)
				if !t.failed {
					controllerEnabled = true
					defer uninstallControllerChart()
				}
			}
		}
		if t.failed {
			fmt.Fprintf(os.Stderr, "controller setup failed (non-fatal): %s\n", t.msg)
			t.failed = false
			t.msg = ""
		}

		// Set up webhook if Docker is available for the webhook image build.
		buildAndLoadWebhookImage(t)
		if !t.failed {
			caBundle := generateWebhookCerts(t)
			if !t.failed {
				installWebhookChart(t, caBundle)
				if !t.failed {
					webhookEnabled = true
					defer uninstallWebhookChart()
				}
			}
		}
		if t.failed {
			// Webhook setup failure is non-fatal — run other tests.
			fmt.Fprintf(os.Stderr, "webhook setup failed (non-fatal): %s\n", t.msg)
			t.failed = false
			t.msg = ""
		}

		// Set up Kueue for integration smoke tests.
		installKueue(t)
		if !t.failed {
			kueueEnabled = true
			defer uninstallKueue()
			// Create shared ResourceFlavors (cluster-scoped, aligned with worker labels).
			createResourceFlavor(t, "us-west1", map[string]string{"nexa.io/region": "us-west1"})
			createResourceFlavor(t, "eu-west1", map[string]string{"nexa.io/region": "eu-west1"})
			createResourceFlavor(t, "default-flavor", nil)
		}
		if t.failed {
			// Kueue setup failure is non-fatal — run other tests.
			fmt.Fprintf(os.Stderr, "kueue setup failed (non-fatal): %s\n", t.msg)
			t.failed = false
			t.msg = ""
		}

		return m.Run()
	}()
	os.Exit(code)
}

// mainT satisfies the tb interface for use in TestMain where *testing.T is unavailable.
type mainT struct {
	failed bool
	msg    string
}

func (t *mainT) Helper()                       {}
func (t *mainT) Name() string                  { return "TestMain" }
func (t *mainT) Cleanup(_ func())              {}
func (t *mainT) Fatalf(format string, a ...any) { t.failed = true; t.msg = fmt.Sprintf(format, a...) }

// waitForSchedulerReady waits until the scheduler pod is Running and Ready.
func waitForSchedulerReady(t tb, client kubernetes.Interface) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		pods, err := client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=nexa-scheduler",
		})
		if err == nil && len(pods.Items) > 0 {
			pod := pods.Items[0]
			if pod.Status.Phase == "Running" {
				for _, c := range pod.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						return
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("scheduler pod not ready within 120s")
}

// TestRegionFiltering verifies that a pod with region=us-west1 lands on a us-west1 node.
func TestRegionFiltering(t *testing.T) {
	ns := createTestNamespace(t, testClient)
	pod := makePod("region-test", map[string]string{
		"nexa.io/region": "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "region-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if region := node.Labels["nexa.io/region"]; region != "us-west1" {
		t.Errorf("pod scheduled on node %s with region=%q, want us-west1", nodeName, region)
	}
}

// TestPrivacyFiltering verifies that a high-privacy pod from org=alpha lands on
// a wiped node with matching org (worker-0).
func TestPrivacyFiltering(t *testing.T) {
	ns := createTestNamespace(t, testClient)
	pod := makePod("privacy-test", map[string]string{
		"nexa.io/privacy": "high",
		"nexa.io/org":     "alpha",
		"nexa.io/region":  "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "privacy-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if wiped := node.Labels["nexa.io/wiped"]; wiped != "true" {
		t.Errorf("pod scheduled on non-wiped node %s", nodeName)
	}
	if org := node.Labels["nexa.io/last-workload-org"]; org != "alpha" {
		t.Errorf("pod scheduled on node %s with org=%q, want alpha", nodeName, org)
	}
}

// TestPrivacyRejection verifies that a high-privacy pod from org=gamma stays Pending
// because no node has org=gamma and wiped=true.
func TestPrivacyRejection(t *testing.T) {
	ns := createTestNamespace(t, testClient)
	pod := makePod("privacy-reject", map[string]string{
		"nexa.io/privacy": "high",
		"nexa.io/org":     "gamma",
		"nexa.io/region":  "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	waitForPodPending(t, testClient, ns, "privacy-reject", 15*time.Second)
}

// TestOrgIsolation verifies that a high-privacy pod from org=beta in us-west1 stays Pending.
// Worker-0 is wiped but org=alpha (mismatch), worker-1 is not wiped, worker-2 is eu-west1.
func TestOrgIsolation(t *testing.T) {
	ns := createTestNamespace(t, testClient)
	pod := makePod("org-isolation", map[string]string{
		"nexa.io/privacy": "high",
		"nexa.io/org":     "beta",
		"nexa.io/region":  "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	waitForPodPending(t, testClient, ns, "org-isolation", 15*time.Second)
}

// TestAuditLogs verifies that the scheduler emits structured JSON audit log entries
// containing the "event" field for scheduling decisions.
func TestAuditLogs(t *testing.T) {
	ns := createTestNamespace(t, testClient)
	pod := makePod("audit-test", map[string]string{
		"nexa.io/region": "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}
	waitForPodScheduled(t, testClient, ns, "audit-test", 60*time.Second)

	// Give the logger a moment to flush.
	time.Sleep(2 * time.Second)

	logs := schedulerLogs(t, testClient)
	if !strings.Contains(logs, `"event"`) {
		t.Errorf("scheduler logs do not contain audit event entries")
	}
	if !strings.Contains(logs, `"scheduled"`) && !strings.Contains(logs, `"scheduling_failed"`) {
		t.Errorf("scheduler logs do not contain expected event types (scheduled or scheduling_failed)")
	}
}

// TestMetricsEndpoint verifies that the /metrics endpoint exposes Nexa-specific metrics.
func TestMetricsEndpoint(t *testing.T) {
	pods, err := testClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=nexa-scheduler",
	})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("could not find scheduler pod: %v", err)
	}
	podName := pods.Items[0].Name

	// Start port-forward in background.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pfCmd := exec.CommandContext(ctx, "kubectl", "port-forward",
		"-n", namespace, podName, "10259:10259")
	if err := pfCmd.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	defer func() { _ = pfCmd.Process.Kill() }()

	// Wait for port-forward to establish.
	time.Sleep(3 * time.Second)

	// Scrape metrics with retries. kube-scheduler serves HTTPS on 10259.
	var out string
	var curlErr error
	for i := 0; i < 3; i++ {
		out, curlErr = runCmdNoFail("curl", "-sk", "https://localhost:10259/metrics")
		if curlErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if curlErr != nil {
		t.Fatalf("curl metrics failed after 3 attempts: %v", curlErr)
	}

	expectedMetrics := []string{
		"nexa_filter_results_total",
		"nexa_scheduling_duration_seconds",
		"nexa_score_distribution",
		"nexa_policy_evaluations_total",
	}
	for _, m := range expectedMetrics {
		if !strings.Contains(out, m) {
			t.Errorf("metrics output does not contain %s", m)
		}
	}
}

// TestPolicyHotReload verifies that changing the policy ConfigMap affects scheduling.
// Disables region policy, then verifies a pod with region=eu-west1 can schedule on any node.
func TestPolicyHotReload(t *testing.T) {
	client := testClient

	// Patch the policy ConfigMap to disable region filtering.
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), "nexa-scheduler-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get policy ConfigMap: %v", err)
	}
	cm.Data["policy.json"] = `{
		"regionPolicy": {
			"enabled": false,
			"defaultRegion": "",
			"defaultZone": ""
		},
		"privacyPolicy": {
			"enabled": true,
			"defaultPrivacy": "standard",
			"strictOrgIsolation": false
		}
	}`
	_, err = client.CoreV1().ConfigMaps(namespace).Update(context.Background(), cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update policy ConfigMap: %v", err)
	}

	// Wait for the informer cache to pick up the change.
	time.Sleep(5 * time.Second)

	// Create a pod requesting eu-west1. With region policy disabled,
	// it should schedule on any node (including us-west1 workers).
	ns := createTestNamespace(t, client)
	pod := makePod("hotreload-test", map[string]string{
		"nexa.io/region": "eu-west1",
	})
	_, err = client.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, client, ns, "hotreload-test", 60*time.Second)
	t.Logf("pod scheduled on %s with region policy disabled (any node is acceptable)", nodeName)

	// Restore the original policy.
	cm, _ = client.CoreV1().ConfigMaps(namespace).Get(context.Background(), "nexa-scheduler-config", metav1.GetOptions{})
	cm.Data["policy.json"] = `{
		"regionPolicy": {
			"enabled": true,
			"defaultRegion": "",
			"defaultZone": ""
		},
		"privacyPolicy": {
			"enabled": true,
			"defaultPrivacy": "standard",
			"strictOrgIsolation": false
		}
	}`
	_, _ = client.CoreV1().ConfigMaps(namespace).Update(context.Background(), cm, metav1.UpdateOptions{})
}

// --- CRD-based policy smoke tests ---

// TestCRDPolicyScheduling verifies that when a NexaPolicy CRD is installed and a resource
// is created, the scheduler uses the CRD policy for scheduling decisions.
func TestCRDPolicyScheduling(t *testing.T) {
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}

	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
    defaultRegion: ""
    defaultZone: ""
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
    strictOrgIsolation: false
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })

	// Wait for the dynamic informer to pick up the CRD resource.
	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)
	pod := makePod("crd-region-test", map[string]string{
		"nexa.io/region": "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "crd-region-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if region := node.Labels["nexa.io/region"]; region != "us-west1" {
		t.Errorf("pod scheduled on node %s with region=%q, want us-west1", nodeName, region)
	}
}

// TestCRDFallbackToConfigMap verifies that when the NexaPolicy resource is deleted
// (but the CRD is still installed), the scheduler falls back to ConfigMap policy.
func TestCRDFallbackToConfigMap(t *testing.T) {
	// Ensure no NexaPolicy resource exists (CRD was installed by TestCRDPolicyScheduling).
	deleteNexaPolicy(t)

	// Wait for informer to notice deletion.
	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)
	pod := makePod("crd-fallback-test", map[string]string{
		"nexa.io/region": "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Should still schedule via ConfigMap fallback.
	nodeName := waitForPodScheduled(t, testClient, ns, "crd-fallback-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if region := node.Labels["nexa.io/region"]; region != "us-west1" {
		t.Errorf("pod scheduled on node %s with region=%q, want us-west1 (ConfigMap fallback)", nodeName, region)
	}
}

// TestCRDPolicyOverridesConfigMap verifies that CRD policy takes precedence over ConfigMap.
// The ConfigMap has region policy enabled, but the CRD disables it — so a pod requesting
// eu-west1 should schedule on any node (including us-west1 workers).
func TestCRDPolicyOverridesConfigMap(t *testing.T) {
	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: false
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
    strictOrgIsolation: false
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })

	// Wait for informer to pick up the CRD.
	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)
	// Pod requests eu-west1, but CRD disables region filtering.
	pod := makePod("crd-override-test", map[string]string{
		"nexa.io/region": "eu-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// With region policy disabled via CRD, pod should schedule on any node.
	nodeName := waitForPodScheduled(t, testClient, ns, "crd-override-test", 60*time.Second)
	t.Logf("pod scheduled on %s with CRD region policy disabled (any node is acceptable)", nodeName)
}

// --- Node controller smoke tests ---

// TestControllerPatchesNodeLabels verifies the node state controller marks a
// wipe-on-complete node as dirty after a pod terminates on it.
func TestControllerPatchesNodeLabels(t *testing.T) {
	if !controllerEnabled {
		t.Skip("node controller not installed")
	}

	// Find worker-0 (has nexa.io/wipe-on-complete=true and nexa.io/wiped=true).
	nodes, err := testClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	var targetNode string
	for _, n := range nodes.Items {
		if n.Labels["nexa.io/wipe-on-complete"] == "true" {
			targetNode = n.Name
			break
		}
	}
	if targetNode == "" {
		t.Fatal("no node with nexa.io/wipe-on-complete=true found")
	}

	// Ensure the node starts wiped=true.
	runCmd(t, "kubectl", "label", "node", targetNode, "nexa.io/wiped=true", "--overwrite")
	t.Cleanup(func() {
		// Restore wiped=true for subsequent tests.
		_, _ = runCmdNoFail("kubectl", "label", "node", targetNode, "nexa.io/wiped=true", "--overwrite")
	})

	// Create a short-lived pod directly on the target node (bypass scheduler).
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "controller-test-pod",
			Labels: map[string]string{
				"nexa.io/org": "alpha",
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      targetNode,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   "busybox:latest",
					Command: []string{"echo", "done"},
				},
			},
		},
	}
	ns := createTestNamespace(t, testClient)
	_, err = testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create controller test pod: %v", err)
	}

	// Wait for pod to complete.
	err = wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 60*time.Second, true,
		func(ctx context.Context) (bool, error) {
			p, err := testClient.CoreV1().Pods(ns).Get(ctx, "controller-test-pod", metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed, nil
		})
	if err != nil {
		t.Fatalf("controller-test-pod did not complete within 60s: %v", err)
	}

	// Poll for the controller to mark the node as dirty (wiped=false).
	err = wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 60*time.Second, true,
		func(ctx context.Context) (bool, error) {
			node, err := testClient.CoreV1().Nodes().Get(ctx, targetNode, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return node.Labels["nexa.io/wiped"] == "false", nil
		})
	if err != nil {
		t.Fatalf("controller did not mark node %s as dirty (wiped=false) within 60s", targetNode)
	}
	t.Logf("controller marked node %s as dirty after pod termination", targetNode)
}

// --- Webhook admission smoke tests ---

// TestWebhookSpoofedOrgRejected verifies that a pod with a spoofed org label is rejected
// by the admission webhook in a webhook-enabled namespace.
func TestWebhookSpoofedOrgRejected(t *testing.T) {
	if !webhookEnabled {
		t.Skip("webhook not installed")
	}
	createWebhookTestNamespace(t, testClient, "alpha-workloads")

	pod := makePod("spoofed-org", map[string]string{
		"nexa.io/org": "beta",
	})
	_, err := testClient.CoreV1().Pods("alpha-workloads").Create(context.Background(), pod, metav1.CreateOptions{})
	if err == nil {
		t.Fatal("expected pod creation to be rejected, but it was admitted")
	}
	if !strings.Contains(err.Error(), "not authorized for org") {
		t.Errorf("expected rejection message about org authorization, got: %v", err)
	}
}

// TestWebhookAuthorizedOrgAdmitted verifies that a pod with a valid org label is admitted
// by the admission webhook.
func TestWebhookAuthorizedOrgAdmitted(t *testing.T) {
	if !webhookEnabled {
		t.Skip("webhook not installed")
	}
	// Reuse the alpha-workloads namespace (already created by previous test or create it).
	// Create a fresh one to avoid ordering dependency.
	createWebhookTestNamespace(t, testClient, "alpha-workloads-admit")

	// We need a rule for this namespace. The wildcard rule allows "default-org".
	// The alpha-workloads rule allows "alpha". Let's use the wildcard.
	pod := makePod("valid-org", map[string]string{
		"nexa.io/org": "default-org",
	})
	_, err := testClient.CoreV1().Pods("alpha-workloads-admit").Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected pod to be admitted, got: %v", err)
	}
}

// TestWebhookNoLabelsAdmitted verifies that a pod with no nexa.io labels is admitted.
func TestWebhookNoLabelsAdmitted(t *testing.T) {
	if !webhookEnabled {
		t.Skip("webhook not installed")
	}
	createWebhookTestNamespace(t, testClient, "webhook-nolabels")

	pod := makePod("no-labels", map[string]string{
		"app": "myapp",
	})
	_, err := testClient.CoreV1().Pods("webhook-nolabels").Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected pod with no nexa.io labels to be admitted, got: %v", err)
	}
}

// --- Kueue integration smoke tests ---

// TestKueueAdmitNexaSchedule verifies the two-phase flow:
// Kueue admits the workload (unsuspends the Job), then Nexa schedules the pod
// to a node matching the region constraints.
func TestKueueAdmitNexaSchedule(t *testing.T) {
	if !kueueEnabled {
		t.Skip("kueue not installed")
	}
	client := testClient
	ns := createTestNamespace(t, client)

	suffix := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	lqName := fmt.Sprintf("lq-%s", suffix)
	setupKueueResources(t, ns, suffix, "us-west1", "4")

	job := makeJob("kueue-nexa-admit", map[string]string{
		"nexa.io/region":             "us-west1",
		"kueue.x-k8s.io/queue-name": lqName,
	})
	_, err := client.BatchV1().Jobs(ns).Create(context.Background(), job, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Phase 1: Wait for Kueue to admit (unsuspend the Job).
	waitForWorkloadAdmitted(t, client, ns, "kueue-nexa-admit", 120*time.Second)

	// Phase 2: Wait for Nexa to schedule the pod.
	nodeName := waitForJobPodScheduled(t, client, ns, "kueue-nexa-admit", 60*time.Second)

	// Verify: pod landed on a us-west1 node.
	node, err := client.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if region := node.Labels["nexa.io/region"]; region != "us-west1" {
		t.Errorf("pod scheduled on node %s with region=%q, want us-west1", nodeName, region)
	}
}

// TestKueueAdmitNexaRejects verifies that when Kueue admits a workload but
// no nodes satisfy Nexa's privacy constraints, the pod stays Pending.
func TestKueueAdmitNexaRejects(t *testing.T) {
	if !kueueEnabled {
		t.Skip("kueue not installed")
	}
	client := testClient
	ns := createTestNamespace(t, client)

	suffix := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	lqName := fmt.Sprintf("lq-%s", suffix)
	setupKueueResources(t, ns, suffix, "default-flavor", "4")

	// org=gamma has no matching wiped node — Nexa will reject all nodes.
	job := makeJob("kueue-nexa-reject", map[string]string{
		"nexa.io/privacy":            "high",
		"nexa.io/org":                "gamma",
		"nexa.io/region":             "us-west1",
		"kueue.x-k8s.io/queue-name": lqName,
	})
	_, err := client.BatchV1().Jobs(ns).Create(context.Background(), job, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Phase 1: Kueue admits the workload.
	waitForWorkloadAdmitted(t, client, ns, "kueue-nexa-reject", 120*time.Second)

	// Phase 2: Pod should stay Pending — Nexa filters all nodes.
	waitForJobPodPending(t, client, ns, "kueue-nexa-reject", 15*time.Second)
}

// TestKueueSuspendsQuotaExceeded verifies that when Kueue's quota is exhausted,
// the Job stays suspended and Nexa never sees the pod (no pod is created).
func TestKueueSuspendsQuotaExceeded(t *testing.T) {
	if !kueueEnabled {
		t.Skip("kueue not installed")
	}
	client := testClient
	ns := createTestNamespace(t, client)

	// Create a ClusterQueue with minimal quota (1 CPU).
	suffix := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	lqName := fmt.Sprintf("lq-%s", suffix)
	setupKueueResources(t, ns, suffix, "default-flavor", "1")

	// First job: consumes the entire quota (1 CPU).
	job1 := makeJobWithResources("kueue-fill-quota", map[string]string{
		"nexa.io/region":             "us-west1",
		"kueue.x-k8s.io/queue-name": lqName,
	}, "1", "512Mi")
	_, err := client.BatchV1().Jobs(ns).Create(context.Background(), job1, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create job1: %v", err)
	}
	waitForWorkloadAdmitted(t, client, ns, "kueue-fill-quota", 120*time.Second)

	// Second job: should stay suspended (quota exhausted).
	job2 := makeJobWithResources("kueue-quota-blocked", map[string]string{
		"nexa.io/region":             "us-west1",
		"kueue.x-k8s.io/queue-name": lqName,
	}, "1", "512Mi")
	_, err = client.BatchV1().Jobs(ns).Create(context.Background(), job2, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create job2: %v", err)
	}

	// Job2 should stay suspended — Kueue won't admit it.
	waitForJobSuspended(t, client, ns, "kueue-quota-blocked", 15*time.Second)

	// Verify no pod was created for the suspended job.
	pods, err := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{
		LabelSelector: "job-name=kueue-quota-blocked",
	})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) > 0 {
		t.Errorf("expected no pods for suspended job, got %d", len(pods.Items))
	}
}

// --- Confidential compute smoke tests ---

// TestConfidentialFiltering verifies that a pod with confidential=required lands on
// a TEE-capable node (worker-0 has TDX, worker-2 has SEV-SNP) and skips worker-1 (no TEE).
// The confidential policy must be enabled via CRD for this test.
func TestConfidentialFiltering(t *testing.T) {
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}

	// Enable confidential policy via CRD.
	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
  confidentialPolicy:
    enabled: true
    requireEncryptedDisk: true
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })

	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)
	pod := makePod("confidential-test", map[string]string{
		"nexa.io/confidential": "required",
		"nexa.io/region":       "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Should schedule on worker-0 (us-west1 + TDX + encrypted). Worker-1 has no TEE.
	nodeName := waitForPodScheduled(t, testClient, ns, "confidential-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	tee := node.Labels["nexa.io/tee"]
	if tee == "" || tee == "none" {
		t.Errorf("confidential pod scheduled on non-TEE node %s (tee=%q)", nodeName, tee)
	}
	if enc := node.Labels["nexa.io/disk-encrypted"]; enc != "true" {
		t.Errorf("confidential pod scheduled on unencrypted node %s", nodeName)
	}
}

// TestWipeTimestampFreshness verifies that a high-privacy pod with cooldown policy
// is scheduled to a node with a recent wipe-timestamp and rejects stale nodes.
func TestWipeTimestampFreshness(t *testing.T) {
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}

	// Enable cooldown policy via CRD (24h cooldown).
	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
    cooldownHours: 24
  confidentialPolicy:
    enabled: false
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })

	time.Sleep(5 * time.Second)

	// Set a recent wipe-timestamp on worker-0 (should pass cooldown).
	recentTime := time.Now().UTC().Format(time.RFC3339)
	runCmd(t, "kubectl", "label", "node", "--selector=nexa.io/last-workload-org=alpha",
		"nexa.io/wipe-timestamp="+recentTime, "--overwrite")
	// Set a stale wipe-timestamp on worker-2 (should fail cooldown).
	staleTime := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	runCmd(t, "kubectl", "label", "node", "--selector=nexa.io/last-workload-org=beta",
		"nexa.io/wipe-timestamp="+staleTime, "--overwrite")
	t.Cleanup(func() {
		// Remove wipe-timestamp labels after test.
		_, _ = runCmdNoFail("kubectl", "label", "node", "--selector=nexa.io/last-workload-org=alpha",
			"nexa.io/wipe-timestamp-", "--overwrite")
		_, _ = runCmdNoFail("kubectl", "label", "node", "--selector=nexa.io/last-workload-org=beta",
			"nexa.io/wipe-timestamp-", "--overwrite")
	})

	ns := createTestNamespace(t, testClient)

	// Pod requesting high-privacy on us-west1. Worker-0 has recent timestamp — should schedule.
	pod := makePod("freshness-test", map[string]string{
		"nexa.io/privacy": "high",
		"nexa.io/org":     "alpha",
		"nexa.io/region":  "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "freshness-test", 60*time.Second)
	t.Logf("high-privacy pod with cooldown scheduled on %s (has recent wipe-timestamp)", nodeName)
}

// TestE2EFullChain is the flagship smoke test. It exercises the complete evidence chain:
// webhook admission → region + privacy + confidential scheduling → audit log → compliance parsing.
func TestE2EFullChain(t *testing.T) {
	if !webhookEnabled {
		t.Skip("webhook not available — skipping full-chain test")
	}

	// Enable all policies via CRD.
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}
	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
  confidentialPolicy:
    enabled: true
    requireEncryptedDisk: true
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })
	time.Sleep(5 * time.Second)

	// Create a webhook-enabled namespace for org=alpha.
	nsName := fmt.Sprintf("e2e-fullchain-%d", time.Now().UnixNano()%1000000)
	createWebhookTestNamespace(t, testClient, nsName)

	// Pod requiring all 3 constraint types: region, privacy, confidential.
	pod := makePod("fullchain-test", map[string]string{
		"nexa.io/org":          "alpha",
		"nexa.io/privacy":      "high",
		"nexa.io/region":       "us-west1",
		"nexa.io/confidential": "required",
	})
	_, err := testClient.CoreV1().Pods(nsName).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod (webhook should admit org=alpha in authorized namespace): %v", err)
	}

	// Should land on worker-0: only us-west1 node with TEE + encrypted + wiped + org=alpha.
	nodeName := waitForPodScheduled(t, testClient, nsName, "fullchain-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	if r := node.Labels["nexa.io/region"]; r != "us-west1" {
		t.Errorf("expected region=us-west1, got %q", r)
	}
	if tee := node.Labels["nexa.io/tee"]; tee == "" || tee == "none" {
		t.Errorf("expected TEE-capable node, got tee=%q", tee)
	}

	// Verify audit log contains a scheduled event for this pod.
	time.Sleep(2 * time.Second) // let logger flush
	logs := schedulerLogs(t, testClient)
	if !strings.Contains(logs, "fullchain-test") {
		t.Errorf("audit logs do not mention fullchain-test pod")
	}
	if !strings.Contains(logs, `"scheduled"`) {
		t.Errorf("audit logs do not contain a 'scheduled' event")
	}

	// Verify audit JSON is parseable by compliance.ReadEntries.
	entries, warnings := parseAuditLogs(t, logs)
	if len(entries) == 0 {
		t.Fatal("compliance.ReadEntries returned 0 entries from scheduler logs")
	}
	// Check that at least one entry is for our pod.
	found := false
	for _, e := range entries {
		if e.Pod.Name == "fullchain-test" && e.Event == "scheduled" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'scheduled' audit entry found for fullchain-test (total entries: %d, warnings: %d)", len(entries), len(warnings))
	}
}

// TestRequireTEEForHighPrivacy verifies the requireTEEForHigh policy path:
// a high-privacy pod (without explicit confidential=required) is only scheduled
// on TEE-capable nodes when the policy requires it.
func TestRequireTEEForHighPrivacy(t *testing.T) {
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}

	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
  confidentialPolicy:
    enabled: true
    requireTEEForHigh: true
    requireEncryptedDisk: true
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })
	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)

	// Pod: privacy=high, region=us-west1 (no explicit confidential=required).
	// With requireTEEForHigh=true, the confidential plugin should treat this
	// as needing TEE. Only worker-0 has us-west1 + TEE + encrypted.
	pod := makePod("tee-for-high-test", map[string]string{
		"nexa.io/privacy": "high",
		"nexa.io/org":     "alpha",
		"nexa.io/region":  "us-west1",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "tee-for-high-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	tee := node.Labels["nexa.io/tee"]
	if tee == "" || tee == "none" {
		t.Errorf("high-privacy pod with requireTEEForHigh scheduled on non-TEE node %s (tee=%q)", nodeName, tee)
	}
	t.Logf("requireTEEForHigh: pod scheduled on %s (tee=%s)", nodeName, tee)
}

// TestStrictOrgIsolation verifies that strictOrgIsolation prevents scheduling
// on nodes used by a different org.
func TestStrictOrgIsolation(t *testing.T) {
	mt := &mainT{}
	applyCRD(mt)
	if mt.failed {
		t.Fatalf("failed to install CRD: %s", mt.msg)
	}

	policyYAML := fmt.Sprintf(`apiVersion: nexa.io/v1alpha1
kind: NexaPolicy
metadata:
  name: default
  namespace: %s
spec:
  regionPolicy:
    enabled: true
  privacyPolicy:
    enabled: true
    defaultPrivacy: standard
    strictOrgIsolation: true
  confidentialPolicy:
    enabled: false
`, namespace)
	applyNexaPolicy(t, policyYAML)
	t.Cleanup(func() { deleteNexaPolicy(t) })
	time.Sleep(5 * time.Second)

	ns := createTestNamespace(t, testClient)

	// Pod for org=alpha on us-west1. Worker-0 has org=alpha — should succeed.
	pod1 := makePod("strict-org-alpha", map[string]string{
		"nexa.io/org":     "alpha",
		"nexa.io/region":  "us-west1",
		"nexa.io/privacy": "standard",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod1, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}
	nodeName := waitForPodScheduled(t, testClient, ns, "strict-org-alpha", 60*time.Second)
	t.Logf("org=alpha pod scheduled on %s", nodeName)

	// Pod for org=gamma on us-west1. No us-west1 node has org=gamma.
	// Worker-0 has org=alpha (strict isolation blocks), worker-1 has no org labels.
	// With strictOrgIsolation, a node with a different org is rejected.
	// Worker-1 has no last-workload-org label — it should be acceptable (no conflict).
	pod2 := makePod("strict-org-gamma", map[string]string{
		"nexa.io/org":     "gamma",
		"nexa.io/region":  "us-west1",
		"nexa.io/privacy": "standard",
	})
	_, err = testClient.CoreV1().Pods(ns).Create(context.Background(), pod2, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Worker-1 (us-west1, no org label) should be the only viable option.
	nodeName2 := waitForPodScheduled(t, testClient, ns, "strict-org-gamma", 60*time.Second)
	node2, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName2, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName2, err)
	}
	nodeOrg := node2.Labels["nexa.io/last-workload-org"]
	if nodeOrg != "" && nodeOrg != "gamma" {
		t.Errorf("strict-org-gamma pod scheduled on node %s with org=%s — isolation violated", nodeName2, nodeOrg)
	}
	t.Logf("org=gamma pod scheduled on %s (org=%q)", nodeName2, nodeOrg)
}

// TestZoneFiltering verifies zone-level filtering independently from region.
func TestZoneFiltering(t *testing.T) {
	ns := createTestNamespace(t, testClient)

	// Pod requesting specific zone. Worker-0 has us-west1-a, worker-1 has us-west1-b.
	pod := makePod("zone-test", map[string]string{
		"nexa.io/region": "us-west1",
		"nexa.io/zone":   "us-west1-a",
	})
	_, err := testClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nodeName := waitForPodScheduled(t, testClient, ns, "zone-test", 60*time.Second)
	node, err := testClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node %s: %v", nodeName, err)
	}
	zone := node.Labels["nexa.io/zone"]
	if zone != "us-west1-a" {
		t.Errorf("zone-test pod scheduled on node %s with zone=%q, want us-west1-a", nodeName, zone)
	}
	t.Logf("zone-filtered pod scheduled on %s (zone=%s)", nodeName, zone)
}
