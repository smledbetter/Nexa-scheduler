package webhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name:  "valid config with two namespaces",
			input: `{"rules":[{"namespace":"alpha","allowedOrgs":["alpha"]},{"namespace":"beta","allowedOrgs":["beta"]}]}`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if len(cfg.Rules) != 2 {
					t.Fatalf("want 2 rules, got %d", len(cfg.Rules))
				}
				if cfg.Rules[0].Namespace != "alpha" {
					t.Errorf("rule[0].Namespace = %q, want alpha", cfg.Rules[0].Namespace)
				}
			},
		},
		{
			name:  "empty rules array is valid",
			input: `{"rules":[]}`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if len(cfg.Rules) != 0 {
					t.Fatalf("want 0 rules, got %d", len(cfg.Rules))
				}
			},
		},
		{
			name:    "empty namespace rejected",
			input:   `{"rules":[{"namespace":"","allowedOrgs":["a"]}]}`,
			wantErr: "namespace must not be empty",
		},
		{
			name:    "invalid privacy level rejected",
			input:   `{"rules":[{"namespace":"ns","allowedOrgs":[],"allowedPrivacy":["extreme"]}]}`,
			wantErr: "invalid privacy level \"extreme\"",
		},
		{
			name:    "duplicate namespace rejected",
			input:   `{"rules":[{"namespace":"ns","allowedOrgs":[]},{"namespace":"ns","allowedOrgs":[]}]}`,
			wantErr: "duplicate namespace \"ns\"",
		},
		{
			name:  "wildcard namespace valid",
			input: `{"rules":[{"namespace":"*","allowedOrgs":["any"]}]}`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.Rules[0].Namespace != "*" {
					t.Errorf("rule[0].Namespace = %q, want *", cfg.Rules[0].Namespace)
				}
			},
		},
		{
			name:    "malformed JSON rejected",
			input:   `{broken`,
			wantErr: "invalid webhook config JSON",
		},
		{
			name:  "omitted privacy defaults to all valid levels",
			input: `{"rules":[{"namespace":"ns","allowedOrgs":["a"]}]}`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if len(cfg.Rules[0].AllowedPrivacy) != 2 {
					t.Fatalf("want 2 default privacy levels, got %d", len(cfg.Rules[0].AllowedPrivacy))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig([]byte(tt.input))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestRuleForNamespace(t *testing.T) {
	cfg := &Config{
		Rules: []NamespaceRule{
			{Namespace: "alpha", AllowedOrgs: []string{"alpha"}},
			{Namespace: "*", AllowedOrgs: []string{"default-org"}},
		},
	}

	t.Run("exact match", func(t *testing.T) {
		rule := cfg.RuleForNamespace("alpha")
		if rule == nil || rule.Namespace != "alpha" {
			t.Fatal("expected exact match for alpha")
		}
	})

	t.Run("wildcard fallback", func(t *testing.T) {
		rule := cfg.RuleForNamespace("unknown")
		if rule == nil || rule.Namespace != "*" {
			t.Fatal("expected wildcard match for unknown namespace")
		}
	})

	t.Run("no rules config", func(t *testing.T) {
		empty := &Config{}
		rule := empty.RuleForNamespace("anything")
		if rule != nil {
			t.Fatal("expected nil for empty config")
		}
	})
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"rules":[{"namespace":"ns","allowedOrgs":["a"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(cfg.Rules))
	}

	_, err = LoadConfigFromFile(filepath.Join(dir, "nonexistent.json"))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
