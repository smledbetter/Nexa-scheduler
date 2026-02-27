# Nexa Scheduler

## 1. Vision

Compliance-aware workload placement for Kubernetes. Nexa answers one question: *given this workload's sensitivity requirements, which node is safe to run it on?*

> *"Run your workloads where they belong — safely, automatically, and with a full audit trail."*

---

## 2. Problem Statement

In shared or multi-tenant Kubernetes environments:
- Sensitive workloads (healthcare, legal, finance) risk co-location with untrusted neighbors.
- Data residency and compliance (HIPAA, GDPR, SOC2) are hard to enforce at scheduling time.
- Default schedulers optimize for utilization, not isolation or data sovereignty.
- Compliance officers have no evidence chain for *where* workloads ran and *why*.

This leads to:
- Over-provisioning (dedicated clusters per team/org) to avoid co-tenancy risk.
- Manual policy enforcement that doesn't scale.
- Compliance gaps that surface during audits, not before.
- Distrust in shared infrastructure, blocking consolidation savings.

---

## 3. Target Users

- **Platform Engineers** managing multi-tenant clusters who need automated placement policy.
- **ML Engineers** running private inference/training jobs on shared GPU or CPU pools.
- **Compliance Officers** who need auditable evidence that workloads met isolation requirements.
- **Research Consortia** sharing compute across institutions with data sovereignty constraints.

---

## 4. Unique Value Proposition (UVP)

> **Nexa Scheduler** is a compliance-aware placement layer for Kubernetes that enforces privacy, isolation, and data sovereignty at scheduling time — with a complete audit trail from admission through placement to compliance report.

### How Nexa differs from adjacent tools

| Tool | What it decides | Nexa's relationship |
|------|----------------|---------------------|
| **Kueue / Volcano / YuniKorn** | *When* does this job run? (queuing, admission, fairness, GPU topology) | **Complement.** Kueue admits the job, Nexa places it safely. Tested co-deployment with zero shared state. |
| **OPA Gatekeeper** | *Can* this pod exist? (admission-time policy) | **Different decision point.** Gatekeeper validates at admission. Nexa enforces at scheduling time — after admission, before placement. |
| **Native k8s (taints/affinities)** | Binary allow/deny per node | **Superset.** Nexa adds scored placement (nuanced, not binary), temporal freshness, org isolation, and audit evidence. |

### Key differentiators

- **Enforcement at the placement boundary.** Not admission-time policy (too early to know node state) or runtime policy (too late to prevent co-location). Nexa operates at exactly the right moment: when the scheduler chooses a node.
- **Complete evidence chain.** Admission validation (webhook) → scheduling enforcement (Filter/Score plugins) → structured audit log → compliance report CLI. A compliance officer can produce SOC2/HIPAA/GDPR artifacts without parsing JSON.
- **Hardware trust.** TEE-capable node scheduling, runtimeClass enforcement, disk encryption requirements, and temporal wipe freshness (node cooldown). Fail-closed on missing or malformed attestation signals.
- **Kueue-native.** Designed to run alongside Kueue, not replace it. Kueue handles "when" and "how much." Nexa handles "where safely." Platform teams don't choose between batch orchestration and compliance.
- **Zero external dependencies.** Native Go rules engine (no OPA/Rego), Kubernetes Scheduling Framework (in-process, no HTTP extender), standard k8s label semantics. Nothing to learn except the `nexa.io/*` label contract.

---

## 5. Core Features

| Feature | Description |
|---------|-------------|
| **Region & Zone Affinity** | Filter and score nodes by geographic labels (`nexa.io/region`, `nexa.io/zone`). Enforce data residency at scheduling time. |
| **Privacy-Aware Scheduling** | Org-based anti-affinity, node cleanliness requirements (`nexa.io/wiped`), and scored placement (wiped > idle > busy-same-org). |
| **Confidential Compute** | Filter nodes by TEE capability (`nexa.io/tee`: `tdx`, `sev-snp`), disk encryption, and runtimeClass. Fail-closed on missing labels. |
| **Node Cooldown** | Temporal wipe freshness via `nexa.io/wipe-timestamp`. Configurable cooldown period — reject nodes with stale wipes. |
| **Admission Webhook** | ValidatingAdmissionWebhook enforces `nexa.io/*` label provenance. Pods can only set org/privacy labels if the namespace is authorized. Fail-closed. |
| **Policy via CRD + ConfigMap** | `NexaPolicy` CRD with ConfigMap fallback. Composite provider (CRD-first, fail-closed on malformed CRD). Hot-reloadable. |
| **Node State Controller** | Separate binary watches pod lifecycle, manages `nexa.io/*` node labels (wiped, last-workload-org, wipe-timestamp). |
| **Audit Logging** | Structured JSON logs of every placement decision: pod, selected node, filtered nodes with reasons, policies applied. |
| **Compliance Report CLI** | Offline tool reads audit logs and produces compliance artifacts: workload inventory, node placement map, isolation compliance, policy timeline. Supports `--standard hipaa/soc2/gdpr`. |
| **Prometheus Metrics** | Scheduling duration, filter results, score distribution, isolation violations, policy evaluations. |

---

## 6. Architecture Overview

```
                    ┌──────────────────────┐
                    │  Kubernetes API Server │
                    └──────────┬───────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
              v                v                v
    ┌─────────────────┐ ┌───────────┐ ┌──────────────────┐
    │ Admission Webhook│ │  Nexa     │ │ Node State       │
    │ (label provenance│ │  Scheduler│ │ Controller       │
    │  validation)     │ │  Plugins  │ │ (label manager)  │
    └─────────────────┘ └─────┬─────┘ └──────────────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
              v               v               v
        ┌──────────┐   ┌──────────┐   ┌──────────┐
        │ Region   │   │ Privacy  │   │Confidential│
        │ Filter + │   │ Filter + │   │ Filter +  │
        │ Score    │   │ Score    │   │ Score     │
        └──────────┘   └──────────┘   └───────────┘
              │               │               │
              v               v               v
        ┌─────────────────────────────────────────┐
        │ Audit PostBind → Structured JSON Log    │
        │                  → Compliance Report CLI │
        └─────────────────────────────────────────┘
```

### Components

| Component | Binary | Purpose |
|-----------|--------|---------|
| **Scheduler Plugins** | `cmd/scheduler/` | Region, Privacy, Confidential (Filter+Score) and Audit (PostBind) plugins running in-process via Kubernetes Scheduling Framework |
| **Node State Controller** | `cmd/controller/` | Watches pod lifecycle, manages `nexa.io/*` node labels. Separate binary — scheduler never needs node write access. |
| **Admission Webhook** | `cmd/webhook/` | ValidatingAdmissionWebhook for label provenance. Separate binary — failure isolation from scheduler. |
| **Compliance CLI** | `cmd/compliance/` | Offline report generator. Zero runtime coupling to the scheduler. |
| **Policy Engine** | `pkg/policy/` | CompositeProvider: CRD-first, ConfigMap fallback, fail-closed on malformed CRD. |

---

## 7. Technical Stack

| Layer | Decision |
|-------|----------|
| **Scheduler Framework** | Kubernetes Scheduling Framework (out-of-tree, in-process). Not Scheduler Extender — Framework has lower latency and richer extension points. |
| **Language** | Go 1.23+ |
| **Policy Engine** | Native Go rules engine. Not OPA — policies are simple predicates that map directly to Filter/Score plugins. OPA can be added later if policy complexity demands it. |
| **Configuration** | NexaPolicy CRD (v1alpha1) with ConfigMap fallback |
| **Observability** | Prometheus metrics + structured JSON logging (klog/v2) |
| **Node State** | Labels (not taints) — enables scored placement, not just binary exclusion |
| **Deployment** | Helm chart (3 subcharts: scheduler, node-controller, webhook) + raw YAML manifests |

---

## 8. Example Workflow

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: medical-ai-inference
  labels:
    nexa.io/org: "hospital-a"
    nexa.io/privacy: "high"
    nexa.io/region: "us-central1"
    nexa.io/confidential: "required"
spec:
  schedulerName: nexa-scheduler
  runtimeClassName: kata-cc
  containers:
    - name: analyzer
      image: medical-llm:latest
```

**What happens:**
1. **Webhook** validates label provenance — is `hospital-a` authorized in this namespace?
2. **Region Filter** rejects nodes not in `us-central1`.
3. **Privacy Filter** rejects dirty nodes, nodes used by other orgs, and nodes with stale wipes (cooldown).
4. **Confidential Filter** rejects non-TEE nodes, nodes without disk encryption, and nodes not running `kata-cc` runtime.
5. **Region/Privacy/Confidential Score** ranks surviving nodes (exact zone match, recently wiped, matching TEE type preferred).
6. **Audit PostBind** logs: structured JSON with pod, selected node, filtered nodes with reasons, policies applied.
7. **Compliance CLI** (later, offline): `nexa-report --org hospital-a --standard hipaa` produces audit evidence.

---

## 9. Success Metrics

| Metric | Target | Status |
|--------|--------|--------|
| High-privacy jobs correctly isolated | >= 99% | Enforced via fail-closed Filter plugins |
| Scheduler decision latency | < 50ms per pod | In-process plugins, no HTTP calls |
| Node wipe verification rate | 100% (logged) | Audit PostBind logs every decision |
| Install time (Helm) | < 5 minutes | 3-subchart Helm install |
| Compliance report generation | Offline, no cluster access needed | CLI reads audit JSON from file/stdin |
| Smoke test coverage | All scheduling paths tested on real cluster | 18 Kind-based scenarios (ConfigMap, CRD, webhook, Kueue, confidential) |

---

## 10. What Nexa Is Not

- **Not a batch scheduler.** GPU topology, gang scheduling, preemption, and quota management are solved by Kueue, Volcano, and YuniKorn. Nexa complements them.
- **Not an admission controller.** OPA Gatekeeper validates policy at admission time. Nexa enforces placement policy at scheduling time — a different decision boundary.
- **Not a runtime security tool.** Nexa makes placement decisions. Runtime enforcement (seccomp, AppArmor, Falco) operates after placement.
- **Not multi-cluster.** Single-cluster scheduling. Federation and multi-cluster placement are separate concerns.
- **Not a UI.** Use Grafana for dashboards, Loki for log search. Nexa exposes Prometheus metrics and structured JSON logs.

---

## 11. Known Limitations

- **TEE labels are self-reported.** Without remote attestation, `nexa.io/confidential=true` is a policy signal, not a cryptographic guarantee. The threat model documents this gap and the trust boundary.
- **No runtime verification.** Nexa verifies node state at scheduling time. It does not verify post-scheduling behavior (e.g., that a wipe actually occurred). Operators must ensure their node lifecycle tooling is trustworthy.
- **No mislabeling detection.** The webhook prevents *unauthorized* labels but cannot catch *missing* labels. A pod that should be `privacy=high` but isn't labeled will be scheduled as `standard`. See `docs/research/llm-label-validation.md` for exploratory mitigation.
- **GPU VRAM not protected by CPU TEEs.** Confidential compute scheduling targets CPU-side TEEs (TDX, SEV-SNP). GPU memory is not encrypted by these technologies. This is a hardware industry gap, not a scheduling gap.

---

## 12. Conclusion

Nexa Scheduler makes compliance-aware placement automatic, auditable, and enforceable. Platform teams get privacy guarantees they can prove to auditors. ML engineers get safe placement without manual node selection. Compliance officers get report artifacts without parsing logs.

It works alongside the tools teams already use — Kueue for batch, Gatekeeper for admission, Prometheus for monitoring — filling the gap none of them cover: *where should this sensitive workload land?*
