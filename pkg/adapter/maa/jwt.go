package maa

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// maaClaims holds the relevant claims from an Azure MAA JWT token.
type maaClaims struct {
	// AttestationType is the type of attestation (e.g., "sevsnpvm").
	AttestationType string `json:"x-ms-attestation-type"`

	// ComplianceStatus indicates whether the platform meets policy (e.g., "azure-compliant-cvm").
	ComplianceStatus string `json:"x-ms-compliance-status"`

	// IsDebuggable indicates whether the SEV-SNP VM has debugging enabled.
	IsDebuggable bool `json:"x-ms-sevsnpvm-is-debuggable"`

	// GuestSVN is the guest security version number.
	GuestSVN int `json:"x-ms-sevsnpvm-guestsvn"`
}

// isCompliant returns true if the MAA claims indicate a trusted, non-debuggable CVM.
func (c *maaClaims) isCompliant() bool {
	if c.IsDebuggable {
		return false
	}
	if c.ComplianceStatus == "" {
		return false
	}
	return true
}

// parseMAAToken extracts claims from an Azure MAA JWT without signature verification.
// Production deployments should validate the JWT signature against MAA's JWKS endpoint.
func parseMAAToken(token string) (*maaClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	// Decode the payload (second part).
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims maaClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal JWT claims: %w", err)
	}

	return &claims, nil
}
