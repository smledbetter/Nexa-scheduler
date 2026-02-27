package compliance

import (
	"fmt"
	"sort"
	"time"

	"github.com/nexascheduler/nexa/pkg/plugins/audit"
)

// Standard represents a compliance standard with its requirements.
type Standard struct {
	Name           string
	RequirePrivacy bool
	RequireRegion  bool
	RequireAudit   bool
}

// Standards maps standard keys to their definitions.
var Standards = map[string]Standard{
	"hipaa": {Name: "HIPAA", RequirePrivacy: true, RequireRegion: true, RequireAudit: true},
	"soc2":  {Name: "SOC 2", RequirePrivacy: false, RequireRegion: false, RequireAudit: true},
	"gdpr":  {Name: "GDPR", RequirePrivacy: false, RequireRegion: true, RequireAudit: true},
}

// Violation describes a single compliance violation.
type Violation struct {
	Timestamp string `json:"timestamp"`
	Pod       string `json:"pod"`
	Namespace string `json:"namespace"`
	Node      string `json:"node,omitempty"`
	Reason    string `json:"reason"`
}

// OrgReport is the per-org section of a compliance report.
type OrgReport struct {
	Org            string      `json:"org"`
	Standard       string      `json:"standard"`
	From           string      `json:"from"`
	To             string      `json:"to"`
	TotalDecisions int         `json:"totalDecisions"`
	Compliant      int         `json:"compliant"`
	Violations     []Violation `json:"violations"`
	ScheduledCount int         `json:"scheduledCount"`
	FailedCount    int         `json:"failedCount"`
}

// Report is the top-level compliance report.
type Report struct {
	GeneratedAt string      `json:"generatedAt"`
	Standard    string      `json:"standard"`
	Orgs        []OrgReport `json:"orgs"`
	Warnings    int         `json:"parseWarnings"`
}

// nowFunc is overridable for testing.
var nowFunc = time.Now

// GenerateReport filters entries by org and time range, then checks compliance
// against the specified standard. Both compliant and violating entries are
// included so auditors have proof of compliance, not just violations.
func GenerateReport(entries []audit.DecisionEntry, warnings []ParseWarning, org, standard, from, to string) (*Report, error) {
	std, ok := Standards[standard]
	if !ok {
		return nil, fmt.Errorf("unknown standard %q; valid standards: hipaa, soc2, gdpr", standard)
	}

	filtered := filterEntries(entries, org, from, to)
	groups := groupByOrg(filtered)

	var orgReports []OrgReport
	for orgName, orgEntries := range groups {
		orgReports = append(orgReports, buildOrgReport(orgName, std, from, to, orgEntries))
	}
	sort.Slice(orgReports, func(i, j int) bool {
		return orgReports[i].Org < orgReports[j].Org
	})

	return &Report{
		GeneratedAt: nowFunc().UTC().Format(time.RFC3339),
		Standard:    std.Name,
		Orgs:        orgReports,
		Warnings:    len(warnings),
	}, nil
}

// filterEntries keeps only scheduled/scheduling_failed events matching org and time range.
func filterEntries(entries []audit.DecisionEntry, org, from, to string) []audit.DecisionEntry {
	var result []audit.DecisionEntry
	for _, e := range entries {
		if e.Event != "scheduled" && e.Event != "scheduling_failed" {
			continue
		}
		if org != "" && e.Pod.Org != org {
			continue
		}
		if from != "" && e.Timestamp < from {
			continue
		}
		if to != "" && e.Timestamp > to {
			continue
		}
		result = append(result, e)
	}
	return result
}

// groupByOrg groups entries by their pod's org label.
func groupByOrg(entries []audit.DecisionEntry) map[string][]audit.DecisionEntry {
	groups := make(map[string][]audit.DecisionEntry)
	for _, e := range entries {
		groups[e.Pod.Org] = append(groups[e.Pod.Org], e)
	}
	return groups
}

func buildOrgReport(org string, std Standard, from, to string, entries []audit.DecisionEntry) OrgReport {
	r := OrgReport{
		Org:      org,
		Standard: std.Name,
		From:     from,
		To:       to,
	}
	for _, e := range entries {
		r.TotalDecisions++
		if e.Event == "scheduled" {
			r.ScheduledCount++
		} else {
			r.FailedCount++
		}
		if reason, violated := checkViolation(e, std); violated {
			r.Violations = append(r.Violations, Violation{
				Timestamp: e.Timestamp,
				Pod:       e.Pod.Name,
				Namespace: e.Pod.Namespace,
				Node:      e.Node,
				Reason:    reason,
			})
		} else {
			r.Compliant++
		}
	}
	return r
}

func checkViolation(entry audit.DecisionEntry, std Standard) (string, bool) {
	if std.RequirePrivacy {
		if entry.Pod.Privacy == "" {
			return "missing privacy label (required by " + std.Name + ")", true
		}
		if !entry.Policy.PrivacyEnabled {
			return "privacy policy disabled at scheduling time", true
		}
	}
	if std.RequireRegion {
		if entry.Pod.Region == "" {
			return "missing region label (required by " + std.Name + ")", true
		}
		if !entry.Policy.RegionEnabled {
			return "region policy disabled at scheduling time", true
		}
	}
	if std.RequireAudit && !entry.Policy.PrivacyEnabled && !entry.Policy.RegionEnabled {
		return "no compliance policies enabled at scheduling time", true
	}
	return "", false
}
