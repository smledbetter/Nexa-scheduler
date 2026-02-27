package attestation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPAttester_Verify(t *testing.T) {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		wantErr      bool
		wantAttested bool
		wantAnchor   string
	}{
		{
			name: "successful attestation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"attested":true,"trustAnchor":"intel-ta"}`))
			},
			wantAttested: true,
			wantAnchor:   "intel-ta",
		},
		{
			name: "attestation failed",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"attested":false}`))
			},
			wantAttested: false,
		},
		{
			name: "non-2xx status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: true,
		},
		{
			name: "malformed JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`not json`))
			},
			wantErr: true,
		},
		{
			name: "empty response body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
			},
			wantErr: true,
		},
		{
			name: "attestation with raw evidence",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"attested":true,"trustAnchor":"azure-maa","raw":{"quote":"abc123"}}`))
			},
			wantAttested: true,
			wantAnchor:   "azure-maa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			attester := NewHTTPAttester(srv.URL, WithTimeout(5*time.Second))
			result, err := attester.Verify(context.Background(), "node-1")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Attested != tt.wantAttested {
				t.Errorf("attested = %v, want %v", result.Attested, tt.wantAttested)
			}
			if result.TrustAnchor != tt.wantAnchor {
				t.Errorf("trustAnchor = %q, want %q", result.TrustAnchor, tt.wantAnchor)
			}
			if result.Timestamp.IsZero() {
				t.Error("timestamp should not be zero")
			}
		})
	}
}

func TestHTTPAttester_VerifyRequestFormat(t *testing.T) {
	var gotContentType string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		_, _ = w.Write([]byte(`{"attested":true}`))
	}))
	defer srv.Close()

	attester := NewHTTPAttester(srv.URL)
	_, err := attester.Verify(context.Background(), "test-node-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if string(gotBody) != `{"nodeId":"test-node-42"}` {
		t.Errorf("body = %q, want %q", gotBody, `{"nodeId":"test-node-42"}`)
	}
}

func TestHTTPAttester_ConnectionRefused(t *testing.T) {
	attester := NewHTTPAttester("http://127.0.0.1:1", WithTimeout(1*time.Second))
	_, err := attester.Verify(context.Background(), "node-1")
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

func TestHTTPAttester_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = w.Write([]byte(`{"attested":true}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	attester := NewHTTPAttester(srv.URL)
	_, err := attester.Verify(ctx, "node-1")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestNewHTTPAttester_Defaults(t *testing.T) {
	a := NewHTTPAttester("https://example.com/attest")
	if a.url != "https://example.com/attest" {
		t.Errorf("url = %q, want %q", a.url, "https://example.com/attest")
	}
	if a.client.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", a.client.Timeout)
	}
}

func TestNewHTTPAttester_WithOptions(t *testing.T) {
	custom := &http.Client{Timeout: 10 * time.Second}
	a := NewHTTPAttester("https://example.com", WithHTTPClient(custom))
	if a.client != custom {
		t.Error("WithHTTPClient did not replace client")
	}

	a2 := NewHTTPAttester("https://example.com", WithTimeout(45*time.Second))
	if a2.client.Timeout != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", a2.client.Timeout)
	}
}
