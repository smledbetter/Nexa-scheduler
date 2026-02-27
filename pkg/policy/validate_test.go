package policy

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		policy  *Policy
		wantErr string
	}{
		{
			name:   "valid — empty defaultPrivacy",
			policy: &Policy{Privacy: PrivacyPolicy{DefaultPrivacy: ""}},
		},
		{
			name:   "valid — standard",
			policy: &Policy{Privacy: PrivacyPolicy{DefaultPrivacy: "standard"}},
		},
		{
			name:   "valid — high",
			policy: &Policy{Privacy: PrivacyPolicy{DefaultPrivacy: "high"}},
		},
		{
			name:    "invalid — unknown privacy level",
			policy:  &Policy{Privacy: PrivacyPolicy{DefaultPrivacy: "secret"}},
			wantErr: `"secret" is invalid`,
		},
		{
			name:    "invalid — typo in privacy level",
			policy:  &Policy{Privacy: PrivacyPolicy{DefaultPrivacy: "High"}},
			wantErr: `"High" is invalid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.policy)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("Validate() returned nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}
