package maa

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func makeTestToken(claims any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payloadB64 + "." + sig
}

func TestParseMAAToken(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantErr   bool
		checkFunc func(t *testing.T, c *maaClaims)
	}{
		{
			name: "valid compliant token",
			token: makeTestToken(map[string]any{
				"x-ms-attestation-type":       "sevsnpvm",
				"x-ms-compliance-status":      "azure-compliant-cvm",
				"x-ms-sevsnpvm-is-debuggable": false,
				"x-ms-sevsnpvm-guestsvn":      2,
			}),
			checkFunc: func(t *testing.T, c *maaClaims) {
				if c.AttestationType != "sevsnpvm" {
					t.Errorf("AttestationType = %q, want %q", c.AttestationType, "sevsnpvm")
				}
				if c.ComplianceStatus != "azure-compliant-cvm" {
					t.Errorf("ComplianceStatus = %q, want %q", c.ComplianceStatus, "azure-compliant-cvm")
				}
				if c.IsDebuggable {
					t.Error("IsDebuggable = true, want false")
				}
				if c.GuestSVN != 2 {
					t.Errorf("GuestSVN = %d, want 2", c.GuestSVN)
				}
				if !c.isCompliant() {
					t.Error("isCompliant() = false, want true")
				}
			},
		},
		{
			name: "debuggable VM -- not compliant",
			token: makeTestToken(map[string]any{
				"x-ms-attestation-type":       "sevsnpvm",
				"x-ms-compliance-status":      "azure-compliant-cvm",
				"x-ms-sevsnpvm-is-debuggable": true,
			}),
			checkFunc: func(t *testing.T, c *maaClaims) {
				if c.isCompliant() {
					t.Error("isCompliant() = true, want false for debuggable VM")
				}
			},
		},
		{
			name: "empty compliance status -- not compliant",
			token: makeTestToken(map[string]any{
				"x-ms-attestation-type":       "sevsnpvm",
				"x-ms-compliance-status":      "",
				"x-ms-sevsnpvm-is-debuggable": false,
			}),
			checkFunc: func(t *testing.T, c *maaClaims) {
				if c.isCompliant() {
					t.Error("isCompliant() = true, want false for empty compliance status")
				}
			},
		},
		{
			name:    "invalid JWT -- too few parts",
			token:   "only.two",
			wantErr: true,
		},
		{
			name:    "invalid JWT -- bad base64",
			token:   "a.!!!invalid!!!.c",
			wantErr: true,
		},
		{
			name:    "invalid JWT -- bad JSON",
			token:   "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := parseMAAToken(tt.token)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, claims)
			}
		})
	}
}
