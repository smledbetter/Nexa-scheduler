// Package nodestate manages node labels for the Nexa Scheduler.
// The Node State Controller watches pod lifecycle events and updates
// node labels to reflect workload history and cleanliness state.
package nodestate

const (
	// LabelWiped indicates whether a node has been wiped since its last workload.
	LabelWiped = "nexa.io/wiped"

	// LabelLastWorkloadOrg records the org of the last completed workload on a node.
	LabelLastWorkloadOrg = "nexa.io/last-workload-org"

	// LabelWipeOnComplete indicates the node should be marked dirty after workload completion.
	LabelWipeOnComplete = "nexa.io/wipe-on-complete"

	// LabelWipeTimestamp records when the node was last wiped (RFC3339 format).
	// Set by the operator/automation alongside LabelWiped=true.
	// Cleared by the controller when marking a node dirty.
	LabelWipeTimestamp = "nexa.io/wipe-timestamp"

	// LabelOrg is the pod-level label identifying the organization that owns the workload.
	LabelOrg = "nexa.io/org"

	// LabelTEEAttested indicates whether a node's TEE claim has been verified
	// by a remote attestation service. Set by the attestation controller.
	LabelTEEAttested = "nexa.io/tee-attested"

	// LabelTEEAttestationTime records when the node was last attested (RFC3339 format).
	LabelTEEAttestationTime = "nexa.io/tee-attestation-time"

	// LabelTEETrustAnchor identifies which attestation service verified the node
	// (e.g., "intel-ta", "azure-maa").
	LabelTEETrustAnchor = "nexa.io/tee-trust-anchor"
)
