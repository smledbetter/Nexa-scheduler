package compliance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testReport() *Report {
	return &Report{
		GeneratedAt: "2026-02-27T12:00:00Z",
		Standard:    "HIPAA",
		Warnings:    1,
		Orgs: []OrgReport{
			{
				Org:            "alpha",
				Standard:       "HIPAA",
				From:           "2026-01-01T00:00:00Z",
				To:             "2026-02-01T00:00:00Z",
				TotalDecisions: 3,
				Compliant:      2,
				ScheduledCount: 2,
				FailedCount:    1,
				Violations: []Violation{
					{
						Timestamp: "2026-01-15T10:00:00Z",
						Pod:       "pod1",
						Namespace: "ns1",
						Node:      "n1",
						Reason:    "missing privacy label (required by HIPAA)",
					},
				},
			},
		},
	}
}

func TestFormatJSON_RoundTrip(t *testing.T) {
	r := testReport()
	var buf bytes.Buffer
	if err := FormatJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Standard != "HIPAA" {
		t.Errorf("expected standard 'HIPAA', got %q", decoded.Standard)
	}
	if len(decoded.Orgs) != 1 || decoded.Orgs[0].Compliant != 2 {
		t.Errorf("unexpected org data after round trip")
	}
}

func TestFormatMarkdown_Headers(t *testing.T) {
	r := testReport()
	var buf bytes.Buffer
	if err := FormatMarkdown(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Compliance Report: HIPAA",
		"## Organization: alpha",
		"### Violations",
		"| Timestamp |",
		"missing privacy label",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q", want)
		}
	}
}

func TestFormatMarkdown_NoViolations(t *testing.T) {
	r := &Report{
		GeneratedAt: "2026-02-27T12:00:00Z",
		Standard:    "HIPAA",
		Orgs: []OrgReport{
			{Org: "alpha", TotalDecisions: 1, Compliant: 1},
		},
	}
	var buf bytes.Buffer
	if err := FormatMarkdown(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No violations found.") {
		t.Error("expected 'No violations found.' in output")
	}
}
