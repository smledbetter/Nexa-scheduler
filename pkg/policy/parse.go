package policy

import (
	"encoding/json"
	"fmt"
)

// ConfigMapKey is the key in the ConfigMap's data that holds the policy JSON.
const ConfigMapKey = "policy.json"

// Parse reads a Policy from ConfigMap data. The data map must contain
// a "policy.json" key with valid JSON. Returns an error if the key is
// missing, the JSON is malformed, or the policy fails validation.
func Parse(data map[string]string) (*Policy, error) {
	raw, ok := data[ConfigMapKey]
	if !ok {
		return nil, fmt.Errorf("ConfigMap missing required key %q", ConfigMapKey)
	}

	var pol Policy
	if err := json.Unmarshal([]byte(raw), &pol); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", ConfigMapKey, err)
	}

	if err := Validate(&pol); err != nil {
		return nil, fmt.Errorf("invalid policy: %w", err)
	}

	return &pol, nil
}
