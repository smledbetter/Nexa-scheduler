# Architecture

Nexa Scheduler is an out-of-tree Kubernetes scheduler that enforces data privacy, workload isolation, and region affinity through the Kubernetes Scheduling Framework. It runs as a separate scheduler binary alongside `kube-scheduler`, handling only pods with `schedulerName: nexa-scheduler`.

## Scheduling Pipeline

```
Pod submitted (schedulerName: nexa-scheduler)
│
├─ PreFilter ── NexaAudit: record cycle start time
│
├─ Filter ──── NexaRegion: reject nodes not matching pod's region/zone
│              NexaPrivacy: reject unwiped nodes, enforce org isolation
│
├─ Score ───── NexaRegion (weight 50): prefer exact zone match
│              NexaPrivacy (weight 50): prefer wiped nodes
│
├─ Bind ────── default kube-scheduler binding
│
├─ PostBind ── NexaAudit: log placement, record duration metric
│
└─ PostFilter ─ NexaAudit: log failure with per-node rejection reasons
               (only runs when all nodes filtered out)
```

## Components

| Package | Purpose | Key types |
|---------|---------|-----------|
| `cmd/scheduler` | Binary entrypoint. Registers plugins and metrics, runs kube-scheduler. | `main()` |
| `pkg/plugins/region` | Region and zone affinity filtering and scoring. | `Plugin`, `Name` |
| `pkg/plugins/privacy` | Privacy-aware filtering (node wipe, org isolation) and scoring. | `Plugin`, `Name` |
| `pkg/plugins/audit` | Structured JSON audit logging and scheduling duration tracking. | `Plugin`, `Logger`, `DecisionEntry` |
| `pkg/policy` | Policy engine. Loads scheduling rules from a ConfigMap via informer cache. | `Policy`, `Provider`, `ConfigMapProvider` |
| `pkg/metrics` | Prometheus metric definitions and nil-safe recording helpers. | `Register()`, `RecordFilter()` |
| `pkg/testing` | Test helpers for constructing `NodeInfo`, `Node`, and `Pod` objects. | `MakeNodeInfo()`, `MakeNode()`, `MakePod()` |

## Plugins

### NexaRegion

Enforces geographic placement constraints using two labels:

| Label | Scope | Example values |
|-------|-------|---------------|
| `nexa.io/region` | Pod, Node | `us-west1`, `eu-west1` |
| `nexa.io/zone` | Pod, Node | `us-west1-a`, `eu-west1-a` |

**Filter:** Rejects nodes whose region or zone label doesn't match the pod's. Pods without region/zone labels (and no policy defaults) pass all nodes. If the policy sets `defaultRegion` or `defaultZone`, those apply to unlabeled pods.

**Score:** Zone + region match = 100, region-only match = 50, no preference = 0.

**Fail-closed:** Returns `framework.Error` if the policy ConfigMap is unreadable. Pods are not scheduled without policy enforcement.

### NexaPrivacy

Enforces node cleanliness and organization isolation:

| Label | Scope | Values |
|-------|-------|--------|
| `nexa.io/privacy` | Pod | `high`, `standard`, or empty |
| `nexa.io/org` | Pod, Node | Organization identifier (e.g., `alpha`, `beta`) |
| `nexa.io/wiped` | Node | `true` if node has been sanitized |
| `nexa.io/last-workload-org` | Node | Org of the last workload that ran |

**Filter (high-privacy pods):** Three checks, all must pass:
1. Node must have `nexa.io/wiped=true`
2. Node's `last-workload-org` must match the pod's org (or be absent)
3. No running pods from a different org on the node

**Strict org isolation:** When `policy.privacy.strictOrgIsolation=true`, checks 2 and 3 apply to ALL pods with an org label, not just high-privacy pods.

**Score:** Wiped node = 100, not wiped but same org = 50, otherwise = 0. Only scores high-privacy pods.

**Isolation violations:** Rejections are counted in the `nexa_isolation_violations_total` metric with reasons: `node_not_wiped`, `cross_org`, `strict_org`.

### NexaAudit

Logs every scheduling decision as structured JSON and tracks scheduling duration:

**PreFilter:** Stores the current timestamp in `CycleState` for duration measurement.

**PostBind (success):** Records scheduling duration in the `nexa_scheduling_duration_seconds` histogram, then logs a JSON entry with event `"scheduled"`, the target node, pod metadata, and active policy state.

**PostFilter (failure):** Records duration, collects per-node rejection reasons from the status map, and logs a `"scheduling_failed"` entry with the filter reasons. Debug mode adds a separate `"filter_details"` entry.

**Data minimization:** The `PodRef` struct only extracts scheduling-relevant labels (`privacy`, `region`, `zone`, `org`). No environment variables, secrets, container specs, or service account tokens are logged.

## Policy Engine

Scheduling behavior is configured via a ConfigMap named `nexa-scheduler-config` in the `nexa-system` namespace.

**ConfigMap structure:**

```json
{
  "regionPolicy": {
    "enabled": true,
    "defaultRegion": "",
    "defaultZone": ""
  },
  "privacyPolicy": {
    "enabled": true,
    "defaultPrivacy": "standard",
    "strictOrgIsolation": false
  }
}
```

**Loading:** The `ConfigMapProvider` uses a `SharedInformerFactory` to watch the ConfigMap. Reads come from the local informer cache, not from the API server — there is no network call per scheduling cycle.

**Hot reload:** ConfigMap changes propagate automatically when the informer syncs. No scheduler restart required.

**Validation:** The `Validate()` function rejects invalid `defaultPrivacy` values (only `""`, `"standard"`, and `"high"` are accepted). Invalid JSON or missing ConfigMap key cause `GetPolicy()` to return an error, which triggers fail-closed behavior in all plugins.

## Metrics

Five metric families are registered at startup with the `nexa_` prefix:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `nexa_scheduling_duration_seconds` | Histogram | `result` | End-to-end scheduling cycle time |
| `nexa_filter_results_total` | Counter | `plugin`, `result` | Filter evaluations (accepted/rejected/error) |
| `nexa_score_distribution` | Histogram | `plugin` | Score values assigned (0-100, buckets of 10) |
| `nexa_isolation_violations_total` | Counter | `reason` | Privacy filter rejections by cause |
| `nexa_policy_evaluations_total` | Counter | `plugin`, `result` | Policy lookups (success/error) |

Metrics are served at `https://<scheduler-ip>:10259/metrics` via the kube-scheduler's built-in HTTPS server. The `--authorization-always-allow-paths=/metrics` flag allows Prometheus to scrape without mTLS authentication.

## Label Reference

| Label | Scope | Read by | Values | Required |
|-------|-------|---------|--------|----------|
| `nexa.io/region` | Pod, Node | NexaRegion | Region identifier (e.g., `us-west1`) | No |
| `nexa.io/zone` | Pod, Node | NexaRegion | Zone identifier (e.g., `us-west1-a`) | No |
| `nexa.io/privacy` | Pod | NexaPrivacy | `high`, `standard`, or empty | No |
| `nexa.io/org` | Pod, Node | NexaPrivacy | Organization identifier | No |
| `nexa.io/wiped` | Node | NexaPrivacy | `true` if sanitized | No |
| `nexa.io/last-workload-org` | Node | NexaPrivacy | Last org that used the node | No |

All labels are optional. Pods without labels are scheduled with no constraints (unless the policy sets defaults).

## Architecture Decisions

### 1. Scheduler Framework, not Scheduler Extender

**Context:** Kubernetes offers two extension mechanisms: the in-process Scheduling Framework (typed Go plugins) and the legacy Scheduler Extender (HTTP webhooks).

**Options:** Framework plugins run in-process with type-safe interfaces and access to the full scheduling cycle (PreFilter, Filter, Score, Reserve, PostBind). Extenders are HTTP callbacks with higher latency, fewer extension points, and no access to CycleState.

**Choice:** Scheduling Framework. It provides lower latency (no HTTP round-trip), richer extension points (PreFilter for timing, PostFilter for failure logging), and typed Go interfaces that catch errors at compile time.

**Consequences:** The scheduler must be deployed as a separate binary (out-of-tree scheduler), not as a webhook sidecar. This is the standard pattern for production schedulers (see `sigs.k8s.io/scheduler-plugins`).

### 2. Native Go Rules Engine, not OPA

**Context:** The PRD suggested Open Policy Agent (OPA) for policy evaluation.

**Options:** OPA provides a general-purpose policy engine with Rego language support. A native Go engine uses simple predicate functions within the scheduler plugins.

**Choice:** Native Go. The scheduling policies (region match, node cleanliness, privacy level) are simple predicates that map directly to Filter/Score plugin logic. Each policy is ~20 lines of Go.

**Consequences:** Adding complex policies (e.g., time-based scheduling windows, cross-namespace rules) would require Go code changes rather than Rego rules. OPA can be added later if policy complexity demands it.

### 3. ConfigMap First, CRD Later

**Context:** Policy configuration needs a storage mechanism.

**Options:** ConfigMap (built-in, no code generation), CRD (typed, versioned, admission control).

**Choice:** ConfigMap for MVP. CRDs require kubebuilder scaffolding and code generation. ConfigMap is simpler and sufficient for the current policy model.

**Consequences:** Policy changes are JSON edits in a ConfigMap, not typed Kubernetes resources. CRD migration is planned for Phase 10 and will support both ConfigMap and CRD with CRD taking precedence.

### 4. Labels, not Taints, for Node State

**Context:** The PRD suggested taints/tolerations for node cleanliness tracking.

**Options:** Taints provide binary exclusion (tainted nodes reject pods without matching tolerations). Labels allow nuanced scoring (prefer wiped nodes, but allow fallback to same-org nodes).

**Choice:** Labels and annotations. They enable the Score plugin to make graduated decisions (wiped=100, same-org=50, other=0) rather than binary taint-based exclusion.

**Consequences:** Node labels are self-reported and trust the label setter. A Node State Controller (Phase 10) will be the sole writer of `nexa.io/*` node labels.

### 5. Two-Component Architecture

**Context:** The scheduler reads node state labels, but something must write and manage those labels.

**Options:** Single binary (scheduler reads and writes node labels) or separate binaries (scheduler reads, controller writes).

**Choice:** Separate binaries. The scheduler plugin is a read-only consumer of node state. The Node State Controller (Phase 10) will manage node labels as a separate Deployment.

**Consequences:** The scheduler's RBAC only needs read access to nodes (get/list/watch). Write access is scoped to the controller, reducing the scheduler's attack surface.
