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

### 8. TEE Node Labels — Self-Reported Attestation

**Threat:** A node operator or compromised kubelet sets `nexa.io/tee=tdx` and `nexa.io/confidential=true` on a node that does not actually have TEE hardware. The confidential compute plugin trusts these labels at scheduling time.

**Impact:** Workloads requiring confidential compute (hardware memory encryption, isolated execution) are placed on nodes without TEE protection. Data in memory is not encrypted and is vulnerable to physical or hypervisor-level attacks.

**Mitigation:** Nexa treats TEE labels as policy signals, not cryptographic guarantees. This is consistent with the node label trust model (same as `nexa.io/wiped`). The label indicates the operator has attested the node's TEE capability through their own provisioning pipeline.

**Recommendation:** Integrate with a remote attestation framework (e.g., Intel Trust Authority, AMD SEV-SNP attestation) to verify TEE claims before labeling nodes. This is a future enhancement — Nexa's label-based model is the scheduling layer; attestation belongs in the provisioning layer.

**Status:** Mitigated when `RequireAttestation=true` in ConfidentialPolicy. The attestation controller (`pkg/nodestate/attestation_controller.go`) periodically verifies TEE nodes against a remote attestation service and patches `nexa.io/tee-attested`, `nexa.io/tee-attestation-time`, and `nexa.io/tee-trust-anchor` labels. The confidential plugin Filter rejects nodes that fail attestation (fail-closed). Without `RequireAttestation`, the original label-trust model applies.

**Code reference:** `pkg/attestation/` (Attester interface + HTTP client), `pkg/plugins/confidential/confidential.go` (Filter checks 4/4b/4c), `pkg/nodestate/attestation_controller.go` (periodic verification loop).

### 9. GPU VRAM Encryption Gap

**Threat:** A pod scheduled to a TEE-capable node with `nexa.io/confidential=required` processes data on a GPU. GPU VRAM is not protected by CPU-level TEEs (Intel TDX, AMD SEV-SNP). Sensitive data in GPU memory is accessible to the hypervisor and co-tenant attacks.

**Impact:** Confidential compute guarantee does not extend to GPU memory. AI/ML workloads using GPU acceleration may have sensitive model weights or training data exposed.

**Mitigation:** This is a hardware industry limitation, not a scheduling gap. No mainstream GPU currently offers full VRAM encryption that integrates with CPU TEE attestation. Confidential GPU compute is an active research area (NVIDIA H100 Confidential Computing is early-stage).

**Recommendation:** Document this limitation for operators. For truly confidential AI workloads, restrict to CPU-only compute until GPU confidential computing matures. The confidential compute plugin does not check for GPU presence — operators must make this trade-off.

**Status:** Accepted risk — hardware limitation, not addressable at the scheduling layer.

### 10. Wipe Timestamp Clock Skew

**Threat:** A node's system clock is skewed (ahead or behind the scheduler's clock). The `nexa.io/wipe-timestamp` label contains an RFC3339 timestamp set by the operator/automation at wipe time. The privacy filter compares this against `time.Now()` on the scheduler pod.

**Impact:**
- **Node clock ahead:** Wipe timestamp appears to be in the future. The privacy filter accepts the node (duration since wipe is negative, always within cooldown). This is the safe direction — future timestamps are treated as "just wiped."
- **Scheduler clock ahead:** Wipe appears older than it actually is. A freshly wiped node might be rejected as stale. This causes false rejections (availability impact, not security impact).
- **Node clock behind:** Wipe timestamp is older than reality. A recently wiped node appears stale. Same as scheduler-ahead: false rejections.

**Mitigation:** The fail-closed design means clock skew can cause false rejections but not false acceptances. NTP synchronization across the cluster is a prerequisite for correct operation (standard Kubernetes assumption).

**Recommendation:** Ensure NTP is configured on all cluster nodes and the scheduler pod's host. A clock skew of more than `CooldownHours` would cause all wiped nodes to be rejected — monitor for scheduling failures if cooldown is enabled.

**Status:** Accepted risk — mitigated by fail-closed design and NTP assumption.

### 11. Attestation Service Unavailability

**Threat:** The remote attestation service (e.g., Intel Trust Authority, Azure MAA) is unreachable due to network partition, service outage, or misconfiguration. The attestation controller cannot verify TEE nodes.

**Impact:** Availability, not security. The attestation controller is fail-closed: any verification error results in `nexa.io/tee-attested=false`. Nodes that were previously attested retain their last-verified labels until the next verification cycle patches them to `false`. The confidential plugin rejects nodes with `tee-attested=false` or missing labels. Result: all confidential workloads become unschedulable until the attestation service recovers.

**Mitigation:**
- `AttestationMaxAgeHours` provides a grace period — previously attested nodes remain valid for the configured duration even if the service is temporarily down
- The attestation controller retries on each interval cycle (default 5 minutes)
- The node state controller continues to function independently (pod lifecycle labels are unaffected)

**Recommendation:** Monitor the attestation controller logs for repeated verification failures. Set `AttestationMaxAgeHours` to a value that balances security freshness with availability tolerance (e.g., 24-48 hours). Consider deploying the attestation service with high availability.

**Status:** Accepted risk — fail-closed by design. This is an availability trade-off, not a security gap.

## Residual Risks

| Risk | Severity | Notes |
|------|----------|-------|
| Pod label spoofing | High | Mitigated by ValidatingAdmissionWebhook |
| ConfigMap policy drift | Medium | Detectable via audit logs, but no automated alerting |
| Metrics information disclosure | Low | ClusterIP-only, standard kube-scheduler practice |
| Stale node labels | Medium | Mitigated by Node State Controller |
| Log volume under heavy load | Low | Every decision produces a JSON line; consider sampling for high-throughput clusters |
| TEE label spoofing | Medium | Mitigated with RequireAttestation=true; self-reported without attestation |
| Attestation service unavailability | Low | Fail-closed; availability impact only, not security |
| GPU VRAM unencrypted | Medium | Hardware limitation; no scheduling-layer mitigation available |
| Wipe timestamp clock skew | Low | Fail-closed design prevents false acceptances; NTP required |

## Recommendations Summary

1. **Deploy an admission webhook** to validate `nexa.io/*` pod labels against submitter identity
2. **Restrict node label writes** to the Node State Controller service account (Phase 10)
3. **Alert on policy ConfigMap changes** via Kubernetes audit logging
4. **Apply NetworkPolicy** to restrict metrics endpoint access to Prometheus
5. **Run `govulncheck`** in CI to detect known vulnerabilities in dependencies
6. **Monitor audit logs** for `privacyEnabled: false` in PolicySnapshot — indicates policy was disabled
