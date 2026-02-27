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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var testClient kubernetes.Interface

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
