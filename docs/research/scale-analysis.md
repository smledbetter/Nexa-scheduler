# Nexa Scheduler — Scale Testing & Architectural Drawbacks

*Analysis date: 2026-02-27*

## How to Test at Scale

### 1. Scheduler Throughput Benchmarking

The critical path is Filter → Score → PostBind, and every step calls `GetPolicy()` which does JSON unmarshal + validate. A realistic benchmark would:

- Use `k8s.io/kubernetes/test/integration/scheduler_perf` framework with custom test cases
- Measure scheduling latency (p50/p99) at 100, 1K, 10K pending pods with 100–5000 nodes
- Instrument the policy provider to measure parse overhead vs. actual scheduling logic
- Compare with and without audit logging (PostBind/PostFilter) to isolate the I/O cost

### 2. Node State Controller Under Churn

- Simulate high pod termination rates (100+ pods/sec) and measure reconcile queue depth + API server patch latency
- The `podLister.List(labels.Everything())` call in `reconcileNode` is the hotspot — profile it at 10K, 50K, 100K pod counts
- Run 2+ controller replicas without leader election to demonstrate the patch race

### 3. Attestation Controller at Node Scale

- Mock the attestation HTTP endpoint with variable latency (10ms–5s)
- Measure tick completion time with 10, 100, 1000 TEE nodes (sequential verification means this scales linearly)
- Test failure modes: what happens when the attestation service is down for multiple ticks?

### 4. Webhook Admission Latency

- Load test with `k6` or `vegeta` against the admission endpoint
- Profile the linear `RuleForNamespace()` scan with 10, 100, 1000 namespace rules
- Test concurrent CREATE storms (1000 pods/sec) to surface any serialization bottlenecks

### 5. End-to-End at Realistic Cluster Scale

- `kwok` (Kubernetes WithOut Kubelet) can simulate 5000+ nodes with minimal resources
- Combine with the existing smoke test scenarios but at 100x scale
- Measure scheduling throughput degradation as policy complexity increases

---

## Architectural Drawbacks

### Critical: Repeated Policy Parsing on Every Call

Every Filter and Score invocation calls `GetPolicy()`, which does `json.Marshal` → `json.Unmarshal` → `Validate()` (CRD path) or `lister.Get()` → `Parse()` → `Validate()` (ConfigMap path). With 4 plugins × 2 extension points (Filter + Score) + Audit, that's **8+ full JSON parse cycles per pod per scheduling cycle**. At 1000 nodes, each Filter runs per-node, so you're looking at ~8000 parse-validate operations per pod. The policy doesn't change between nodes in a single cycle.

**Fix**: Cache the parsed `*Policy` in the provider, invalidate via informer event handler. A single `atomic.Pointer[Policy]` would eliminate all per-call parsing.

### Critical: O(all-pods) Scan in Node State Reconciliation

`controller.go:reconcileNode` calls `podLister.List(labels.Everything())` and then client-side filters for the target node. At 50K pods across 500 nodes, every single node reconciliation scans all 50K pods. Under churn (many pods terminating), this becomes a hot loop.

**Fix**: Add an informer index on `spec.nodeName` so you can query `podIndexer.ByIndex("nodeName", nodeName)` — O(pods-on-node) instead of O(all-pods).

### High: Sequential Attestation Verification

The attestation controller verifies TEE nodes one at a time in a `for` loop with a 30-second HTTP timeout per node. With 100 TEE nodes and a 200ms average attestation time, a single tick takes 20 seconds. With a slow service or timeouts, it can take 50+ minutes — longer than the 5-minute tick interval, causing tick skips and stale attestation state.

**Fix**: Bounded goroutine pool (`errgroup` with `SetLimit`) for parallel verification.

### High: No Leader Election on Controllers

Neither the node state controller nor the attestation controller implement leader election. Running multiple replicas (standard for HA) causes:

- Duplicate node label patches racing via MergePatch (no resourceVersion)
- Double the API server load
- Non-deterministic label outcomes when two replicas see different pod states

### Medium: Singleton Provider Failure Is Permanent

```go
var sharedProviderOnce sync.Once
```

If the CRD informer fails to start during the first `NewCompositeProviderFromHandle` call (network blip, RBAC misconfiguration), `sharedProviderErr` is set forever. Every plugin returns `Error` on every scheduling cycle. The scheduler is bricked until restart. No retry, no circuit breaker, no health check that surfaces this.

### Medium: MergePatch Without Optimistic Concurrency

Both controllers patch node labels with `types.MergePatchType` and no `resourceVersion`. Two concurrent patches can overwrite each other:

1. Controller A reads node (labels: `{org: alpha, wiped: true}`)
2. Controller B reads node (same state)
3. A patches `{org: beta}` → succeeds
4. B patches `{wiped: false}` → succeeds, but doesn't see A's change
5. Final state depends on ordering, not intent

This violates the k8s optimistic concurrency model.

### Medium: Eventual Consistency Race Between Scheduler and Controller

The scheduler reads node labels from its informer cache. The controller writes labels via API server patches. There's an inherent window where:

1. Pod terminates on node X
2. Controller hasn't reconciled yet → `wiped=true` still set
3. Scheduler places a new sensitive pod on node X (trusting `wiped=true`)
4. Controller reconciles → sets `wiped=false`, but pod is already scheduled

For a **privacy-focused scheduler**, this race is a semantic correctness issue, not just a performance one. A pod could land on a node that hasn't actually been wiped.

### Medium: Audit Logger Has No Buffering

`logger.go` does `json.Marshal` + `w.Write()` synchronously in the PostBind/PostFilter hot path. In PostFilter, it also builds a slice of all filtered node reasons. At 5000 nodes with most filtered, that's a large JSON entry written synchronously to stderr. Under high scheduling throughput, this becomes an I/O bottleneck.

### Low: Webhook Config Is Static With Linear Lookup

Namespace rules are loaded once at startup and scanned linearly on every admission request. Adding a namespace requires restarting the webhook. At scale (hundreds of namespaces with distinct rules), both the operational burden and per-request cost increase.

### Low: CompositeProvider Always Tries CRD First

In the common case where only ConfigMap policies exist, every `GetPolicy()` call tries CRD lister → gets `NotFound` → falls back to ConfigMap. An event-driven approach (watch for CRD creation, flip a flag) would eliminate the constant fallback path.

---

## Risk Summary

| Risk | Impact | Likelihood at Scale |
|------|--------|-------------------|
| Policy parse overhead | Scheduling latency degrades linearly with pod count | Near-certain above 100 pods/sec |
| Pod lister full scan | Controller CPU spikes, API server pressure | Near-certain above 10K pods |
| Sequential attestation | Stale TEE state, security window | Near-certain above 50 TEE nodes |
| No leader election | Data races on node labels | Certain with HA deployment |
| Scheduler-controller race | Pod placed on unwiped node | Possible under churn |
| Singleton failure | Complete scheduling outage | Possible during rollouts |

---

## What's Well-Designed

- Workqueue deduplication in the node state controller — multiple pod terminations on the same node collapse to one reconcile
- Plugin separation (Region/Privacy/Confidential/Audit) is clean and independently testable
- Fail-closed on malformed CRD policy is the right default for a security-oriented scheduler
- Metric cardinality is bounded — no per-pod or per-namespace labels that could explode
- The `Base` plugin pattern reduces boilerplate without hiding important behavior
- Table-driven tests throughout are easy to extend

---

## Recommendations by Scale Tier

### Tier 1: Small Cluster (< 100 nodes, < 5K pods, < 10 pods/sec)

Current architecture works as-is. These are the only changes worth making at this scale.

| Recommendation | Severity | Effort | Why Now |
|---|---|---|---|
| Cache parsed `*Policy` via `atomic.Pointer` | Critical | S (1 sprint) | Eliminates 8+ JSON parse cycles per pod. Pure win at any scale, no downside. |
| Add retry/backoff to provider singleton init | Medium | S (hours) | A transient failure during startup permanently bricks the scheduler. One-line `sync.Once` replacement. |
| Add `spec.nodeName` informer index to controller | Critical | S (hours) | Trivial to add, prevents O(all-pods) scan from day one. No reason to ship without it. |

### Tier 2: Medium Cluster (100–500 nodes, 5K–25K pods, 10–50 pods/sec)

Label races and sequential bottlenecks start to bite. HA deployment becomes necessary.

| Recommendation | Severity | Effort | Why Now |
|---|---|---|---|
| Leader election for node state + attestation controllers | High | M (1 sprint) | HA requires multiple replicas. Without leader election, label patches race and produce non-deterministic state. |
| Switch MergePatch to SSA (Server-Side Apply) or use `resourceVersion` | Medium | M (1 sprint) | Concurrent patches silently overwrite each other. SSA with field ownership is the k8s-native solution. |
| Parallel attestation verification via `errgroup.SetLimit` | High | S (hours) | 100 TEE nodes × 200ms = 20s per tick. With timeouts, ticks overlap. Bounded parallelism (e.g., 10 concurrent) reduces tick time to ~2s. |
| Buffered audit logger | Medium | S (hours) | At 50 pods/sec with 500 nodes, PostFilter generates large JSON entries synchronously. A `bufio.Writer` with periodic flush eliminates the I/O stall. |
| Webhook namespace rule index (map lookup) | Low | S (hours) | Linear scan of 100+ rules on every admission. Replace `[]NamespaceRule` with `map[string]NamespaceRule`. |

### Tier 3: Large Cluster (500–2000 nodes, 25K–100K pods, 50–200 pods/sec)

Eventual consistency and scheduling correctness become the dominant concerns.

| Recommendation | Severity | Effort | Why Now |
|---|---|---|---|
| PreFilter gate for node wipe state | Critical | M (1 sprint) | The scheduler-controller race allows pods onto unwiped nodes. A PreFilter that checks real-time node state (not cached labels) for `privacy: high` pods closes the gap. Tradeoff: adds one API call per high-privacy scheduling cycle. |
| Per-cycle policy snapshot (read once in PreFilter, store in CycleState) | High | M (1 sprint) | Even with cached policy, reading it 8 times per cycle is wasteful. A single PreFilter read stored in CycleState gives all plugins a consistent snapshot with zero redundant work. |
| Event-driven CRD detection in CompositeProvider | Low | S (hours) | Eliminates the constant CRD `NotFound` → ConfigMap fallback path. Watch for CRD creation, flip an `atomic.Bool`. |
| Webhook hot-reload via ConfigMap/CRD | Medium | M (1 sprint) | Static config requires webhook restart for namespace changes. At this scale, namespace churn is routine. Watch a ConfigMap and rebuild the rule index on change. |
| Scheduling throughput metrics (p50/p99 per plugin) | Medium | S (hours) | Default histogram buckets don't capture sub-ms scheduling. Add per-plugin duration histograms with custom buckets (0.1ms–100ms) to identify bottlenecks before they become outages. |

### Tier 4: Very Large Cluster (2000+ nodes, 100K+ pods, 200+ pods/sec)

Fundamental architectural changes needed. The current single-scheduler, label-based coordination model hits its ceiling.

| Recommendation | Severity | Effort | Why Now |
|---|---|---|---|
| Node state as CRD (not labels) | High | L (2–3 sprints) | Labels have a 256KB total limit per node and no history/versioning. A `NexaNodeState` CRD with status subresource gives structured state, optimistic concurrency via status updates, and watch-based cache invalidation. |
| Scheduler sharding (multi-profile or multi-instance) | High | L (2–3 sprints) | Single scheduler instance becomes a bottleneck. Shard by namespace or workload class, each instance running the full plugin chain. Requires careful coordination to avoid double-scheduling. |
| Async audit pipeline (channel + background writer) | Medium | M (1 sprint) | Synchronous JSON writes in the scheduling hot path add latency variance. A bounded channel with a background goroutine draining to a structured logging backend (e.g., fluentd sidecar) decouples scheduling from I/O entirely. |
| Attestation result caching with TTL | Medium | S (hours) | Re-verifying all TEE nodes every 5 minutes is wasteful when attestation state rarely changes. Cache results with a configurable TTL (e.g., 1 hour), re-verify on failure or expiry. |
| Controller sharding by node selector | High | L (2 sprints) | Single controller reconciling all nodes doesn't scale. Shard controllers by node pool (e.g., GPU vs. CPU, region), each responsible for a subset of nodes via label selector on the informer. |
| Reserve plugin for wipe-state fencing | Critical | L (2 sprints) | The PreFilter gate from Tier 3 adds API latency. A Reserve plugin that atomically claims the node (patches `nexa.io/reserved-by=<pod>`) before Bind, with Unreserve cleanup on failure, provides strong consistency without per-node API calls in Filter. |

---

## Conclusion

The architecture is sound for small-to-medium clusters (sub-500 nodes, sub-10K pods). The issues above become material at production scale for the target use case (shared clusters running sensitive AI workloads, which implies large clusters with GPU nodes and high pod churn).

Tier 1 recommendations should be implemented immediately — they're low-effort, high-impact, and have no tradeoffs. Tier 2 is necessary before any HA or production deployment. Tier 3 addresses correctness gaps that matter for the privacy guarantees Nexa promises. Tier 4 is a future architecture evolution if Nexa targets large enterprise clusters.
