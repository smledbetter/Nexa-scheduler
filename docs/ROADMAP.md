# Nexa Scheduler — Roadmap

## Current State

- **Tests:** 52 (134 subtests)
- **Coverage:** ~90% overall (100% metrics, 90.0% audit, 91.3% privacy, 88.1% region, 72.2% policy, 100% testing)
- **LOC:** ~3850 (application + deployment, excluding go.sum/config)
- **Go installed:** Yes — Go 1.26.0, golangci-lint v1.64.8
- **Helm installed:** Yes — via Homebrew
- **Milestone status:** Sprint 6 (Phase 6) complete. Sprint 7 next.
- **Gates:** All 4 Go gates passing (build, lint, test, coverage) + helm lint + helm template

---

## Architecture Decisions (Sprint 0)

These refine or override the PRD where the original recommendations were imprecise:

1. **Scheduler Framework only** — no Scheduler Extender. The Framework runs in-process with typed Go plugins (Filter, Score, Reserve, PostBind). Extenders are legacy HTTP webhooks with higher latency and fewer extension points.

2. **Native Go rules engine** — no OPA for MVP. The policies (region match, node cleanliness, privacy level) are simple predicates that map directly to Filter/Score plugins. OPA adds Rego complexity and a dependency for what amounts to 20 lines of Go per policy. OPA integration can be added later if policy complexity demands it.

3. **ConfigMap first, CRD later** — ConfigMap for policy configuration in MVP. CRDs require kubebuilder scaffolding and code generation. Phase 3 delivers ConfigMap; Phase 8 upgrades to CRD once the policy model is proven.

4. **Labels, not taints, for node state** — The PRD suggests taints/tolerations for node cleanliness. Labels and annotations are better: they let the scheduler make nuanced decisions (e.g., score clean nodes higher) rather than binary taint-based exclusion. Labels: `nexa.io/wiped`, `nexa.io/last-workload-org`, `nexa.io/wipe-on-complete`.

5. **Two-component architecture** — The scheduler plugin (read-only consumer of node state) and a Node State Controller (writes/manages node labels) are separate binaries. This separation prevents the scheduler from needing write access to node objects.

---

## Phases

### Phase 1: Project Scaffolding & Scheduler Plugin Shell — [Sprint 1] ✅

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

### Phase 2: Region & Privacy Filters — [Sprint 2] ✅

**Goal:** Both core scheduling plugins get real logic. Pods with `nexa.io/region` or `nexa.io/zone` labels are only placed on matching nodes. Pods with `nexa.io/privacy=high` require clean nodes (`nexa.io/wiped=true`) and org-based anti-affinity.

**Deliverables:**
- Test helper: construct `NodeInfo` from `v1.Node` specs for realistic unit tests
- Region Filter plugin: reject nodes not matching pod's region/zone labels
- Region Score plugin: prefer exact zone match > same region > no preference
- Privacy Filter plugin: enforce node cleanliness for high-privacy pods; org-based anti-affinity (reject nodes with pods from different orgs)
- Privacy Score plugin: prefer cleaner nodes (wiped > idle > busy-same-org)
- Unit tests: 20+ cases across both plugins (exact match, region-only, zone-only, no labels, conflicting labels, missing node labels, node dirty/clean states, org matching, privacy levels high/standard/none, malformed labels)

**Estimated LOC:** 1000–1500 (actual: 635 — pure plugin logic is more concise than estimated)

---

### Phase 3: Policy Configuration via ConfigMap — [Sprint 3] ✅

**Goal:** Scheduling policies are defined in a ConfigMap (`nexa-scheduler-config`) and dynamically loaded. Policies specify which labels trigger which filters and what the default behavior is.

**Deliverables:**
- Policy data model (Go structs with JSON tags)
- ConfigMap watcher using `client-go` informers
- Policy evaluation integrated into Filter/Score plugins
- Validation: reject malformed policies with clear error messages
- Unit tests for policy parsing, validation, and application
- Example ConfigMap manifests
- Integration test: Region + Privacy plugins compose correctly on shared pod/node pairs

**Estimated LOC:** 600–900 (actual: 910)

---

### Phase 4: Audit Logging — [Sprint 4] ✅

**Goal:** Every scheduling decision is logged as structured JSON: which pod, which node was selected, which nodes were filtered (and why), which policies applied.

**Deliverables:**
- PostBind plugin: log successful placement decisions
- Filter plugin enhancement: log filtered-out nodes with reasons
- Structured log format (JSON, compatible with Loki/Fluentd)
- Log levels: decision summaries at INFO, filter details at DEBUG
- Unit tests for log output format and content

**Estimated LOC:** 400–700

---

### Phase 5: Prometheus Metrics — [Sprint 5] ✅

**Goal:** Expose scheduling metrics at `/metrics` for Prometheus scraping.

**Deliverables:**
- Metrics: `nexa_scheduling_duration_seconds` (histogram), `nexa_filter_results_total` (counter by filter name and result), `nexa_score_distribution` (histogram), `nexa_isolation_violations_total` (counter), `nexa_policy_evaluations_total` (counter)
- Metrics registered via `prometheus/client_golang`
- Unit tests verifying metric registration and increments
- Grafana dashboard JSON (basic)

**Estimated LOC:** 400–600

---

### Phase 6: Helm Chart & Deployment — [Sprint 6] ✅

**Goal:** One-command installation via Helm with sensible defaults.

**Deliverables:**
- Helm chart: scheduler deployment, RBAC, ServiceAccount, ConfigMap
- Values file with documented options
- RBAC: minimal permissions (scheduler reads pods/nodes)
- CI: chart linting and template validation
- Sample manifests for non-Helm users

**Estimated LOC:** 500–800 (YAML/templates)

---

### Phase 7: Smoke Tests — [Sprint 7]

**Goal:** Prove the scheduler works end-to-end on a real cluster. Fix the policy ConfigMap name mismatch from Sprint 6.

**Deliverables:**
- Bug fix: policy ConfigMap name must match `DefaultConfigMapName` ("nexa-scheduler-config")
- Kind-based smoke test suite (7 scenarios: region filtering, privacy filtering, privacy rejection, org isolation, audit logs, metrics endpoint, policy hot reload)
- Test helpers: cluster lifecycle (Kind CLI), pod/node factories, wait/assert utilities
- Makefile `smoke` target with `//go:build smoke` tag isolation
- 3-worker Kind cluster config with labeled nodes for constraint matrix testing

**Estimated LOC:** 500–600

---

### Phase 8: Documentation & Hardening — [Sprint 8]

**Goal:** Production-ready documentation and edge case handling.

**Deliverables:**
- Quickstart guide (Kind cluster + Helm install + sample pod)
- Architecture document with diagrams
- Threat model (attack surfaces, mitigations)
- Integration guide (existing schedulers, monitoring, CI/CD)
- Edge case hardening: scheduler crash recovery, ConfigMap deletion handling, node label race conditions
- Fake clientset tests for ConfigMapProvider (covering New() and Get() paths that require a running API server)

**Estimated LOC:** 500–1000

---

### Phase 9: CRD Policy & Node State Controller — [Sprint 9]

**Goal:** Replace ConfigMap policies with a `NexaPolicy` CRD. Introduce the Node State Controller as a separate binary that manages node labels.

**Deliverables:**
- `NexaPolicy` CRD definition (kubebuilder or hand-written)
- CRD controller that watches NexaPolicy resources and configures the scheduler
- Node State Controller binary: watches node events, manages `nexa.io/*` labels
- Migration path from ConfigMap to CRD (support both, prefer CRD)
- Helm subchart for Node State Controller
- Unit and integration tests

**Estimated LOC:** 1200–1800

---

### Phase 10: GPU & Confidential Compute Scheduling — [Sprint 10] (optional)

**Goal:** Schedule GPU/accelerator workloads with topology awareness, gang-scheduling, and priority-based preemption — and gate sensitive AI workloads on confidential computing capabilities. Pods requesting GPUs are placed on nodes that minimize fragmentation, respect NUMA/NVLink topology, and can be co-scheduled as groups. Pods requiring confidential compute are placed only on TEE-capable nodes with verified encryption support.

**Deliverables:**

*GPU scheduling:*
- GPU topology Score plugin: prefer nodes where requested GPU count aligns with available contiguous GPUs; score based on `nvidia.com/gpu` extended resources and topology labels (`nexa.io/gpu-topology`, `nexa.io/nvlink-group`)
- Gang-scheduling Permit plugin: hold pods belonging to a job group (`nexa.io/gang-group`) until all members are schedulable, then release together; timeout with configurable grace period
- Preemption priority integration: priority classes for training vs. inference vs. batch workloads; configurable preemption policies in the policy engine (which job types can preempt which)
- Filter plugin: reject nodes without sufficient GPU resources or incompatible accelerator type (`nexa.io/accelerator-type`: A100, H100, etc.)

*Confidential compute scheduling:*
- New node labels: `nexa.io/tee` (values: `tdx`, `sev-snp`, `none`), `nexa.io/confidential` (boolean), `nexa.io/disk-encrypted` (boolean)
- Confidential Filter plugin: reject non-TEE nodes for pods with `nexa.io/confidential=required`; reject nodes without disk encryption for pods with `nexa.io/privacy=high`
- Confidential Score plugin: prefer TEE-capable nodes for `privacy=high` workloads; prefer nodes with matching TEE type when pod specifies `nexa.io/tee-type`
- Policy rule: `privacy=high` + `gpu=required` → require `nexa.io/confidential=true` node (configurable via CRD or ConfigMap)
- runtimeClass constraint: policy can require `runtimeClassName: kata-cc` (or similar) for confidential workloads; validated at Filter time

*Shared:*
- Unit tests: topology scoring (contiguous vs. fragmented), gang-scheduling (partial group, full group, timeout), preemption priority ordering, accelerator type filtering, TEE label filtering, confidential+GPU policy composition, runtimeClass enforcement
- Threat model addendum (cross-ref Phase 8): document GPU VRAM encryption gap — GPU memory is not protected by CPU TEEs, data in VRAM and in transit over PCIe is exposed to physical/firmware-level attacks; recommend processing sensitive data in TEE and minimizing GPU exposure for highest-privacy workloads

**Known limitations (to document, not solve):**
- Node labels are self-reported. Without remote attestation, `nexa.io/confidential=true` is a policy signal, not a cryptographic guarantee. Attestation integration is a future phase.
- No mainstream GPU offers full VRAM encryption. Confidential GPU compute is a hardware industry gap, not a scheduling gap.
- Confidential Containers (CoCo/Kata) add overhead. Policy should make confidential placement opt-in, not default.

**Estimated LOC:** 1000–1500
