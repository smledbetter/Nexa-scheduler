// Package maa provides an HTTP adapter between Nexa's attestation controller
// and Microsoft Azure Attestation (MAA). It fetches platform evidence from
// per-node agents and forwards it to MAA for verification.
package maa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// Handler serves the /verify endpoint that bridges Nexa's attestation
// contract to Azure MAA.
type Handler struct {
	maaEndpoint   string
	evidencePort  int
	kubeClient    kubernetes.Interface
	httpClient    *http.Client
	evidenceURLFn func(ip string, port int) string
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(c *http.Client) HandlerOption {
	return func(h *Handler) { h.httpClient = c }
}

// withEvidenceURLFn overrides how evidence URLs are constructed (for testing).
func withEvidenceURLFn(fn func(ip string, port int) string) HandlerOption {
	return func(h *Handler) { h.evidenceURLFn = fn }
}

// NewHandler creates a Handler that verifies nodes against the given MAA endpoint.
func NewHandler(maaEndpoint string, evidencePort int, kubeClient kubernetes.Interface, opts ...HandlerOption) *Handler {
	h := &Handler{
		maaEndpoint:  maaEndpoint,
		evidencePort: evidencePort,
		kubeClient:   kubeClient,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		evidenceURLFn: func(ip string, port int) string {
			return fmt.Sprintf("http://%s:%d/evidence", ip, port)
		},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// verifyRequest matches the Nexa attestation contract.
type verifyRequest struct {
	NodeID string `json:"nodeId"`
}

// verifyResponse matches the Nexa attestation contract.
type verifyResponse struct {
	Attested    bool            `json:"attested"`
	TrustAnchor string          `json:"trustAnchor,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// evidenceResponse is the JSON returned by the per-node evidence agent.
type evidenceResponse struct {
	Report string `json:"report"` // base64-encoded SNP report
}

// maaRequest is the JSON body sent to Azure MAA's /attest/SevSnpVm endpoint.
type maaRequest struct {
	Report string `json:"report"`
}

// ServeHTTP routes requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/verify":
		h.handleVerify(w, r)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req verifyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		klog.ErrorS(err, "failed to decode verify request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.NodeID == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	result, err := h.verify(ctx, req.NodeID)
	if err != nil {
		klog.ErrorS(err, "attestation failed", "nodeId", req.NodeID)
		http.Error(w, fmt.Sprintf("attestation failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		klog.ErrorS(err, "failed to encode response")
	}
}

func (h *Handler) verify(ctx context.Context, nodeID string) (*verifyResponse, error) {
	// Step 1: Look up node's internal IP.
	nodeIP, err := h.getNodeInternalIP(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("get node IP: %w", err)
	}

	// Step 2: Fetch platform evidence from the node's evidence agent.
	evidence, err := h.fetchEvidence(ctx, nodeIP)
	if err != nil {
		return nil, fmt.Errorf("fetch evidence from %s: %w", nodeID, err)
	}

	// Step 3: Send evidence to Azure MAA for verification.
	claims, rawToken, err := h.attestWithMAA(ctx, evidence)
	if err != nil {
		return nil, fmt.Errorf("MAA attestation: %w", err)
	}

	// Step 4: Check MAA claims for compliance.
	attested := claims.isCompliant()

	return &verifyResponse{
		Attested:    attested,
		TrustAnchor: "azure-maa",
		Raw:         rawToken,
	}, nil
}

func (h *Handler) getNodeInternalIP(ctx context.Context, nodeName string) (string, error) {
	node, err := h.kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", nodeName, err)
	}

	for _, addr := range node.Status.Addresses {
		if addr.Type == v1.NodeInternalIP {
			return addr.Address, nil
		}
	}
	return "", fmt.Errorf("node %s has no InternalIP address", nodeName)
}

func (h *Handler) fetchEvidence(ctx context.Context, nodeIP string) (string, error) {
	url := h.evidenceURLFn(nodeIP, h.evidencePort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create evidence request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("evidence request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("evidence agent returned status %d", resp.StatusCode)
	}

	var er evidenceResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&er); err != nil {
		return "", fmt.Errorf("decode evidence response: %w", err)
	}
	if er.Report == "" {
		return "", fmt.Errorf("evidence agent returned empty report")
	}

	return er.Report, nil
}

func (h *Handler) attestWithMAA(ctx context.Context, report string) (*maaClaims, json.RawMessage, error) {
	url := fmt.Sprintf("%s/attest/SevSnpVm?api-version=2022-08-01", h.maaEndpoint)
	body, err := json.Marshal(maaRequest{Report: report})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal MAA request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("create MAA request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("MAA request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("MAA returned status %d", resp.StatusCode)
	}

	var maaResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&maaResp); err != nil {
		return nil, nil, fmt.Errorf("decode MAA response: %w", err)
	}
	if maaResp.Token == "" {
		return nil, nil, fmt.Errorf("MAA returned empty token")
	}

	rawToken, _ := json.Marshal(map[string]string{"token": maaResp.Token})

	claims, err := parseMAAToken(maaResp.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("parse MAA token: %w", err)
	}

	return claims, rawToken, nil
}
