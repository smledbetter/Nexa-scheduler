// Package attestation provides remote attestation verification for TEE-capable nodes.
// It defines the Attester interface and data types used by the attestation controller
// to verify node TEE claims against a remote attestation service.
package attestation

import (
	"context"
	"encoding/json"
	"time"
)

// Attester verifies a node's TEE attestation against a remote service.
type Attester interface {
	// Verify checks the attestation state of the given node.
	// Returns a Result indicating whether attestation succeeded.
	// On error, callers must treat the node as unattested (fail-closed).
	Verify(ctx context.Context, nodeID string) (*Result, error)
}

// Result holds the outcome of an attestation verification.
type Result struct {
	// Attested is true if the node passed attestation verification.
	Attested bool `json:"attested"`

	// TrustAnchor identifies the attestation service that verified the node
	// (e.g., "intel-ta", "azure-maa").
	TrustAnchor string `json:"trustAnchor,omitempty"`

	// Timestamp is when the attestation was performed.
	Timestamp time.Time `json:"timestamp"`

	// Raw is the unprocessed attestation response for audit logging.
	Raw json.RawMessage `json:"raw,omitempty"`
}
