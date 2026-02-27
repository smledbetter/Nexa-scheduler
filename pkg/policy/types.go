// Package policy defines scheduling policy types and configuration.
// Policies control which plugins are enabled, default label values,
// and strictness levels for privacy isolation.
package policy

// Policy is the top-level scheduling policy loaded from a ConfigMap.
type Policy struct {
	Region       RegionPolicy       `json:"regionPolicy"`
	Privacy      PrivacyPolicy      `json:"privacyPolicy"`
	Confidential ConfidentialPolicy `json:"confidentialPolicy"`
}

// RegionPolicy controls region and zone affinity behavior.
type RegionPolicy struct {
	// Enabled controls whether the region plugin filters and scores nodes.
	// When false, all nodes pass the region filter.
	Enabled bool `json:"enabled"`

	// DefaultRegion is applied to pods without a nexa.io/region label.
	// Empty string means no default (pods without the label have no region preference).
	DefaultRegion string `json:"defaultRegion,omitempty"`

	// DefaultZone is applied to pods without a nexa.io/zone label.
	// Empty string means no default.
	DefaultZone string `json:"defaultZone,omitempty"`
}

// PrivacyPolicy controls privacy-aware scheduling behavior.
type PrivacyPolicy struct {
	// Enabled controls whether the privacy plugin filters and scores nodes.
	// When false, all nodes pass the privacy filter.
	Enabled bool `json:"enabled"`

	// DefaultPrivacy is applied to pods without a nexa.io/privacy label.
	// Valid values: "high", "standard", "".
	// Empty string means no default (pods without the label have no privacy requirement).
	DefaultPrivacy string `json:"defaultPrivacy"`

	// StrictOrgIsolation when true enforces org isolation for ALL pods,
	// not just high-privacy pods. This enables cluster-wide org isolation.
	StrictOrgIsolation bool `json:"strictOrgIsolation"`

	// CooldownHours specifies how recently a node must have been wiped
	// to be considered clean for high-privacy workloads.
	// 0 means disabled (any wipe is fresh enough). Requires nexa.io/wipe-timestamp label.
	CooldownHours int `json:"cooldownHours,omitempty"`
}

// ConfidentialPolicy controls confidential compute scheduling behavior.
type ConfidentialPolicy struct {
	// Enabled controls whether the confidential compute plugin filters and scores nodes.
	// When false, all nodes pass the confidential filter.
	Enabled bool `json:"enabled"`

	// RequireTEEForHigh when true requires TEE-capable nodes for privacy=high workloads.
	RequireTEEForHigh bool `json:"requireTEEForHigh"`

	// RequireEncryptedDisk when true requires disk encryption for confidential workloads.
	RequireEncryptedDisk bool `json:"requireEncryptedDisk"`

	// DefaultTEEType is the preferred TEE type when a pod doesn't specify one.
	DefaultTEEType string `json:"defaultTEEType,omitempty"`

	// RequireRuntimeClass when set requires confidential pods to use this runtimeClassName.
	RequireRuntimeClass string `json:"requireRuntimeClass,omitempty"`
}
