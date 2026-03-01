package evidence

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAgent_Evidence_ValidReport(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "sev-guest")
	reportData := []byte("fake-snp-attestation-report-bytes")
	if err := os.WriteFile(reportPath, reportData, 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(reportPath)
	req := httptest.NewRequest(http.MethodGet, "/evidence", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp evidenceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(resp.Report)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != string(reportData) {
		t.Errorf("report = %q, want %q", string(decoded), string(reportData))
	}
}

func TestAgent_Evidence_MissingFile(t *testing.T) {
	a := NewAgent("/nonexistent/path/sev-guest")
	req := httptest.NewRequest(http.MethodGet, "/evidence", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestAgent_Evidence_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "sev-guest")
	if err := os.WriteFile(reportPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(reportPath)
	req := httptest.NewRequest(http.MethodGet, "/evidence", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestAgent_Evidence_BadMethod(t *testing.T) {
	a := NewAgent("/dev/null")
	req := httptest.NewRequest(http.MethodPost, "/evidence", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestAgent_Healthz(t *testing.T) {
	a := NewAgent("/dev/null")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAgent_NotFound(t *testing.T) {
	a := NewAgent("/dev/null")
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
