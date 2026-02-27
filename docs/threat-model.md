# Threat Model

## Scope

This document covers the attack surface of the Nexa Scheduler as deployed via Helm on a Kubernetes cluster. It covers the scheduler binary, its policy ConfigMap, node/pod labels, audit logs, metrics endpoint, and container image.

**Out of scope:** The Kubernetes API server, etcd, kubelet, container runtime, and network fabric are assumed to be secured by the cluster operator. The Node State Controller (Phase 10) will require a separate threat model addendum.

## Trust Boundaries

```
                    ┌─────────────────────────────────────────┐
                    │           Kubernetes API Server          │
                    │  (trusted — RBAC enforced, TLS required) │
                    └──────┬────────────────────┬─────────────┘
                           │                    │
              Informer watches           Pod submissions
              (ConfigMap, Nodes,         (any authenticated
               Pods, etc.)                user/service account)
                           │                    │
                    ┌──────▼────────────────────▼─────────────┐
                    │          Nexa Scheduler Pod              │
                    │  ┌─────────────────────────────────┐    │
                    │  │ Plugins: Region, Privacy, Audit  │    │
                    │  │ Policy: ConfigMapProvider (cache) │    │
                    │  │ Metrics: /metrics (HTTPS, no auth)│    │
                    │  └─────────────────────────────────┘    │
                    │  Non-root (UID 65532), read-only FS     │
                    │  All capabilities dropped                │
                    └──────┬────────────────────┬─────────────┘
                           │                    │
                    Audit logs            Metrics endpoint
                    (stdout JSON)         (HTTPS :10259)
                           │                    │
                    ┌──────▼──────┐      ┌──────▼──────┐
                    │ Log pipeline │      │ Prometheus   │
                    │ (Fluentd,    │      │ (scrapes     │
                    │  Loki, etc.) │      │  /metrics)   │
                    └─────────────┘      └─────────────┘
```

## Attack Surfaces

### 1. Pod Labels — Label Spoofing

**Threat:** A malicious actor with pod-create permissions sets `nexa.io/org=alpha` on their pod to land on nodes reserved for org alpha, or sets `nexa.io/privacy=standard` to bypass high-privacy constraints.

**Impact:** Cross-organization workload co-location. A pod from org X runs on a node trusted by org Y. Sensitive data residency requirements violated.

**Mitigation:** The scheduler trusts pod labels at face value by design — it is a placement engine, not an admission controller. Label validation requires a separate mechanism.

**Recommendation:** Deploy the Nexa ValidatingAdmissionWebhook (`deploy/helm/nexa-webhook/`) to restrict `nexa.io/org` labels based on namespace rules. Example rule: only pods in namespace `alpha-workloads` may set `nexa.io/org=alpha`.

**Status:** Mitigated — ValidatingAdmissionWebhook (`pkg/webhook/`) enforces label integrity per namespace. Opt-in via `nexa.io/webhook=enabled` namespace label. Fail-closed by default.

**Code reference:** `pkg/webhook/handler.go` validates `nexa.io/org` and `nexa.io/privacy` labels against namespace-scoped rules. `pkg/plugins/privacy/privacy.go` reads labels at scheduling time (post-admission).

### 2. Node Labels — Unauthorized Mutation

**Threat:** An attacker with node-write permissions (or a compromised kubelet) changes `nexa.io/wiped=true` on a node that has not actually been sanitized, causing high-privacy pods to land on dirty nodes.

**Impact:** Sensitive workloads run on nodes with residual data from previous tenants. Privacy isolation guarantee broken.

**Mitigation:** RBAC restricts node label writes. The scheduler's own ClusterRole only has `get`/`list`/`watch` on nodes (no write access). The cluster admin controls which principals can modify node objects.

**Recommendation:** Restrict node label writes to a dedicated Node State Controller service account (Phase 10). Use audit logging to detect unauthorized label changes.

**Status:** Partial — RBAC mitigates. The Node State Controller (Phase 10) will further restrict label authority.

**Code reference:** `deploy/helm/nexa-scheduler/templates/clusterrole.yaml` — scheduler has read-only access to nodes.

### 3. ConfigMap Policy — Tampering

**Threat:** An attacker with ConfigMap-write permissions in the `nexa-system` namespace modifies `nexa-scheduler-config` to disable privacy enforcement (`"enabled": false`) or inject permissive defaults.

**Impact:** All privacy filtering silently disabled. Pods schedule without isolation checks.

**Adversarial scenario:** Attacker gains write access to the `nexa-system` namespace (e.g., through a compromised CI pipeline service account). They patch the ConfigMap:
```json
{"privacyPolicy": {"enabled": false}, "regionPolicy": {"enabled": false}}
```
All subsequent pods schedule without any Nexa constraints. The change appears in audit logs (policy snapshot shows `privacyEnabled: false`), but only if someone is monitoring.

**Mitigation:** RBAC restricts ConfigMap writes in `nexa-system`. The scheduler validates ConfigMap content and fails closed on invalid JSON (the `Parse()` and `Validate()` functions reject malformed data). Audit logs include a `PolicySnapshot` field showing the active policy state at decision time.

**Recommendation:** Alert on policy ConfigMap changes (Kubernetes audit log or custom controller). Consider a ValidatingAdmissionWebhook that rejects ConfigMap updates which disable required policies.

**Status:** Mitigated — RBAC + fail-closed + audit trail.

**Code reference:** `pkg/policy/parse.go`, `pkg/policy/validate.go`, `pkg/plugins/audit/audit.go` (PolicySnapshot in log entries).

### 4. Audit Logs — Log Injection

**Threat:** A pod submitter sets label values containing newlines, JSON-breaking characters, or misleading content (e.g., `nexa.io/org=alpha\n{"event":"scheduled","node":"fake"}`) to corrupt the audit trail.

**Impact:** Tampered audit logs. Injected entries could make it appear that pods were scheduled to nodes they never ran on.

**Mitigation:** The audit logger uses Go's `encoding/json.Marshal()`, which properly escapes special characters in string values. Newlines become `\n`, quotes become `\"`. Each log entry is a single JSON line terminated by `\n`. The `PodRef` struct only copies label values into string fields — no raw byte injection is possible.

**Status:** Mitigated — JSON marshaling handles escaping.

**Code reference:** `pkg/plugins/audit/logger.go:80-87` — `json.Marshal` produces escaped output.

### 5. Metrics Endpoint — Unauthenticated Access

**Threat:** The `/metrics` endpoint is accessible without authentication (via `--authorization-always-allow-paths=/metrics`). An attacker with network access to port 10259 can read scheduling telemetry.

**Impact:** Information disclosure: scheduling latency, filter acceptance/rejection rates, isolation violation counts, policy evaluation errors. This reveals cluster scheduling patterns and workload characteristics.

**Adversarial scenario:** An attacker with pod-exec access in the cluster runs `curl -sk https://nexa-scheduler.nexa-system:10259/metrics` and observes `nexa_isolation_violations_total{reason="cross_org"} 47`, revealing that cross-org scheduling attempts are frequent — indicating a multi-tenant cluster with imperfect isolation.

**Mitigation:** The endpoint uses HTTPS (TLS). The Service is `ClusterIP` (not exposed externally by default). Network policies can further restrict access.

**Recommendation:** Apply a NetworkPolicy that restricts access to port 10259 to the Prometheus namespace. If metrics must be exposed externally, use a metrics aggregator (e.g., PushGateway) rather than direct exposure.

**Status:** Accepted risk — intentional for Prometheus scraping. Standard practice for kube-scheduler components.

**Code reference:** `deploy/helm/nexa-scheduler/templates/deployment.yaml` — `--authorization-always-allow-paths=/metrics`.

### 6. Scheduler Binary — Container Escape

**Threat:** A vulnerability in the scheduler binary or Go runtime allows an attacker to escape the container and access the host node.

**Impact:** Full cluster compromise. The scheduler's ServiceAccount token provides cluster-wide read access to pods, nodes, configmaps, and more.

**Mitigation:**
- **Distroless image:** `gcr.io/distroless/static:nonroot` — no shell, no package manager, minimal attack surface
- **Non-root:** Runs as UID 65532 (nonroot user)
- **Read-only filesystem:** `readOnlyRootFilesystem: true`
- **No capabilities:** All Linux capabilities dropped
- **No privilege escalation:** `allowPrivilegeEscalation: false`

**Status:** Mitigated — defense in depth via container hardening.

**Code reference:** `Dockerfile` (distroless base, USER 65532), `deploy/helm/nexa-scheduler/values.yaml` (security context).

### 7. Dependencies — Supply Chain Attack

**Threat:** A compromised or malicious upstream dependency (among the 30+ k8s modules and Prometheus client) introduces backdoor code.

**Impact:** Arbitrary code execution within the scheduler container. Given the scheduler's RBAC, this provides read access to cluster-wide pod and node metadata, plus write access to pod bindings.

**Mitigation:**
- Go module checksums (`go.sum`) verify dependency integrity
- All k8s dependencies pinned to a single release version (v0.34.1/v1.34.1)
- 30+ `replace` directives in `go.mod` ensure consistent dependency resolution
- Dependencies are standard Kubernetes ecosystem libraries (well-maintained, widely audited)

**Recommendation:** Run `govulncheck` periodically. Pin Go toolchain version in CI. Consider using SLSA provenance for the container image.

**Status:** Partially mitigated — checksums and pinning provide integrity, but no automated vulnerability scanning.

**Code reference:** `go.mod`, `go.sum`.

## Residual Risks

| Risk | Severity | Notes |
|------|----------|-------|
| Pod label spoofing | High | Requires admission webhook — outside scheduler scope |
| ConfigMap policy drift | Medium | Detectable via audit logs, but no automated alerting |
| Metrics information disclosure | Low | ClusterIP-only, standard kube-scheduler practice |
| Stale node labels | Medium | Mitigated by Phase 10 Node State Controller |
| Log volume under heavy load | Low | Every decision produces a JSON line; consider sampling for high-throughput clusters |

## Recommendations Summary

1. **Deploy an admission webhook** to validate `nexa.io/*` pod labels against submitter identity
2. **Restrict node label writes** to the Node State Controller service account (Phase 10)
3. **Alert on policy ConfigMap changes** via Kubernetes audit logging
4. **Apply NetworkPolicy** to restrict metrics endpoint access to Prometheus
5. **Run `govulncheck`** in CI to detect known vulnerabilities in dependencies
6. **Monitor audit logs** for `privacyEnabled: false` in PolicySnapshot — indicates policy was disabled
