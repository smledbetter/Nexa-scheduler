# Nexa Scheduler — Roadmap

## Current State

- **Tests:** 0
- **Coverage:** N/A (no code yet)
- **LOC:** 0 (application code)
- **Go installed:** No — must install Go 1.23+ and golangci-lint before Sprint 1
- **Milestone status:** Pre-MVP, planning complete

---

## Architecture Decisions (Sprint 0)

These refine or override the PRD where the original recommendations were imprecise:

1. **Scheduler Framework only** — no Scheduler Extender. The Framework runs in-process with typed Go plugins (Filter, Score, Reserve, PostBind). Extenders are legacy HTTP webhooks with higher latency and fewer extension points.

2. **Native Go rules engine** — no OPA for MVP. The policies (region match, node cleanliness, privacy level) are simple predicates that map directly to Filter/Score plugins. OPA adds Rego complexity and a dependency for what amounts to 20 lines of Go per policy. OPA integration can be added later if policy complexity demands it.

3. **ConfigMap first, CRD later** — ConfigMap for policy configuration in MVP. CRDs require kubebuilder scaffolding and code generation. Phase 4 delivers ConfigMap; Phase 7 upgrades to CRD once the policy model is proven.

4. **Labels, not taints, for node state** — The PRD suggests taints/tolerations for node cleanliness. Labels and annotations are better: they let the scheduler make nuanced decisions (e.g., score clean nodes higher) rather than binary taint-based exclusion. Labels: `nexa.io/wiped`, `nexa.io/last-workload-org`, `nexa.io/wipe-on-complete`.

5. **Two-component architecture** — The scheduler plugin (read-only consumer of node state) and a Node State Controller (writes/manages node labels) are separate binaries. This separation prevents the scheduler from needing write access to node objects.

---

## Phases

### Phase 1: Project Scaffolding & Scheduler Plugin Shell — [Sprint 1]

**Goal:** A running out-of-tree scheduler binary that registers with the Kubernetes Scheduling Framework, accepts pods with `schedulerName: nexa-scheduler`, and schedules them using no-op Filter/Score plugins.

**Deliverables:**
- Go module initialized (`go.mod` with k8s.io/kube-scheduler dependency)
- Scheduler binary entrypoint using `k8s.io/kube-scheduler/app`
- No-op Filter and Score plugin structs implementing the framework interfaces
- Plugin registration via `app.WithPlugin`
- Unit tests for plugin registration and interface compliance
- Dockerfile for the scheduler binary
- golangci-lint configuration (`.golangci.yml`)
- Makefile with build/test/lint targets

**Estimated LOC:** 600–900

---

### Phase 2: Region & Zone Affinity Filter — [Sprint 2]

**Goal:** Pods with `nexa.io/region` or `nexa.io/zone` labels are only placed on nodes matching those labels. Non-matching nodes are filtered out. Nodes with exact matches score higher than nodes with partial matches (same region, different zone).

**Deliverables:**
- Filter plugin: reject nodes not matching pod's region/zone labels
- Score plugin: prefer exact zone match > same region > no preference
- ConfigMap reader for default region/zone policies
- Unit tests with fake framework handles (at least 10 test cases covering: exact match, region-only, zone-only, no labels, conflicting labels, missing node labels)
- Integration test with a fake scheduler

**Estimated LOC:** 500–800

---

### Phase 3: Privacy & Node Cleanliness Filter — [Sprint 3]

**Goal:** Pods with `nexa.io/privacy=high` are only placed on nodes labeled `nexa.io/wiped=true` and not currently running workloads from a different organization. Anti-affinity enforcement based on `nexa.io/org` labels.

**Deliverables:**
- Filter plugin: enforce node cleanliness for high-privacy pods
- Filter plugin: org-based anti-affinity (reject nodes with pods from different orgs)
- Score plugin: prefer cleaner nodes (wiped > idle > busy-same-org)
- Unit tests (node dirty/clean states, org matching, privacy levels: high/standard/none)

**Estimated LOC:** 700–1000

---

### Phase 4: Policy Configuration via ConfigMap — [Sprint 4]

**Goal:** Scheduling policies are defined in a ConfigMap (`nexa-scheduler-config`) and dynamically loaded. Policies specify which labels trigger which filters and what the default behavior is.

**Deliverables:**
- Policy data model (Go structs with JSON tags)
- ConfigMap watcher using `client-go` informers
- Policy evaluation integrated into Filter/Score plugins
- Validation: reject malformed policies with clear error messages
- Unit tests for policy parsing, validation, and application
- Example ConfigMap manifests

**Estimated LOC:** 600–900

---

### Phase 5: Audit Logging — [Sprint 5]

**Goal:** Every scheduling decision is logged as structured JSON: which pod, which node was selected, which nodes were filtered (and why), which policies applied.

**Deliverables:**
- PostBind plugin: log successful placement decisions
- Filter plugin enhancement: log filtered-out nodes with reasons
- Structured log format (JSON, compatible with Loki/Fluentd)
- Log levels: decision summaries at INFO, filter details at DEBUG
- Unit tests for log output format and content

**Estimated LOC:** 400–700

---

### Phase 6: Prometheus Metrics — [Sprint 6]

**Goal:** Expose scheduling metrics at `/metrics` for Prometheus scraping.

**Deliverables:**
- Metrics: `nexa_scheduling_duration_seconds` (histogram), `nexa_filter_results_total` (counter by filter name and result), `nexa_score_distribution` (histogram), `nexa_isolation_violations_total` (counter), `nexa_policy_evaluations_total` (counter)
- Metrics registered via `prometheus/client_golang`
- Unit tests verifying metric registration and increments
- Grafana dashboard JSON (basic)

**Estimated LOC:** 400–600

---

### Phase 7: CRD Policy & Node State Controller — [Sprint 7]

**Goal:** Replace ConfigMap policies with a `NexaPolicy` CRD. Introduce the Node State Controller as a separate binary that manages node labels.

**Deliverables:**
- `NexaPolicy` CRD definition (kubebuilder or hand-written)
- CRD controller that watches NexaPolicy resources and configures the scheduler
- Node State Controller binary: watches node events, manages `nexa.io/*` labels
- Migration path from ConfigMap to CRD (support both, prefer CRD)
- Unit and integration tests

**Estimated LOC:** 1200–1800

---

### Phase 8: Helm Chart & Deployment — [Sprint 8]

**Goal:** One-command installation via Helm with sensible defaults.

**Deliverables:**
- Helm chart: scheduler deployment, RBAC, ServiceAccount, ConfigMap/CRD
- Helm chart: Node State Controller deployment (optional subchart)
- Values file with documented options
- RBAC: minimal permissions (scheduler reads pods/nodes; controller writes node labels)
- CI: chart linting and template validation
- Sample manifests for non-Helm users

**Estimated LOC:** 500–800 (YAML/templates)

---

### Phase 9: Documentation & Hardening — [Sprint 9]

**Goal:** Production-ready documentation and edge case handling.

**Deliverables:**
- Quickstart guide (Kind cluster + Helm install + sample pod)
- Architecture document with diagrams
- Threat model (attack surfaces, mitigations)
- Integration guide (existing schedulers, monitoring, CI/CD)
- Edge case hardening: scheduler crash recovery, ConfigMap deletion handling, node label race conditions
- End-to-end test suite (Kind-based)

**Estimated LOC:** 500–1000
