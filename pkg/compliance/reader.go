// Package compliance reads audit log JSON lines and produces compliance reports.
package compliance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/nexascheduler/nexa/pkg/plugins/audit"
)

// ParseWarning records a malformed line that was skipped during parsing.
type ParseWarning struct {
	Line int
	Err  error
}

// ReadEntries reads DecisionEntry JSON lines from r.
// Malformed lines are skipped and returned as warnings.
// Empty lines are silently ignored.
func ReadEntries(r io.Reader) ([]audit.DecisionEntry, []ParseWarning) {
	var entries []audit.DecisionEntry
	var warnings []ParseWarning
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e audit.DecisionEntry
		if err := json.Unmarshal(line, &e); err != nil {
			warnings = append(warnings, ParseWarning{
				Line: lineNum,
				Err:  fmt.Errorf("line %d: %w", lineNum, err),
			})
			continue
		}
		entries = append(entries, e)
	}
	return entries, warnings
}
