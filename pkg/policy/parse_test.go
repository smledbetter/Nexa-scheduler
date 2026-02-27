package policy

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		data      map[string]string
		wantErr   string // substring of expected error, "" if no error
		checkFunc func(t *testing.T, pol *Policy)
	}{
		{
			name: "valid policy — all fields",
			data: map[string]string{
				"policy.json": `{
					"regionPolicy": {"enabled": true, "defaultRegion": "us-west1", "defaultZone": "us-west1-a"},
					"privacyPolicy": {"enabled": true, "defaultPrivacy": "high", "strictOrgIsolation": true}
				}`,
			},
			checkFunc: func(t *testing.T, pol *Policy) {
				if !pol.Region.Enabled {
					t.Error("Region.Enabled = false, want true")
				}
				if pol.Region.DefaultRegion != "us-west1" {
					t.Errorf("Region.DefaultRegion = %q, want %q", pol.Region.DefaultRegion, "us-west1")
				}
				if pol.Region.DefaultZone != "us-west1-a" {
					t.Errorf("Region.DefaultZone = %q, want %q", pol.Region.DefaultZone, "us-west1-a")
				}
				if !pol.Privacy.Enabled {
					t.Error("Privacy.Enabled = false, want true")
				}
				if pol.Privacy.DefaultPrivacy != "high" {
					t.Errorf("Privacy.DefaultPrivacy = %q, want %q", pol.Privacy.DefaultPrivacy, "high")
				}
				if !pol.Privacy.StrictOrgIsolation {
					t.Error("Privacy.StrictOrgIsolation = false, want true")
				}
			},
		},
		{
			name: "valid policy — minimal (defaults to zero values)",
			data: map[string]string{
				"policy.json": `{"regionPolicy": {"enabled": false}, "privacyPolicy": {"enabled": false}}`,
			},
			checkFunc: func(t *testing.T, pol *Policy) {
				if pol.Region.Enabled {
					t.Error("Region.Enabled = true, want false")
				}
				if pol.Privacy.Enabled {
					t.Error("Privacy.Enabled = true, want false")
				}
			},
		},
		{
			name: "valid policy — empty JSON object",
			data: map[string]string{
				"policy.json": `{}`,
			},
			checkFunc: func(t *testing.T, pol *Policy) {
				if pol.Region.Enabled {
					t.Error("Region.Enabled = true, want false")
				}
			},
		},
		{
			name:    "missing policy.json key",
			data:    map[string]string{"other-key": "value"},
			wantErr: "missing required key",
		},
		{
			name:    "empty data map",
			data:    map[string]string{},
			wantErr: "missing required key",
		},
		{
			name: "malformed JSON",
			data: map[string]string{
				"policy.json": `{not valid json`,
			},
			wantErr: "failed to parse",
		},
		{
			name: "invalid defaultPrivacy value",
			data: map[string]string{
				"policy.json": `{"privacyPolicy": {"defaultPrivacy": "secret"}}`,
			},
			wantErr: "invalid",
		},
		{
			name: "valid defaultPrivacy — standard",
			data: map[string]string{
				"policy.json": `{"privacyPolicy": {"defaultPrivacy": "standard"}}`,
			},
			checkFunc: func(t *testing.T, pol *Policy) {
				if pol.Privacy.DefaultPrivacy != "standard" {
					t.Errorf("Privacy.DefaultPrivacy = %q, want %q", pol.Privacy.DefaultPrivacy, "standard")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pol, err := Parse(tt.data)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("Parse() returned nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Parse() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, pol)
			}
		})
	}
}
