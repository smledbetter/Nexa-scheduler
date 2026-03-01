// Package evidence provides an HTTP agent that reads SEV-SNP attestation
// reports from the local platform and serves them to the MAA adapter.
package evidence

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"k8s.io/klog/v2"
)

// Agent serves platform attestation evidence over HTTP.
type Agent struct {
	reportPath string
}

// NewAgent creates an Agent that reads SNP reports from the given path.
// On Azure confidential VMs, the default path is /dev/sev-guest.
func NewAgent(reportPath string) *Agent {
	return &Agent{reportPath: reportPath}
}

type evidenceResponse struct {
	Report string `json:"report"` // base64-encoded SNP report
}

// ServeHTTP routes requests to the appropriate handler.
func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/evidence":
		a.handleEvidence(w, r)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func (a *Agent) handleEvidence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data, err := os.ReadFile(a.reportPath)
	if err != nil {
		klog.ErrorS(err, "failed to read attestation report", "path", a.reportPath)
		http.Error(w, fmt.Sprintf("read report: %v", err), http.StatusInternalServerError)
		return
	}
	if len(data) == 0 {
		klog.ErrorS(nil, "attestation report is empty", "path", a.reportPath)
		http.Error(w, "empty report", http.StatusInternalServerError)
		return
	}

	resp := evidenceResponse{
		Report: base64.StdEncoding.EncodeToString(data),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		klog.ErrorS(err, "failed to encode evidence response")
	}
}
