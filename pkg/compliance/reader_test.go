package compliance

import (
	"strings"
	"testing"
)

func TestReadEntries_Valid(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-01-15T00:00:00Z","level":"INFO","event":"scheduled","pod":{"name":"a","namespace":"ns1","org":"alpha"},"node":"n1","policy":{"regionEnabled":true,"privacyEnabled":true}}`,
		`{"timestamp":"2026-01-16T00:00:00Z","level":"INFO","event":"scheduling_failed","pod":{"name":"b","namespace":"ns1","org":"alpha"},"policy":{"regionEnabled":true,"privacyEnabled":true}}`,
		`{"timestamp":"2026-01-17T00:00:00Z","level":"INFO","event":"scheduled","pod":{"name":"c","namespace":"ns2","org":"beta"},"node":"n2","policy":{"regionEnabled":false,"privacyEnabled":true}}`,
	}, "\n")

	entries, warnings := ReadEntries(strings.NewReader(input))
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(warnings))
	}
	if entries[0].Pod.Name != "a" {
		t.Errorf("expected pod name 'a', got %q", entries[0].Pod.Name)
	}
	if entries[1].Event != "scheduling_failed" {
		t.Errorf("expected event 'scheduling_failed', got %q", entries[1].Event)
	}
	if entries[2].Pod.Org != "beta" {
		t.Errorf("expected org 'beta', got %q", entries[2].Pod.Org)
	}
}

func TestReadEntries_Empty(t *testing.T) {
	entries, warnings := ReadEntries(strings.NewReader(""))
	if entries != nil {
		t.Fatalf("expected nil entries, got %d", len(entries))
	}
	if warnings != nil {
		t.Fatalf("expected nil warnings, got %d", len(warnings))
	}
}

func TestReadEntries_MixedValidInvalid(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-01-15T00:00:00Z","level":"INFO","event":"scheduled","pod":{"name":"a","namespace":"ns1"},"node":"n1","policy":{"regionEnabled":true,"privacyEnabled":true}}`,
		`not json at all`,
		``,
		`{"timestamp":"2026-01-16T00:00:00Z","level":"INFO","event":"scheduled","pod":{"name":"b","namespace":"ns1"},"node":"n2","policy":{"regionEnabled":true,"privacyEnabled":true}}`,
	}, "\n")

	entries, warnings := ReadEntries(strings.NewReader(input))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Line != 2 {
		t.Errorf("expected warning on line 2, got line %d", warnings[0].Line)
	}
}

func TestReadEntries_AllMalformed(t *testing.T) {
	input := "bad line 1\nbad line 2\nbad line 3\n"
	entries, warnings := ReadEntries(strings.NewReader(input))
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %d", len(warnings))
	}
}
