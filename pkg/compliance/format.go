package compliance

import (
	"encoding/json"
	"io"
	"text/template"
)

// FormatJSON writes the report as indented JSON.
func FormatJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

var mdTemplate = template.Must(template.New("report").Parse(`# Compliance Report: {{ .Standard }}

Generated: {{ .GeneratedAt }}
Parse warnings: {{ .Warnings }}
{{ range .Orgs }}
## Organization: {{ .Org }}

Period: {{ .From }} to {{ .To }}

| Metric | Count |
|--------|-------|
| Total decisions | {{ .TotalDecisions }} |
| Compliant | {{ .Compliant }} |
| Violations | {{ len .Violations }} |
| Scheduled | {{ .ScheduledCount }} |
| Failed | {{ .FailedCount }} |
{{ if .Violations }}
### Violations

| Timestamp | Pod | Namespace | Node | Reason |
|-----------|-----|-----------|------|--------|
{{ range .Violations }}| {{ .Timestamp }} | {{ .Pod }} | {{ .Namespace }} | {{ .Node }} | {{ .Reason }} |
{{ end }}{{ else }}
No violations found.
{{ end }}{{ end }}`))

// FormatMarkdown writes the report as a markdown document.
func FormatMarkdown(w io.Writer, r *Report) error {
	return mdTemplate.Execute(w, r)
}
