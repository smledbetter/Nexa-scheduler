# Nexa Scheduler — GPU Scheduling Architecture Analysis

*Analysis date: 2026-02-27*

## Context

The roadmap (docs/ROADMAP.md, line 209) explicitly removes GPU scheduling:

> Batch/AI scheduling features (GPU topology, gang scheduling, preemption priority) are mature in Volcano, Kueue, and YuniKorn. Building inferior versions of solved problems dilutes focus. Nexa's value is compliance-aware placement, which is complementary to batch schedulers — not competitive with them.

This analysis asks a different question: **what happens when Nexa's privacy and compliance guarantees need to extend to GPU workloads?** That's a gap the batch schedulers don't address.

---

## Where the Current Architecture Fails for GPU Workloads

### 1. GPU VRAM Is Outside the Trust Boundary

The threat model (`docs/threat-model.md`, threat #9) flags this as an accepted risk. CPU TEEs (TDX, SEV-SNP) protect main memory but not GPU VRAM. The confidential compute plugin checks `nexa.io/tee-capable`, `nexa.io/disk-encrypted`, and attestation status — all CPU-side properties. A pod with `privacy: high` requesting `nvidia.com/gpu: 8` passes all confidential compute filters and lands on a TEE node, but its model weights and activations sit in unencrypted VRAM.

This isn't just a documentation gap. The compliance report CLI (`cmd/compliance/`) generates reports claiming confidential compute protection for workloads that are partially unprotected. The audit trail is technically accurate (the *scheduling decision* was compliant) but misleading (the *execution environment* wasn't fully confidential).

### 2. GPU Memory Has No "Wipe" Equivalent

The privacy plugin's core mechanism is node cleanliness: `nexa.io/wiped=true` means the node has been scrubbed between tenants. The node state controller tracks this via pod lifecycle events and label patches. But GPU memory is not scrubbed by anything in this pipeline. When org-alpha's training job finishes and org-beta's inference job lands on the same GPU node:

- CPU memory: wiped (kernel handles this)
- Disk: potentially wiped (if `wipe-on-complete=true`)
- GPU VRAM: **contains residual data from org-alpha** — model weights, gradients, activations

The `nexa.io/wiped` label is a lie for GPU nodes unless something external resets GPU memory. Neither Nexa nor the standard kubelet lifecycle does this.

### 3. Org Isolation Doesn't Account for GPU Sharing

The privacy plugin's strict org isolation checks `nodeInfo.GetPods()` to ensure no other org's pods run on the same node. But with GPU time-slicing (NVIDIA MPS) or MIG partitioning, multiple pods from different orgs can share a *single physical GPU* simultaneously. The node-level isolation check passes (it only looks at pod labels, not GPU assignments), but the actual GPU-level isolation is violated.

### 4. No Resource Awareness Means No GPU-Aware Scoring

Nexa's Score plugins rank nodes by region match and privacy cleanliness. A node with 8 free GPUs and a node with 0 free GPUs get the same Nexa score. The default scheduler's resource scoring handles this, but when Nexa's scores dominate (weight 50 each for region + privacy = 100 total), they can override the default scheduler's resource fit scoring and push pods toward GPU-exhausted nodes that happen to be clean and in-region.

---

## What Would Need to Change

### Layer 1: Awareness (Minimal — extends current label model)

These changes acknowledge GPU existence without fundamentally changing the architecture.

**GPU capability labels on nodes.** The node state controller or an external agent would need to maintain labels like:
- `nexa.io/gpu-present=true`
- `nexa.io/gpu-type=a100` (or h100, etc.)
- `nexa.io/gpu-confidential-compute=true` (for GPUs with HBM encryption, e.g., H100 SXM with confidential computing mode)
- `nexa.io/gpu-memory-wiped=true`

The confidential compute plugin's Filter would add a check: if a pod requests GPU resources AND has `privacy: high`, require `nexa.io/gpu-confidential-compute=true`. This is a ~20-line addition to `confidential.go`'s Filter method and a new label in `nodestate/labels.go`.

**Policy extension.** Add to the confidential policy:
```json
{
  "requireConfidentialGPU": true,
  "gpuWipeRequired": true
}
```

Effort: 1 sprint. No architectural change.

### Layer 2: GPU Wipe Lifecycle (Medium — extends controller model)

The node state controller currently watches pod lifecycle and patches CPU/disk-related labels. For GPU wipe:

**New problem: GPU reset is a privileged node operation.** Unlike CPU memory (handled by the kernel on process exit), GPU VRAM reset requires `nvidia-smi -r` or equivalent, which must run on the node itself. The controller can't do this remotely via the API server.

**Architecture change: DaemonSet agent.** A new per-node DaemonSet (`cmd/gpu-agent/`) that:

1. Watches for pod termination on its node (local kubelet API or downward API)
2. Identifies which GPUs were used (inspect cgroup device assignments or NVIDIA device plugin allocations)
3. Resets those specific GPUs (`nvidia-smi -rgc`, CUDA memory wipe, or full GPU reset)
4. Patches the node label `nexa.io/gpu-memory-wiped=true` with a timestamp
5. The central controller verifies the timestamp freshness (same pattern as attestation)

This is a fundamentally different operational model from the current controller. The current architecture is centralized (one controller watches all nodes). GPU wipe requires a distributed agent on every GPU node. The node state controller becomes a *verifier* of claims made by node-local agents, not the sole source of truth.

**Trust implication:** The GPU agent runs with elevated privileges (GPU device access). It self-reports wipe status via node labels — the same self-reporting trust gap that remote attestation (Sprint 17) was designed to close for TEE. You'd need a GPU attestation equivalent, and that doesn't exist in the ecosystem yet.

Effort: 2-3 sprints. New binary, new DaemonSet chart, new trust model.

### Layer 3: GPU-Level Isolation (Hard — breaks the node-as-unit-of-isolation model)

The entire Nexa architecture treats the **node** as the isolation boundary. Filter decides per-node. Score ranks nodes. Labels live on nodes. The controller reconciles nodes. This works because CPU and disk are node-level resources.

GPUs break this. A single node can have 8 GPUs, and with MIG, each GPU can be partitioned into up to 7 instances. The isolation boundary for GPU workloads is the **GPU device** (or MIG slice), not the node.

**What changes:**

**The privacy plugin needs device-level state, not just node-level state.** "Is this node clean?" becomes "Is GPU 3 on this node clean?" The current `nodeInfo.GetPods()` check tells you which orgs have pods on the node, but not which GPUs those pods are using. You'd need to cross-reference pod GPU assignments (from the device plugin or DRA allocation) with org labels.

**Node labels can't represent per-device state.** You could encode it (`nexa.io/gpu-0-wiped=true`, `nexa.io/gpu-1-org=alpha`), but this is fragile — label keys are limited to 63 characters, and the number of GPUs per node varies. This is where the NexaNodeState CRD becomes mandatory:

```yaml
apiVersion: nexa.io/v1
kind: NexaNodeState
metadata:
  name: worker-gpu-01
spec:
  gpuDevices:
  - index: 0
    type: h100
    confidentialCompute: true
    wiped: true
    wipeTimestamp: "2026-02-27T10:00:00Z"
    lastOrg: "alpha"
  - index: 1
    type: h100
    confidentialCompute: true
    wiped: false
    lastOrg: "beta"
    migPartitions:
    - slice: "3g.40gb"
      wiped: true
      lastOrg: "beta"
```

**The Filter plugin can't make GPU-level decisions in the current framework.** The k8s scheduler framework's Filter extension point is called per-node. It returns "schedulable" or "unschedulable" for the whole node. You can't say "schedulable on GPU 0 and 2, but not GPU 1." GPU device assignment happens later, in the device plugin or DRA allocator, which runs *after* the scheduler's Filter/Score/Bind cycle.

This is the fundamental architectural tension: **Nexa makes privacy decisions at Filter time, but GPU assignment happens at Bind time.** By the time you know which specific GPU the pod gets, the scheduling decision is already made.

**Possible approaches:**

1. **Reserve + PreBind hook.** Add a Reserve plugin that claims specific GPU devices (via the NexaNodeState CRD) before Bind. If the claimed GPU fails privacy checks, Unreserve and retry. This adds a round-trip but keeps privacy correctness. Problem: races with the device plugin, which also assigns GPUs independently.

2. **DRA integration (k8s 1.31+).** Dynamic Resource Allocation gives the scheduler visibility into device-level claims *during* scheduling. Nexa could implement a DRA driver that adds privacy constraints to GPU claims. This is the "right" long-term answer but couples Nexa to a rapidly evolving k8s API (DRA graduated to beta in 1.31, still changing).

3. **Node-level GPU homogeneity constraint.** Simpler: require that all GPUs on a node belong to the same org at any time. This reduces GPU isolation to node isolation (which Nexa already handles) at the cost of utilization. A `nexa.io/gpu-exclusive=true` label plus strict org isolation effectively prevents GPU sharing across orgs. This is what most security-sensitive deployments do today anyway.

Effort: Option 3 is 1 sprint. Options 1-2 are 3-5 sprints each with significant k8s API surface risk.

### Layer 4: Kueue/Batch Scheduler Coordination (Hard — distributed system problem)

Currently Kueue and Nexa coordinate implicitly: Kueue controls admission (suspend/unsuspend), Nexa controls placement. This works because Kueue doesn't care *where* a pod runs, and Nexa doesn't care *when* it runs.

GPU scheduling breaks this independence. Kueue manages GPU quotas (how many GPUs each team can use). Nexa manages GPU privacy (which GPUs are clean for which org). These constraints interact:

- Kueue admits a job for org-alpha requesting 4 GPUs
- Nexa finds only 2 clean GPUs on compliant nodes
- The job is unsuspended but can't be scheduled
- It sits Pending indefinitely — neither Kueue nor Nexa knows why

**What changes:**

The systems need bidirectional awareness. Options:

1. **Nexa-aware ResourceFlavors.** Kueue's ResourceFlavor concept already supports `nodeLabels`. If Nexa's privacy labels are included in flavor definitions, Kueue's quota accounting considers only compliant nodes. But this is static — it doesn't account for dynamic wipe state.

2. **Capacity feedback loop.** Nexa exposes "effective GPU capacity per org" as a metric or API. Kueue's quota system references this instead of raw node capacity. This requires a new component (capacity calculator) that combines node GPU counts, wipe state, org assignments, and confidential compute status into an effective available count.

3. **Co-scheduling protocol.** A webhook or controller that intercepts Kueue's admission decision and pre-validates against Nexa's constraints before unsuspending the job. If Nexa can't place it, Kueue keeps it suspended. This avoids the "admitted but unschedulable" limbo.

Effort: 2-4 sprints depending on approach. Option 1 is operational (no code), option 3 is a new controller.

---

## Capability Gap Summary

| Capability | Current State | Gap for GPU |
|---|---|---|
| Node-level privacy isolation | Solid (Filter + labels + controller) | Works if GPUs treated as node-exclusive |
| Device-level privacy isolation | Not modeled | Requires CRD state + DRA or Reserve plugin |
| Confidential compute verification | CPU TEE only | Needs GPU CC detection (H100 CC mode) |
| Memory wipe lifecycle | CPU/disk only | Needs GPU agent DaemonSet + new trust model |
| Org isolation enforcement | Node-level pod scan | Misses GPU sharing (MPS/MIG) |
| Compliance audit trail | Accurate for CPU workloads | Misleading for GPU workloads (claims CC coverage that doesn't extend to VRAM) |
| Batch scheduler coordination | Independent (Kueue suspend/unsuspend) | Breaks when GPU privacy constraints reduce effective capacity |
| Resource-aware scoring | None (label-only) | Nexa scores can override resource fit, pushing pods to GPU-exhausted nodes |

---

## Recommendation

The cheapest and most defensible approach is **option 3 from Layer 3: enforce node-level GPU exclusivity** and extend the existing node isolation model to cover GPUs. This avoids the device-level complexity entirely, aligns with how security-sensitive GPU deployments actually work (dedicated GPU nodes per tenant), and requires only ~1 sprint of work: GPU capability labels, a policy flag, and a Filter check.

The per-device isolation path (Layers 2-4) is architecturally interesting but solves a problem that most target customers avoid by dedicating GPU nodes to tenants. It's the kind of work that should be demand-driven, not speculative.
