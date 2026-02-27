package compliance

import (
	"testing"
	"time"

	"github.com/nexascheduler/nexa/pkg/plugins/audit"
)

func init() {
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	}
}

func makeEntry(event, name, ns, org, privacy, region, node string, privacyEnabled, regionEnabled bool) audit.DecisionEntry {
	return audit.DecisionEntry{
		Timestamp: "2026-01-15T10:00:00Z",
		Level:     "INFO",
		Event:     event,
		Pod: audit.PodRef{
			Name:      name,
			Namespace: ns,
			Org:       org,
			Privacy:   privacy,
			Region:    region,
		},
		Node:   node,
		Policy: audit.PolicySnapshot{PrivacyEnabled: privacyEnabled, RegionEnabled: regionEnabled},
	}
}

func TestGenerateReport_HIPAA_Violations(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "", "us-west1", "n1", true, true),
		makeEntry("scheduled", "pod2", "ns1", "alpha", "high", "", "n2", true, true),
	}
	report, err := GenerateReport(entries, nil, "", "hipaa", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orgs) != 1 {
		t.Fatalf("expected 1 org, got %d", len(report.Orgs))
	}
	org := report.Orgs[0]
	if len(org.Violations) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(org.Violations))
	}
	if org.Compliant != 0 {
		t.Errorf("expected 0 compliant, got %d", org.Compliant)
	}
}

func TestGenerateReport_Compliant(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "high", "us-west1", "n1", true, true),
	}
	report, err := GenerateReport(entries, nil, "", "hipaa", "", "")
	if err != nil {
		t.Fatal(err)
	}
	org := report.Orgs[0]
	if org.Compliant != 1 {
		t.Errorf("expected 1 compliant, got %d", org.Compliant)
	}
	if len(org.Violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(org.Violations))
	}
}

func TestGenerateReport_OrgFilter(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "high", "us-west1", "n1", true, true),
		makeEntry("scheduled", "pod2", "ns1", "beta", "high", "us-west1", "n2", true, true),
	}
	report, err := GenerateReport(entries, nil, "alpha", "hipaa", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orgs) != 1 {
		t.Fatalf("expected 1 org, got %d", len(report.Orgs))
	}
	if report.Orgs[0].Org != "alpha" {
		t.Errorf("expected org 'alpha', got %q", report.Orgs[0].Org)
	}
}

func TestGenerateReport_TimeRange(t *testing.T) {
	e1 := makeEntry("scheduled", "pod1", "ns1", "alpha", "high", "us-west1", "n1", true, true)
	e1.Timestamp = "2026-01-10T00:00:00Z"
	e2 := makeEntry("scheduled", "pod2", "ns1", "alpha", "high", "us-west1", "n2", true, true)
	e2.Timestamp = "2026-01-20T00:00:00Z"

	report, err := GenerateReport([]audit.DecisionEntry{e1, e2}, nil, "", "hipaa", "2026-01-15T00:00:00Z", "2026-01-25T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if report.Orgs[0].TotalDecisions != 1 {
		t.Errorf("expected 1 decision in range, got %d", report.Orgs[0].TotalDecisions)
	}
}

func TestGenerateReport_EmptyInput(t *testing.T) {
	report, err := GenerateReport(nil, nil, "", "hipaa", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orgs) != 0 {
		t.Errorf("expected 0 orgs, got %d", len(report.Orgs))
	}
	if report.Standard != "HIPAA" {
		t.Errorf("expected standard 'HIPAA', got %q", report.Standard)
	}
}

func TestGenerateReport_UnknownStandard(t *testing.T) {
	_, err := GenerateReport(nil, nil, "", "pci", "", "")
	if err == nil {
		t.Fatal("expected error for unknown standard")
	}
}

func TestGenerateReport_SkipsFilterDetails(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "high", "us-west1", "n1", true, true),
		makeEntry("filter_details", "pod1", "ns1", "alpha", "high", "us-west1", "", true, true),
	}
	report, err := GenerateReport(entries, nil, "", "hipaa", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Orgs[0].TotalDecisions != 1 {
		t.Errorf("expected 1 decision (filter_details excluded), got %d", report.Orgs[0].TotalDecisions)
	}
}

func TestGenerateReport_SOC2_PolicyDisabled(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "", "", "n1", false, false),
	}
	report, err := GenerateReport(entries, nil, "", "soc2", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orgs[0].Violations) != 1 {
		t.Fatalf("expected 1 SOC2 violation, got %d", len(report.Orgs[0].Violations))
	}
	if report.Orgs[0].Violations[0].Reason != "no compliance policies enabled at scheduling time" {
		t.Errorf("unexpected reason: %s", report.Orgs[0].Violations[0].Reason)
	}
}

func TestGenerateReport_GDPR_NoRegion(t *testing.T) {
	entries := []audit.DecisionEntry{
		makeEntry("scheduled", "pod1", "ns1", "alpha", "high", "", "n1", true, true),
	}
	report, err := GenerateReport(entries, nil, "", "gdpr", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orgs[0].Violations) != 1 {
		t.Fatalf("expected 1 GDPR violation, got %d", len(report.Orgs[0].Violations))
	}
}
