package attestation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPAttester verifies node attestation via an HTTP REST API.
// It sends a POST request with the node ID and parses the attestation response.
type HTTPAttester struct {
	url    string
	client *http.Client
}

// Option configures an HTTPAttester.
type Option func(*HTTPAttester)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(a *HTTPAttester) {
		a.client.Timeout = d
	}
}

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(a *HTTPAttester) {
		a.client = c
	}
}

// NewHTTPAttester creates an attester that verifies nodes via the given URL.
func NewHTTPAttester(url string, opts ...Option) *HTTPAttester {
	a := &HTTPAttester{
		url:    url,
		client: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// verifyRequest is the JSON body sent to the attestation service.
type verifyRequest struct {
	NodeID string `json:"nodeId"`
}

// verifyResponse is the JSON body returned by the attestation service.
type verifyResponse struct {
	Attested    bool            `json:"attested"`
	TrustAnchor string          `json:"trustAnchor,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// Verify sends a verification request to the attestation service.
// Any error (network, non-2xx, malformed response) returns an error;
// callers must treat errors as attestation failure (fail-closed).
func (a *HTTPAttester) Verify(ctx context.Context, nodeID string) (*Result, error) {
	body, err := json.Marshal(verifyRequest{NodeID: nodeID})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("attestation request to %s: %w", a.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("attestation service returned status %d", resp.StatusCode)
	}

	var vr verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("decode attestation response: %w", err)
	}

	return &Result{
		Attested:    vr.Attested,
		TrustAnchor: vr.TrustAnchor,
		Timestamp:   time.Now(),
		Raw:         vr.Raw,
	}, nil
}
