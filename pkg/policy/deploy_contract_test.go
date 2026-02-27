package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDeployConfigMapNameContract verifies that the ConfigMap name in deployment
// manifests matches DefaultConfigMapName. This prevents the Sprint 6 regression
// where the Helm chart created a ConfigMap with a different name than what the
// plugins expect.
func TestDeployConfigMapNameContract(t *testing.T) {
	root := repoRoot(t)

	t.Run("helm template uses DefaultConfigMapName", func(t *testing.T) {
		path := filepath.Join(root, "deploy", "helm", "nexa-scheduler", "templates", "configmap-policy.yaml")
		content := readFile(t, path)
		// The Helm template should contain the hardcoded name, not a release-prefixed template expression.
		if !strings.Contains(content, "name: "+DefaultConfigMapName) {
			t.Errorf("Helm configmap-policy.yaml does not contain 'name: %s'\nGot:\n%s", DefaultConfigMapName, content)
		}
	})

	t.Run("raw manifest uses DefaultConfigMapName", func(t *testing.T) {
		path := filepath.Join(root, "deploy", "manifests", "configmap-policy.yaml")
		content := readFile(t, path)
		if !strings.Contains(content, "name: "+DefaultConfigMapName) {
			t.Errorf("raw configmap-policy.yaml does not contain 'name: %s'\nGot:\n%s", DefaultConfigMapName, content)
		}
	})

	t.Run("DefaultConfigMapName is nexa-scheduler-config", func(t *testing.T) {
		// Explicit contract: if someone changes the constant, this test forces
		// them to update the deploy files too.
		if DefaultConfigMapName != "nexa-scheduler-config" {
			t.Errorf("DefaultConfigMapName = %q, want %q", DefaultConfigMapName, "nexa-scheduler-config")
		}
	})
}

// repoRoot walks up from the current test file to find the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

// readFile reads a file and fails the test if it doesn't exist.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
