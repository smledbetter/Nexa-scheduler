// Package audit implements structured JSON audit logging for scheduling decisions.
// PostBind logs successful placements; PostFilter logs scheduling failures with
// per-node rejection reasons.
package audit

import (
	"encoding/json"
	"io"
	"time"
)

// Logger writes structured JSON audit entries to an io.Writer.
// Debug-level entries are only emitted when debug is true.
type Logger struct {
	w     io.Writer
	debug bool
}

// NewLogger creates a Logger that writes JSON lines to w.
// If debug is true, detailed per-node filter reasons are also emitted.
func NewLogger(w io.Writer, debug bool) *Logger {
	return &Logger{w: w, debug: debug}
}

// DecisionEntry is a single structured audit log record.
type DecisionEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Event     string         `json:"event"`
	Pod       PodRef         `json:"pod"`
	Node      string         `json:"node,omitempty"`
	Policy    PolicySnapshot `json:"policy"`
	Filters   []FilterResult `json:"filters,omitempty"`
}

// PodRef identifies a pod using only scheduling-relevant metadata.
// No env vars, secrets, or service account tokens are included.
type PodRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Privacy   string `json:"privacy,omitempty"`
	Region    string `json:"region,omitempty"`
	Zone      string `json:"zone,omitempty"`
	Org       string `json:"org,omitempty"`
}

// PolicySnapshot captures which policies were active at decision time.
type PolicySnapshot struct {
	RegionEnabled  bool `json:"regionEnabled"`
	PrivacyEnabled bool `json:"privacyEnabled"`
}

// FilterResult records a single node rejection with its reason.
type FilterResult struct {
	Node   string `json:"node"`
	Reason string `json:"reason"`
}

// now is a function variable for testing. Production uses time.Now.
var now = time.Now

// LogDecision writes an INFO-level audit entry.
func (l *Logger) LogDecision(entry DecisionEntry) {
	entry.Level = "INFO"
	entry.Timestamp = now().UTC().Format(time.RFC3339)
	l.write(entry)
}

// LogFilterDetail writes a DEBUG-level audit entry with per-node filter reasons.
// Only emitted when the logger is in debug mode.
func (l *Logger) LogFilterDetail(entry DecisionEntry) {
	if !l.debug {
		return
	}
	entry.Level = "DEBUG"
	entry.Timestamp = now().UTC().Format(time.RFC3339)
	l.write(entry)
}

func (l *Logger) write(entry DecisionEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = l.w.Write(data)
}
