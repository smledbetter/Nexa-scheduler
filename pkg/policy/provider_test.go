package policy

import (
	"errors"
	"testing"
)

func TestStaticProvider(t *testing.T) {
	t.Run("returns policy", func(t *testing.T) {
		pol := &Policy{Region: RegionPolicy{Enabled: true}}
		provider := &StaticProvider{P: pol}

		got, err := provider.GetPolicy()
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}
		if !got.Region.Enabled {
			t.Error("Region.Enabled = false, want true")
		}
	})

	t.Run("returns error", func(t *testing.T) {
		provider := &StaticProvider{Err: errors.New("config unavailable")}

		_, err := provider.GetPolicy()
		if err == nil {
			t.Fatal("GetPolicy() returned nil error, want error")
		}
		if err.Error() != "config unavailable" {
			t.Errorf("GetPolicy() error = %q, want %q", err.Error(), "config unavailable")
		}
	})

	t.Run("returns nil policy and nil error", func(t *testing.T) {
		provider := &StaticProvider{}

		got, err := provider.GetPolicy()
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}
		if got != nil {
			t.Errorf("GetPolicy() = %v, want nil", got)
		}
	})
}
