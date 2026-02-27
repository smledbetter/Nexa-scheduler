//go:build smoke

// Package smoke provides Kind-based end-to-end smoke tests for the Nexa scheduler.
package smoke

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	clusterName = "nexa-smoke"
	helmRelease = "nexa-smoke"
	namespace   = "nexa-system"
)

// tb is the subset of testing.TB used by setup helpers.
// Both *testing.T and mainT satisfy this interface.
type tb interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
	Name() string
}

// repoRoot returns the absolute path to the repository root.
func repoRoot(t tb) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (no go.mod found)")
			return "" // unreachable; satisfies compiler
		}
		dir = parent
	}
}

// runCmd executes a command and returns combined output. Fails the test on error.
func runCmd(t tb, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %s %v failed: %v\noutput: %s", name, args, err, out.String())
	}
	return out.String()
}

// runCmdNoFail executes a command and returns combined output and error.
func runCmdNoFail(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// createCluster creates a Kind cluster with the given config.
func createCluster(t tb) {
	t.Helper()
	root := repoRoot(t)
	configPath := filepath.Join(root, "test", "smoke", "testdata", "kind-config.yaml")
	runCmd(t, "kind", "create", "cluster",
		"--name", clusterName,
		"--config", configPath,
		"--wait", "120s",
	)
}

// deleteCluster deletes the Kind cluster.
func deleteCluster() {
	_, _ = runCmdNoFail("kind", "delete", "cluster", "--name", clusterName)
}

// buildAndLoadImage builds the Docker image and loads it into the Kind cluster.
func buildAndLoadImage(t tb) {
	t.Helper()
	root := repoRoot(t)
	runCmd(t, "docker", "build", "-t", "nexascheduler/nexa-scheduler:smoke", root)
	runCmd(t, "kind", "load", "docker-image", "nexascheduler/nexa-scheduler:smoke", "--name", clusterName)
}

// installChart installs the Helm chart with smoke test overrides.
func installChart(t tb) {
	t.Helper()
	root := repoRoot(t)
	chartPath := filepath.Join(root, "deploy", "helm", "nexa-scheduler")
	runCmd(t, "helm", "install", helmRelease, chartPath,
		"--namespace", namespace,
		"--create-namespace",
		"--set", "image.tag=smoke",
		"--set", "image.pullPolicy=Never",
		"--wait",
		"--timeout", "120s",
	)
}

// uninstallChart removes the Helm release.
func uninstallChart() {
	_, _ = runCmdNoFail("helm", "uninstall", helmRelease, "--namespace", namespace)
}

// labelWorkers applies the node label matrix to the 3 Kind worker nodes.
func labelWorkers(t tb) {
	t.Helper()
	client := kubeClient(t)
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}

	// Collect worker nodes (non-control-plane).
	var workers []string
	for _, n := range nodes.Items {
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; !ok {
			workers = append(workers, n.Name)
		}
	}
	if len(workers) < 3 {
		t.Fatalf("expected 3 workers, got %d", len(workers))
	}

	// Worker-0: us-west1 / us-west1-a / wiped / org=alpha
	runCmd(t, "kubectl", "label", "node", workers[0],
		"nexa.io/region=us-west1",
		"nexa.io/zone=us-west1-a",
		"nexa.io/wiped=true",
		"nexa.io/last-workload-org=alpha",
		"--overwrite",
	)
	// Worker-1: us-west1 / us-west1-b / not wiped / no org
	runCmd(t, "kubectl", "label", "node", workers[1],
		"nexa.io/region=us-west1",
		"nexa.io/zone=us-west1-b",
		"--overwrite",
	)
	// Worker-2: eu-west1 / eu-west1-a / wiped / org=beta
	runCmd(t, "kubectl", "label", "node", workers[2],
		"nexa.io/region=eu-west1",
		"nexa.io/zone=eu-west1-a",
		"nexa.io/wiped=true",
		"nexa.io/last-workload-org=beta",
		"--overwrite",
	)
}

// kubeClient returns a Kubernetes client configured for the Kind cluster.
func kubeClient(t tb) kubernetes.Interface {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("create kube client: %v", err)
	}
	return client
}

// createTestNamespace creates an isolated namespace for a test.
func createTestNamespace(t *testing.T, client kubernetes.Interface) string {
	t.Helper()
	ns := fmt.Sprintf("smoke-%s-%d", t.Name(), time.Now().UnixNano()%10000)
	// Sanitize: k8s namespace must be lowercase DNS label
	ns = strings.ToLower(strings.ReplaceAll(ns, "/", "-"))
	if len(ns) > 63 {
		ns = ns[:63]
	}
	_, err := client.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = client.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})
	return ns
}

// makePod creates a pod spec for the Nexa scheduler with the given labels.
func makePod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			SchedulerName: "nexa-scheduler",
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   "busybox:latest",
					Command: []string{"sleep", "300"},
				},
			},
		},
	}
}

// waitForPodScheduled waits until the pod is bound to a node (has a nodeName).
func waitForPodScheduled(t *testing.T, client kubernetes.Interface, ns, name string, timeout time.Duration) string {
	t.Helper()
	var nodeName string
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if pod.Spec.NodeName != "" {
			nodeName = pod.Spec.NodeName
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("pod %s/%s not scheduled within %v: %v", ns, name, timeout, err)
	}
	return nodeName
}

// waitForPodPending verifies the pod stays Pending (unscheduled) for the given duration.
func waitForPodPending(t *testing.T, client kubernetes.Interface, ns, name string, checkDuration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(checkDuration)
	for time.Now().Before(deadline) {
		pod, err := client.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if pod.Spec.NodeName != "" {
			t.Fatalf("pod %s/%s was scheduled on %s, expected to stay Pending", ns, name, pod.Spec.NodeName)
		}
		time.Sleep(2 * time.Second)
	}
}

// applyCRD installs the NexaPolicy CRD into the cluster.
func applyCRD(t tb) {
	t.Helper()
	root := repoRoot(t)
	crdPath := filepath.Join(root, "deploy", "manifests", "nexapolicy-crd.yaml")
	runCmd(t, "kubectl", "apply", "-f", crdPath)
}

// applyNexaPolicy creates a NexaPolicy resource from inline YAML via kubectl.
func applyNexaPolicy(t *testing.T, yamlContent string) {
	t.Helper()
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("apply NexaPolicy failed: %v\noutput: %s", err, out.String())
	}
}

// deleteNexaPolicy removes the default NexaPolicy resource if it exists.
func deleteNexaPolicy(t *testing.T) {
	t.Helper()
	_, _ = runCmdNoFail("kubectl", "delete", "nexapolicy", "default", "-n", namespace, "--ignore-not-found")
}

const (
	webhookRelease = "nexa-webhook-smoke"
)

// buildAndLoadWebhookImage builds the webhook Docker image and loads it into Kind.
func buildAndLoadWebhookImage(t tb) {
	t.Helper()
	root := repoRoot(t)
	runCmd(t, "docker", "build", "-t", "nexascheduler/nexa-webhook:smoke", "-f", filepath.Join(root, "Dockerfile.webhook"), root)
	runCmd(t, "kind", "load", "docker-image", "nexascheduler/nexa-webhook:smoke", "--name", clusterName)
}

// generateWebhookCerts creates a self-signed CA and serving certificate for the webhook.
// Returns the CA bundle (PEM) for injection into the ValidatingWebhookConfiguration.
func generateWebhookCerts(t tb) (caBundle string) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "nexa-webhook-certs")
	_ = os.MkdirAll(dir, 0o755)

	svcDNS := fmt.Sprintf("%s-nexa-webhook.%s.svc", webhookRelease, namespace)

	// Generate CA key and cert.
	runCmd(t, "openssl", "req", "-x509", "-newkey", "rsa:2048",
		"-keyout", filepath.Join(dir, "ca.key"),
		"-out", filepath.Join(dir, "ca.crt"),
		"-days", "1", "-nodes", "-subj", "/CN=nexa-webhook-ca")

	// Generate server key and CSR.
	runCmd(t, "openssl", "req", "-newkey", "rsa:2048",
		"-keyout", filepath.Join(dir, "tls.key"),
		"-out", filepath.Join(dir, "tls.csr"),
		"-nodes", "-subj", fmt.Sprintf("/CN=%s", svcDNS),
		"-addext", fmt.Sprintf("subjectAltName=DNS:%s", svcDNS))

	// Sign the server cert with the CA.
	// Write a temporary ext file for SAN.
	extContent := fmt.Sprintf("subjectAltName=DNS:%s", svcDNS)
	extFile := filepath.Join(dir, "ext.cnf")
	if err := os.WriteFile(extFile, []byte(extContent), 0o644); err != nil {
		t.Fatalf("write ext file: %v", err)
	}
	runCmd(t, "openssl", "x509", "-req",
		"-in", filepath.Join(dir, "tls.csr"),
		"-CA", filepath.Join(dir, "ca.crt"),
		"-CAkey", filepath.Join(dir, "ca.key"),
		"-CAcreateserial",
		"-out", filepath.Join(dir, "tls.crt"),
		"-days", "1",
		"-extfile", extFile)

	// Create the TLS secret in the namespace.
	runCmd(t, "kubectl", "create", "secret", "tls", "nexa-webhook-tls",
		"--cert", filepath.Join(dir, "tls.crt"),
		"--key", filepath.Join(dir, "tls.key"),
		"-n", namespace)

	// Read CA bundle for webhook config.
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	return string(caPEM)
}

// installWebhookChart installs the webhook Helm chart with smoke test overrides.
func installWebhookChart(t tb, caBundle string) {
	t.Helper()
	root := repoRoot(t)
	chartPath := filepath.Join(root, "deploy", "helm", "nexa-webhook")

	// Base64 encode the CA bundle for the webhook config.
	caBundleB64 := base64Encode([]byte(caBundle))

	rulesJSON := `[{"namespace":"alpha-workloads","allowedOrgs":["alpha"],"allowedPrivacy":["standard","high"]},{"namespace":"*","allowedOrgs":["default-org"],"allowedPrivacy":["standard","high"]}]`

	runCmd(t, "helm", "install", webhookRelease, chartPath,
		"--namespace", namespace,
		"--set", "image.tag=smoke",
		"--set", "image.pullPolicy=Never",
		"--set", "tls.caBundle="+caBundleB64,
		"--set-json", "rules="+rulesJSON,
		"--wait",
		"--timeout", "120s",
	)
}

// uninstallWebhookChart removes the webhook Helm release.
func uninstallWebhookChart() {
	_, _ = runCmdNoFail("helm", "uninstall", webhookRelease, "--namespace", namespace)
}

// base64Encode returns the base64-encoded string of the input.
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// createWebhookTestNamespace creates a namespace with the webhook-enabled label.
func createWebhookTestNamespace(t *testing.T, client kubernetes.Interface, nsName string) {
	t.Helper()
	_, err := client.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			Labels: map[string]string{
				"nexa.io/webhook": "enabled",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace %s: %v", nsName, err)
	}
	t.Cleanup(func() {
		_ = client.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})
}

// schedulerLogs returns the logs from the Nexa scheduler pod.
func schedulerLogs(t *testing.T, client kubernetes.Interface) string {
	t.Helper()
	pods, err := client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=nexa-scheduler",
	})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("could not find scheduler pod: %v", err)
	}
	out := runCmd(t, "kubectl", "logs", "-n", namespace, pods.Items[0].Name)
	return out
}

// --- Kueue integration helpers ---

const (
	kueueVersion = "v0.16.1"
	kueueNS      = "kueue-system"
)

// installKueue installs Kueue from the upstream release manifest.
func installKueue(t tb) {
	t.Helper()
	url := fmt.Sprintf(
		"https://github.com/kubernetes-sigs/kueue/releases/download/%s/manifests.yaml",
		kueueVersion,
	)
	runCmd(t, "kubectl", "apply", "--server-side", "-f", url)
	waitForKueueReady(t)
}

// uninstallKueue removes Kueue from the cluster.
func uninstallKueue() {
	url := fmt.Sprintf(
		"https://github.com/kubernetes-sigs/kueue/releases/download/%s/manifests.yaml",
		kueueVersion,
	)
	_, _ = runCmdNoFail("kubectl", "delete", "-f", url, "--ignore-not-found")
}

// waitForKueueReady waits until the Kueue controller-manager pod is Running and Ready.
func waitForKueueReady(t tb) {
	t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		pods, err := kubeClient(t).CoreV1().Pods(kueueNS).List(
			context.Background(), metav1.ListOptions{
				LabelSelector: "control-plane=controller-manager",
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
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("kueue controller not ready within 180s")
}

// makeJob creates a batch/v1 Job targeting the Nexa scheduler.
// The Job is created suspended (Kueue manages unsuspending on admission).
func makeJob(name string, labels map[string]string) *batchv1.Job {
	suspend := true
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: batchv1.JobSpec{
			Suspend: &suspend,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SchedulerName: "nexa-scheduler",
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   "busybox:latest",
							Command: []string{"sleep", "300"},
						},
					},
				},
			},
		},
	}
}

// applyKueueResource creates a Kueue resource from inline YAML via kubectl.
func applyKueueResource(t tb, yamlContent string) {
	t.Helper()
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("apply Kueue resource failed: %v\noutput: %s", err, out.String())
	}
}

// deleteKueueResource deletes a Kueue resource by kind, name, and optional namespace.
func deleteKueueResource(kind, name, ns string) {
	args := []string{"delete", kind, name, "--ignore-not-found"}
	if ns != "" {
		args = append(args, "-n", ns)
	}
	_, _ = runCmdNoFail("kubectl", args...)
}

// createResourceFlavor creates a Kueue ResourceFlavor (cluster-scoped).
func createResourceFlavor(t tb, name string, nodeLabels map[string]string) {
	t.Helper()
	labelsYAML := ""
	for k, v := range nodeLabels {
		labelsYAML += fmt.Sprintf("    %s: %q\n", k, v)
	}
	spec := ""
	if labelsYAML != "" {
		spec = fmt.Sprintf("spec:\n  nodeLabels:\n%s", labelsYAML)
	}
	yaml := fmt.Sprintf(`apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: %s
%s`, name, spec)
	applyKueueResource(t, yaml)
}

// setupKueueResources creates a ClusterQueue and LocalQueue for a test.
// Returns after both resources exist. Cleanup is registered via t.Cleanup.
func setupKueueResources(t *testing.T, ns, suffix, flavorName string, cpuQuota string) {
	t.Helper()
	cqName := fmt.Sprintf("cq-%s", suffix)
	lqName := fmt.Sprintf("lq-%s", suffix)

	cqYAML := fmt.Sprintf(`apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  name: %s
spec:
  namespaceSelector: {}
  resourceGroups:
    - coveredResources: ["cpu", "memory"]
      flavors:
        - name: %s
          resources:
            - name: cpu
              nominalQuota: "%s"
            - name: memory
              nominalQuota: "4Gi"
`, cqName, flavorName, cpuQuota)
	applyKueueResource(t, cqYAML)
	t.Cleanup(func() { deleteKueueResource("clusterqueue", cqName, "") })

	lqYAML := fmt.Sprintf(`apiVersion: kueue.x-k8s.io/v1beta1
kind: LocalQueue
metadata:
  name: %s
  namespace: %s
spec:
  clusterQueue: %s
`, lqName, ns, cqName)
	applyKueueResource(t, lqYAML)
	t.Cleanup(func() { deleteKueueResource("localqueue", lqName, ns) })
}

// waitForWorkloadAdmitted waits for Kueue to admit the workload (unsuspend the Job).
func waitForWorkloadAdmitted(t *testing.T, client kubernetes.Interface, ns, jobName string, timeout time.Duration) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			job, err := client.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			if job.Spec.Suspend != nil && !*job.Spec.Suspend {
				return true, nil
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("job %s/%s not admitted by Kueue within %v: %v", ns, jobName, timeout, err)
	}
}

// waitForJobSuspended verifies the Job stays suspended for the given duration.
func waitForJobSuspended(t *testing.T, client kubernetes.Interface, ns, jobName string, checkDuration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(checkDuration)
	for time.Now().Before(deadline) {
		job, err := client.BatchV1().Jobs(ns).Get(context.Background(), jobName, metav1.GetOptions{})
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if job.Spec.Suspend != nil && !*job.Spec.Suspend {
			t.Fatalf("job %s/%s was unsuspended, expected to stay suspended", ns, jobName)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForJobPodScheduled waits for a Job's pod to be scheduled by Nexa.
// Returns the node name.
func waitForJobPodScheduled(t *testing.T, client kubernetes.Interface, ns, jobName string, timeout time.Duration) string {
	t.Helper()
	var nodeName string
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", jobName),
			})
			if err != nil || len(pods.Items) == 0 {
				return false, nil
			}
			for _, pod := range pods.Items {
				if pod.Spec.NodeName != "" {
					nodeName = pod.Spec.NodeName
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("job %s/%s pod not scheduled within %v: %v", ns, jobName, timeout, err)
	}
	return nodeName
}

// waitForJobPodPending verifies a Job's pod stays Pending (unscheduled) for the given duration.
func waitForJobPodPending(t *testing.T, client kubernetes.Interface, ns, jobName string, checkDuration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(checkDuration)
	for time.Now().Before(deadline) {
		pods, err := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("job-name=%s", jobName),
		})
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, pod := range pods.Items {
			if pod.Spec.NodeName != "" {
				t.Fatalf("job pod %s/%s was scheduled on %s, expected to stay Pending",
					ns, pod.Name, pod.Spec.NodeName)
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// makeJobWithResources creates a Job with explicit CPU and memory resource requests.
func makeJobWithResources(name string, labels map[string]string, cpu, memory string) *batchv1.Job {
	job := makeJob(name, labels)
	job.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(memory),
		},
	}
	return job
}
