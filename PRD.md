# Nexa Scheduler

## 1. Vision
Enable organizations to run sensitive AI and batch workloads on shared Kubernetes clusters with strong, automated privacy and compliance controls — without sacrificing performance, simplicity, or cost efficiency.

> *“Run your workloads where they belong — safely, automatically, and invisibly.”*

---

## 2. Problem Statement
In shared or multi-tenant Kubernetes environments:
- Sensitive workloads (e.g., healthcare, legal, finance) risk co-location with untrusted neighbors.
- Data residency and compliance (e.g., HIPAA, GDPR) are hard to enforce at scale.
- Default schedulers optimize for utilization, not isolation or data control.
- Users lack visibility into *where* and *how* their jobs ran.

This leads to:
- Over-provisioning (dedicated clusters per team/org).
- Manual policy enforcement.
- Compliance gaps.
- Distrust in shared infrastructure.

---

## 3. Target Users
- **Platform Engineers** managing multi-tenant clusters.
- **ML Engineers** running private inference/training jobs.
- **Compliance Officers** needing auditability.
- **Research Consortia** sharing compute across institutions.

---

## 4. Unique Value Proposition (UVP)
> **Nexa Scheduler** is a lightweight, open-source Kubernetes scheduler extension that enforces **data privacy, workload isolation, and compliance-by-default** through automated placement — so users can safely share infrastructure without tradeoffs.

Key differentiators:
-**Automatic isolation**: Enforces anti-affinity, clean nodes, and region locking out of the box.
-**Policy-driven**: Define rules via labels (e.g., `privacy=high`, `region=eu`).
-**Audit-ready**: Logs placement decisions and node hygiene.
-**No vendor lock-in**: Works on any Kubernetes cluster — cloud, on-prem, hybrid.
-**Low overhead**: Adds minimal latency; integrates with existing tooling.

---

## 5. Core Features

| Feature | Description |
|-------|-------------|
| **Smart Placement Engine** | Extends `kube-scheduler` to enforce isolation, data locality, and node cleanliness. |
| **Privacy-Aware Scheduling** | Blocks placement on tainted or previously used nodes unless sanitized. |
| **Region & Zone Affinity** | Respects `region`, `zone`, and `compliance` labels from pod specs. |
| **Ephemeral Node Support** | Integrates with node lifecycle hooks to wipe state post-job. |
| **Policy Configuration** | Uses ConfigMaps or CRDs to define rules (e.g., “PHI jobs must run on wiped nodes”). |
| **Audit Logging** | Records job placement, node state, and policy checks. |
| **Metrics Export** | Exposes Prometheus metrics: queue time, success rate, isolation violations. |

---

## 6. Architecture Overview

```
+---------------------+
| Kubernetes API Server |
+----------+----------+
           |
           | Watches Pods (with nexa.scheduling=enabled)
           v
+---------------------+
|   Nexa Scheduler    | ← ConfigMap (policies)
+----------+----------+ ← Prometheus (metrics)
           |
           | Filters & Scores Nodes
           v
+---------------------+    +---------------------+
| Node A (clean, us)  |    | Node B (dirty, eu)   |
| - No prior workloads|    | - Ran other job      |
| - Region: us-west1  |    | - Not wiped          |
+----------+----------+    +----------+----------+
           |                          |
           |   Only Node A            | Rejected
           |   passes filters         |
           v                          v
+--------------------------------------------------+
| Placement Decision: Pod → Node A                 |
+--------------------------------------------------+
```

### Key Components:
- **Nexa Scheduler Plugin**: Out-of-tree scheduler (or scheduler extender) that runs alongside `kube-scheduler`.
- **Policy Engine**: Evaluates rules from ConfigMap or CRD.
- **Node State Tracker**: Watches node conditions (e.g., `wiped=true`, `region=us`).
- **Audit Logger**: Writes structured logs to stdout or external sink.
- **Metrics Server**: Exposes `/metrics` endpoint for Prometheus.

---

## 7. Technical Stack

| Layer | Technology |
|------|------------|
| **Scheduler Framework** | Kubernetes Scheduler Framework (out-of-tree) or Scheduler Extender |
| **Language** | Go (idiomatic for Kubernetes) |
| **Policy Engine** | Open Policy Agent (OPA) or native Go rules engine |
| **Configuration** | Kubernetes ConfigMap or Custom Resource (e.g., `NexaPolicy`) |
| **Observability** | Prometheus + Grafana, structured logging (JSON) |
| **Node Hygiene** | Integration with node startup scripts or DaemonSet (e.g., wipe `/tmp`, `dd` ephemeral disk) |
| **Deployment** | Helm chart or YAML manifests |
| **CI/CD** | GitHub Actions, containerized build (Docker) |
| **Hosting** | GitHub (open source), container on GHCR or Docker Hub |

---

## 8. Example Workflow

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: medical-ai-inference
  labels:
    nexa/schedule: "enabled"
    privacy: "high"
    data-classification: "phi"
    region: "us-central1"
spec:
  schedulerName: nexa-scheduler
  containers:
    - name: analyzer
      image: medical-llm:latest
  nodeSelector:
    nexa/wipe-on-complete: "true"
```

**Scheduler Behavior**:
1. Sees `privacy=high` and `region=us-central1`.
2. Filters nodes not in `us-central1`.
3. Excludes nodes with `last-workload-user=other-org`.
4. Requires `nexa/wipe-on-complete=true`.
5. Logs: `Placed pod medical-ai-inference on node kube-node-7 (clean, us-central1)`.

---

## 9. Success Metrics
| Metric | Target |
|-------|--------|
| % of high-privacy jobs correctly isolated | ≥ 99% |
| Scheduler decision latency | < 50ms per pod |
| Node wipe verification rate | 100% (logged) |
| Install time (Helm) | < 5 minutes |
| Documentation completeness | Full setup + 3 examples |

---

## 10. Roadmap (v1)
- [ ] MVP: Out-of-tree scheduler with region and anti-affinity filters.
- [ ] Add node cleanliness tracking via taints/tolerations.
- [ ] Policy CRD and ConfigMap support.
- [ ] Audit logging and Prometheus metrics.
- [ ] Helm chart and sample manifests.
- [ ] Documentation: quickstart, threat model, integration guide.

---

## 11. Non-Goals (v1)
- Confidential computing (e.g., TEEs) — future phase.
- Multi-cluster scheduling — keep it single-cluster for now.
- UI dashboard — use Grafana/Loki instead.
- AuthZ or RBAC — assume cluster admin configures policies.

---

## 12. Conclusion
**Nexa Scheduler** makes shared Kubernetes clusters safer by design. It doesn’t ask users to choose between efficiency and privacy — it delivers both, quietly and reliably.

It works.
