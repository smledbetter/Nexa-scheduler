# Nexa Scheduler — Roadmap

## Current State

- **Status:** Milestone 1 complete (admission webhook + Kueue integration shipped). Starting Milestone 2.
- **Tests:** 82 top-level (+ 16 smoke tests behind `//go:build smoke`, all passing)
- **Coverage:** ~92% overall (100% metrics, 87.1% audit, 92.0% privacy, 89.1% region, 95.7% policy, 85.9% nodestate, 100% testing, 93.2% webhook)
- **LOC:** ~9450 (application + deployment, excluding go.sum/config)
- **Binaries:** 3 (scheduler, node controller, webhook)
- **Helm subcharts:** 3 (nexa-scheduler, nexa-node-controller, nexa-webhook)
- **Go installed:** Yes — Go 1.26.0, golangci-lint v1.64.8
- **Helm installed:** Yes — via Homebrew
- **Docs:** Quickstart, architecture, threat model (label spoofing mitigated), integration guide with Kueue section (docs/)
- **Sprints completed:** 14 (Sprint 0–13), across 13 phases
- **Gates:** All gates passing (build, lint, test, coverage, helm lint x3, helm template, smoke vet)

---

## Architecture Decisions (Sprint 0)

These refine or override the PRD where the original recommendations were imprecise:

1. **Scheduler Framework only** — no Scheduler Extender. The Framework runs in-process with typed Go plugins (Filter, Score, Reserve, PostBind). Extenders are legacy HTTP webhooks with higher latency and fewer extension points.

2. **Native Go rules engine** — no OPA for MVP. The policies (region match, node cleanliness, privacy level) are simple predicates that map directly to Filter/Score plugins. OPA adds Rego complexity and a dependency for what amounts to 20 lines of Go per policy. OPA integration can be added later if policy complexity demands it.

3. **ConfigMap first, CRD later** — ConfigMap for policy configuration in MVP. CRDs require kubebuilder scaffolding and code generation. Phase 3 delivers ConfigMap; Phase 10 upgrades to CRD once the policy model is proven.

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

### Phase 7: Smoke Tests — [Sprint 7] ✅

**Goal:** Prove the scheduler works end-to-end on a real cluster. Fix the policy ConfigMap name mismatch from Sprint 6.

**Deliverables:**
- Bug fix: policy ConfigMap name must match `DefaultConfigMapName` ("nexa-scheduler-config")
- Bug fix: RBAC ClusterRole expanded for all kube-scheduler framework informer requirements
- Bug fix: metrics registered with `legacyregistry.Registerer()` instead of `prometheus.DefaultRegisterer`
- Bug fix: `--authorization-always-allow-paths` added for Prometheus metrics scraping
- Kind-based smoke test suite (7 scenarios, all passing: region filtering, privacy filtering, privacy rejection, org isolation, audit logs, metrics endpoint, policy hot reload)
- Test helpers: cluster lifecycle (Kind CLI), pod/node factories, wait/assert utilities
- Deploy contract tests: 5 unit tests (ConfigMap name, RBAC completeness, auth path)
- Makefile `smoke` target with `//go:build smoke` tag isolation
- 3-worker Kind cluster config with labeled nodes for constraint matrix testing

**Estimated LOC:** 500–600 (actual: 816 — includes post-SHIP deployment bug fixes)

---

### Phase 8: Hardening — [Sprint 8] ✅

**Goal:** Close deferred test coverage gaps and eliminate code duplication identified in sprint retrospectives.

**Deliverables:**
- Fake clientset tests for ConfigMapProvider (covering `NewConfigMapProvider()` and `GetPolicy()` paths — deferred since Sprint 3)
- Extract duplicate metric helpers (`recordFilter`, `recordPolicyEval`, `recordScore`) from region.go and privacy.go into `pkg/metrics/` as exported functions
- Tests for extracted metric helpers (nil-safe, increment verification)

**Estimated LOC:** 200–350

---

### Phase 9: Documentation — [Sprint 9] ✅

**Goal:** Production-ready documentation for operators and contributors.

**Deliverables:**
- Quickstart guide (Kind cluster + Helm install + sample pod)
- Architecture document with diagrams
- Threat model (attack surfaces, mitigations)
- Integration guide (existing schedulers, monitoring, CI/CD)

**Estimated LOC:** 400–700 (markdown/docs) (actual: 912)

---

### Phase 10: CRD Policy & Node State Controller — [Sprint 10] ✅

**Goal:** Replace ConfigMap policies with a `NexaPolicy` CRD. Introduce the Node State Controller as a separate binary that manages node labels.

**Deliverables:**
- `NexaPolicy` CRD definition (kubebuilder or hand-written)
- CRD controller that watches NexaPolicy resources and configures the scheduler
- Node State Controller binary: watches node events, manages `nexa.io/*` labels
- Migration path from ConfigMap to CRD (support both, prefer CRD)
- Helm subchart for Node State Controller
- Unit and integration tests

**Estimated LOC:** 1200–1800 (actual: 1555)

---

### Phase 10a: Hardening — [Sprint 11] ✅

**Goal:** Recover test coverage dropped by Sprint 10 and add CRD smoke tests for real-cluster validation.

**Deliverables:**
- Provider factory unit tests (`pkg/policy/provider_factory_test.go`): mock ProviderHandle, singleton behavior, broken KubeConfig fallback
- Node state controller coverage recovery (`pkg/nodestate/controller_test.go`): processNextWorkItem (4 cases), event handlers (5 cases), Run() lifecycle (2 cases)
- CRD smoke tests (3 scenarios): CRD policy scheduling, ConfigMap fallback, CRD overrides ConfigMap

**Estimated LOC:** 400–600 (actual: ~530)

---

## Next: Compliance & Kueue Complement Roadmap

**Strategic direction:** Position Nexa as a complement to Kueue for sensitive AI workloads. Kueue handles "when does this job run?" (queuing, admission, fairness). Nexa handles "where does it land safely?" (privacy, compliance, geographic sovereignty). GPU scheduling, gang scheduling, and preemption are out of scope — Kueue/Volcano/YuniKorn serve those use cases well.

**Architectural decision: GPU scheduling removed from roadmap.** Batch/AI scheduling features (GPU topology, gang scheduling, preemption priority) are mature in Volcano, Kueue, and YuniKorn. Building inferior versions of solved problems dilutes focus. Nexa's value is compliance-aware placement, which is complementary to batch schedulers — not competitive with them.

---

### Milestone 1: Production Trust — [Sprints 12–13] ✅

**Goal:** Make Nexa's privacy guarantees enforceable, not advisory. Close the label spoofing threat (highest-severity documented risk) and validate Kueue co-deployment so platform teams can adopt Nexa alongside their existing batch infrastructure.

**Why first:** Without the admission webhook, every privacy guarantee is honor-system. No compliance officer will certify a system where any developer can bypass isolation by typing a label. This is the #1 adoption gate.

#### Phase 11: Admission Webhook for Label Integrity — [Sprint 12] ✅

**Goal:** ValidatingAdmissionWebhook that enforces `nexa.io/*` label provenance. Pods can only set org/privacy labels if the submitting namespace is authorized.

**Architecture:** Separate binary (`cmd/webhook/main.go`) with its own Helm subchart. Not embedded in scheduler or controller — failure isolation is critical since a broken webhook can block pod creation.

**Deliverables:**
- `pkg/webhook/config.go` + `config_test.go`: Config types, ParseConfig, LoadConfigFromFile, RuleForNamespace, validation (12 test cases)
- `pkg/webhook/handler.go` + `handler_test.go`: HTTP handler implementing `admission.k8s.io/v1` AdmissionReview with validation logic (40 test cases incl. subtests)
- `cmd/webhook/main.go`: Standalone binary entrypoint with TLS, config loading, graceful shutdown
- `Dockerfile.webhook`: Multi-stage distroless build
- Helm subchart: `deploy/helm/nexa-webhook/` (Deployment, Service, ValidatingWebhookConfiguration, ConfigMap, RBAC, ServiceAccount — 8 templates)
- Fail-closed: webhook unavailable = pod rejected (configurable via `failurePolicy` in values.yaml)
- Scope: webhook fires on pods in namespaces with `nexa.io/webhook=enabled` label (opt-in via namespaceSelector)
- TLS from filesystem (`--cert-dir` flag) — operator provides certs via cert-manager or manually
- Smoke tests: 3 webhook scenarios with TLS cert generation helpers
- Threat model updated: label spoofing risk closed (Section 1: Gap → Mitigated)
- **Descoped:** Label auto-injection requires MutatingAdmissionWebhook (architecturally distinct); deferred

**Estimated LOC:** 600–800 (actual: 1556 incl. Helm templates and tests)

#### Phase 12: Kueue Integration — [Sprint 13] ✅

**Goal:** Documented and tested co-deployment of Kueue + Nexa. Kueue admits jobs (quota, fairness), Nexa places pods (privacy, region). Platform engineers can install both without conflicts.

**Architecture:** No new Go packages. The integration point is the Kubernetes Pod API — Kueue controls `spec.suspend`, Nexa reads from the scheduler queue. They share no state or CRDs.

**Deliverables:**
- Integration guide section: "Running Nexa alongside Kueue" (interaction model, label propagation, ResourceFlavor alignment with Nexa regions)
- Shared Helm values example: both charts installed on same cluster
- Smoke test infrastructure: `installKueue(t)` / `uninstallKueue()` lifecycle helpers, `makeJob()` helper (Kueue manages Jobs, not bare Pods), `waitForWorkloadAdmitted()` two-phase wait (admitted by Kueue, then scheduled by Nexa), Kueue resource setup helpers (ResourceFlavor, ClusterQueue, LocalQueue)
- Smoke tests (3 scenarios): Kueue admits → Nexa schedules to compliant node; Kueue admits → Nexa rejects all nodes (privacy) → pod stays Pending; Kueue suspends (quota exceeded) → Nexa never sees the pod
- Document potential conflict: Kueue ResourceFlavor nodeSelector vs. Nexa region filter. Configuration concern, not code concern
- Kueue version compatibility matrix (pin to specific Kueue release in smoke tests)

**Estimated LOC:** 300–600 (smoke tests + documentation) (actual: 592)

---

### Milestone 2: Compliance Evidence — [Sprint 14]

**Goal:** A compliance officer can produce audit evidence for SOC2/HIPAA/GDPR without parsing JSON. The evidence chain is complete: admission validation (M1) → scheduling enforcement → audit report.

#### Phase 13: Compliance Report Generation — [Sprint 14]

**Goal:** CLI tool that reads structured JSON audit logs and produces compliance artifacts: workload inventory, node placement map, isolation compliance, policy timeline, geographic residency.

**Architecture:** New package `pkg/compliance/` and binary `cmd/compliance/main.go`. Offline tool — zero runtime coupling to the scheduler. Shares audit log JSON schema with `pkg/plugins/audit/`.

**Deliverables:**
- `pkg/compliance/reader.go`: Parse DecisionEntry JSON lines from file or stdin
- `pkg/compliance/report.go`: Aggregate entries into compliance report (per-org, per-time-range)
- `cmd/compliance/main.go`: CLI entrypoint (`nexa-report --org alpha --from T1 --to T2 --standard hipaa`)
- Output formats: JSON (machine-readable) + markdown (human-readable)
- Reports flag violations with full context (timestamp, pod, node, reason)
- Reports include compliant decisions (auditors need proof of compliance, not just violations)
- Integration guide: "Immutable Audit Storage" section with S3 Object Lock and Loki retention examples (documentation, not code — log shipping is operator infrastructure)
- Unit tests for parsing, aggregation, and report generation

**Estimated LOC:** 300–400

---

### Milestone 3: Hardware Trust — [Sprint 15]

**Goal:** Extend Nexa's privacy guarantees to hardware-level protections and temporal freshness. Platform engineers operating TEE-capable infrastructure can enforce confidential compute requirements at scheduling time. Temporal policy ensures wipe freshness.

#### Phase 14: Confidential Compute Scheduling + Node Cooldown — [Sprint 15]

**Goal:** New Filter/Score plugin for TEE-capable nodes. Extend wipe tracking from boolean to timestamp for temporal freshness policy.

**Architecture:** Confidential compute plugin follows the established pattern (identical structure to region/privacy plugins). Node cooldown is a surgical enhancement to the existing node state controller and privacy filter.

**Deliverables:**

*Confidential compute:*
- `pkg/plugins/confidential/confidential.go`: Filter + Score plugin
- New node labels: `nexa.io/tee` (values: `tdx`, `sev-snp`, `none`), `nexa.io/confidential` (boolean), `nexa.io/disk-encrypted` (boolean)
- Confidential Filter: reject non-TEE nodes for pods with `nexa.io/confidential=required`; reject nodes without disk encryption for pods with `nexa.io/privacy=high`
- Confidential Score: prefer TEE-capable nodes for `privacy=high` workloads; prefer nodes with matching TEE type when pod specifies `nexa.io/tee-type`
- Policy extension: `ConfidentialPolicy` added to NexaPolicy CRD (`requireTEEForHigh`, `requireEncryptedDisk`, `defaultTEEType`)
- runtimeClass constraint: policy can require `runtimeClassName: kata-cc` for confidential workloads, validated at Filter time

*Node cooldown:*
- New label: `nexa.io/wipe-timestamp` (RFC3339) — written by Node State Controller alongside `nexa.io/wiped=true`
- Policy extension: `CooldownHours int` in `PrivacyPolicy` (0 = disabled, backward compatible)
- Privacy Filter enhancement: if `CooldownHours > 0`, reject nodes where wipe timestamp exceeds threshold
- Fail-closed: missing or malformed timestamp = node rejected

*Shared:*
- Unit tests: TEE label filtering, confidential+privacy policy composition, runtimeClass enforcement, temporal freshness (recent wipe passes, stale wipe rejected, missing timestamp rejected, cooldown disabled)
- Threat model addendum: GPU VRAM encryption gap (GPU memory not protected by CPU TEEs), self-reported labels vs. remote attestation
- Smoke tests for TEE filtering and temporal freshness

**Known limitations (to document, not solve):**
- Node labels are self-reported. Without remote attestation, `nexa.io/confidential=true` is a policy signal, not a cryptographic guarantee. Attestation integration is a future phase.
- No mainstream GPU offers full VRAM encryption. Confidential GPU compute is a hardware industry gap, not a scheduling gap.
- Confidential Containers (CoCo/Kata) add overhead. Policy should make confidential placement opt-in, not default.

**Estimated LOC:** 500–750

---

### Deferred

#### Graduated Isolation Policies — (not planned)

Add a `medium` privacy tier between `high` and `standard` with configurable per-level requirements. **Deferred because:** the binary high/standard model covers real-world cases. `strictOrgIsolation` already serves the "medium" use case (org isolation without wipe requirement). Adding tiers increases policy surface area and audit complexity. Revisit only when users report the binary model as an adoption blocker.

#### Immutable Audit Log Shipping — (not planned, documented instead)

Built-in sidecar for log forwarding to append-only storage. **Cut because:** every organization has an existing log pipeline (Fluent Bit, Promtail, Vector, Datadog). Nexa's JSON-to-stderr contract is the standard Kubernetes logging interface. Immutability guarantees come from the destination (S3 Object Lock, Loki retention), not the shipper. Example configs are documented in the integration guide.

#### GPU & Batch Scheduling — (out of scope)

GPU topology awareness, gang scheduling, preemption priority. **Out of scope because:** Kueue, Volcano, and YuniKorn are mature, CNCF-backed solutions for these capabilities. Nexa complements them (privacy-aware placement) rather than competing with them (batch orchestration).
