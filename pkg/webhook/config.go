// Package webhook implements a ValidatingAdmissionWebhook that enforces
// nexa.io/* pod label provenance. It prevents label spoofing by validating
// that namespaces are authorized to set specific org and privacy labels.
package webhook

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds the admission rules loaded from a ConfigMap or file.
type Config struct {
	Rules []NamespaceRule `json:"rules"`
}

// NamespaceRule maps a namespace to its allowed nexa.io label values.
type NamespaceRule struct {
	// Namespace is the namespace name (exact match) or "*" for a wildcard default.
	Namespace string `json:"namespace"`

	// AllowedOrgs lists org values this namespace may set on nexa.io/org.
	// Empty means no org labels are allowed from this namespace.
	AllowedOrgs []string `json:"allowedOrgs"`

	// AllowedPrivacy lists privacy values this namespace may set on nexa.io/privacy.
	// If omitted or empty, defaults to all valid privacy levels ("standard", "high").
	AllowedPrivacy []string `json:"allowedPrivacy,omitempty"`
}

// validPrivacyLevels defines the allowed values for nexa.io/privacy.
var validPrivacyLevels = map[string]bool{
	"standard": true,
	"high":     true,
}

// ParseConfig unmarshals JSON config data and validates it.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid webhook config JSON: %w", err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadConfigFromFile reads and parses a webhook config file.
func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read webhook config %s: %w", path, err)
	}
	return ParseConfig(data)
}

// RuleForNamespace returns the rule for the given namespace, or nil if none matches.
// An exact namespace match takes precedence over a wildcard ("*") rule.
func (c *Config) RuleForNamespace(ns string) *NamespaceRule {
	var wildcard *NamespaceRule
	for i := range c.Rules {
		if c.Rules[i].Namespace == ns {
			return &c.Rules[i]
		}
		if c.Rules[i].Namespace == "*" {
			wildcard = &c.Rules[i]
		}
	}
	return wildcard
}

func validateConfig(cfg *Config) error {
	seen := make(map[string]bool)
	for i, rule := range cfg.Rules {
		if rule.Namespace == "" {
			return fmt.Errorf("rule[%d]: namespace must not be empty", i)
		}
		if seen[rule.Namespace] {
			return fmt.Errorf("rule[%d]: duplicate namespace %q", i, rule.Namespace)
		}
		seen[rule.Namespace] = true

		for j, p := range rule.AllowedPrivacy {
			if !validPrivacyLevels[p] {
				return fmt.Errorf("rule[%d].allowedPrivacy[%d]: invalid privacy level %q; must be \"standard\" or \"high\"", i, j, p)
			}
		}
	}
	return nil
}

func applyDefaults(cfg *Config) {
	for i := range cfg.Rules {
		if len(cfg.Rules[i].AllowedPrivacy) == 0 {
			cfg.Rules[i].AllowedPrivacy = []string{"standard", "high"}
		}
	}
}
