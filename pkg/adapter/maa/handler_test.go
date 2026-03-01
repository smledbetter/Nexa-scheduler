package maa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testNode(name, ip string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{Type: v1.NodeInternalIP, Address: ip},
			},
		},
	}
}

// newTestHandler creates a Handler wired to mock evidence and MAA servers.
// The evidenceURLFn is overridden to route to the evidence test server
// regardless of the node IP.
func newTestHandler(t *testing.T, evidenceHandler, maaHandler http.Handler) (*Handler, func()) {
	t.Helper()

	var evidenceServer *httptest.Server
	if evidenceHandler != nil {
		evidenceServer = httptest.NewServer(evidenceHandler)
	}

	var maaURL string
	var maaServer *httptest.Server
	if maaHandler != nil {
		maaServer = httptest.NewServer(maaHandler)
		maaURL = maaServer.URL
	} else {
		maaURL = "http://127.0.0.1:1" // unreachable
	}

	kubeClient := fake.NewSimpleClientset(testNode("worker-1", "10.0.0.1"))

	opts := []HandlerOption{}
	if evidenceServer != nil {
		evidenceURL := evidenceServer.URL
		opts = append(opts, withEvidenceURLFn(func(_ string, _ int) string {
			return evidenceURL + "/evidence"
		}))
	}

	h := NewHandler(maaURL, 9443, kubeClient, opts...)

	cleanup := func() {
		if evidenceServer != nil {
			evidenceServer.Close()
		}
		if maaServer != nil {
			maaServer.Close()
		}
	}

	return h, cleanup
}

func TestHandler_Verify_Success(t *testing.T) {
	token := makeTestToken(map[string]any{
		"x-ms-attestation-type":       "sevsnpvm",
		"x-ms-compliance-status":      "azure-compliant-cvm",
		"x-ms-sevsnpvm-is-debuggable": false,
	})

	evidence := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report":"dGVzdC1yZXBvcnQ"}`))
	})

	maa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/attest/SevSnpVm" {
			t.Errorf("unexpected MAA path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		resp, _ := json.Marshal(map[string]string{"token": token})
		_, _ = w.Write(resp)
	})

	h, cleanup := newTestHandler(t, evidence, maa)
	defer cleanup()

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp verifyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Attested {
		t.Error("Attested = false, want true")
	}
	if resp.TrustAnchor != "azure-maa" {
		t.Errorf("TrustAnchor = %q, want %q", resp.TrustAnchor, "azure-maa")
	}
}

func TestHandler_Verify_EvidenceFailure(t *testing.T) {
	evidence := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	h, cleanup := newTestHandler(t, evidence, nil)
	defer cleanup()

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_Verify_MAARejectsAttestation(t *testing.T) {
	token := makeTestToken(map[string]any{
		"x-ms-attestation-type":       "sevsnpvm",
		"x-ms-compliance-status":      "azure-compliant-cvm",
		"x-ms-sevsnpvm-is-debuggable": true,
	})

	evidence := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report":"dGVzdA"}`))
	})

	maa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp, _ := json.Marshal(map[string]string{"token": token})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	})

	h, cleanup := newTestHandler(t, evidence, maa)
	defer cleanup()

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp verifyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Attested {
		t.Error("Attested = true, want false for debuggable VM")
	}
}

func TestHandler_Verify_MAAUnreachable(t *testing.T) {
	evidence := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report":"dGVzdA"}`))
	})

	// Pass nil for MAA handler → unreachable endpoint.
	h, cleanup := newTestHandler(t, evidence, nil)
	defer cleanup()

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_Verify_NodeNotFound(t *testing.T) {
	kubeClient := fake.NewSimpleClientset() // no nodes
	h := NewHandler("http://unused", 9443, kubeClient)

	body := `{"nodeId":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_Verify_EmptyNodeID(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	h := NewHandler("http://unused", 9443, kubeClient)

	body := `{"nodeId":""}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Verify_BadMethod(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	h := NewHandler("http://unused", 9443, kubeClient)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandler_Healthz(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	h := NewHandler("http://unused", 9443, kubeClient)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandler_Verify_ContextCancellation(t *testing.T) {
	evidence := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	h, cleanup := newTestHandler(t, evidence, nil)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_Verify_MAAEmptyToken(t *testing.T) {
	evidence := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report":"dGVzdA"}`))
	})

	maa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":""}`))
	})

	h, cleanup := newTestHandler(t, evidence, maa)
	defer cleanup()

	body := `{"nodeId":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
