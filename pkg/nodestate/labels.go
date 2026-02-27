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

	// LabelOrg is the pod-level label identifying the organization that owns the workload.
	LabelOrg = "nexa.io/org"
)
