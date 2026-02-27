# Integration Guide

## Running Alongside the Default Scheduler

Nexa Scheduler runs as a secondary scheduler. It only handles pods with `schedulerName: nexa-scheduler`. All other pods continue to use the default `kube-scheduler`.

To opt a pod into Nexa scheduling:

```yaml
spec:
  schedulerName: nexa-scheduler
```

Pods without this field (or with `schedulerName: default-scheduler`) are ignored by Nexa. Both schedulers can run simultaneously without conflict.

## Monitoring

### Prometheus Scrape Configuration

The scheduler serves metrics at `https://<pod-ip>:10259/metrics` with a self-signed TLS certificate. Add a scrape config:

```yaml
scrape_configs:
  - job_name: nexa-scheduler
    scheme: https
    tls_config:
      insecure_skip_verify: true
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names: [nexa-system]
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        regex: nexa-scheduler
        action: keep
```

Or use a ServiceMonitor if you have the Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: nexa-scheduler
  namespace: nexa-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: nexa-scheduler
  endpoints:
    - port: https
      scheme: https
      tlsConfig:
        insecureSkipVerify: true
```

### Metric Reference

| Metric | Type | Labels | What to watch |
|--------|------|--------|---------------|
| `nexa_scheduling_duration_seconds` | Histogram | `result` (scheduled/failed) | P99 latency; alert if > 1s |
| `nexa_filter_results_total` | Counter | `plugin`, `result` | High `rejected` rate may indicate misconfigured labels |
| `nexa_score_distribution` | Histogram | `plugin` | Score spread; all zeros means scoring is ineffective |
| `nexa_isolation_violations_total` | Counter | `reason` | Any non-zero count means pods attempted cross-org scheduling |
| `nexa_policy_evaluations_total` | Counter | `plugin`, `result` | `result=error` means policy ConfigMap is unreachable |

### Sample Grafana Queries

Scheduling success rate:

```promql
sum(rate(nexa_scheduling_duration_seconds_count{result="scheduled"}[5m]))
/
sum(rate(nexa_scheduling_duration_seconds_count[5m]))
```

P99 scheduling latency:

```promql
histogram_quantile(0.99, rate(nexa_scheduling_duration_seconds_bucket{result="scheduled"}[5m]))
```

Isolation violation rate:

```promql
sum(rate(nexa_isolation_violations_total[5m])) by (reason)
```

Policy error rate (should be zero):

```promql
sum(rate(nexa_policy_evaluations_total{result="error"}[5m])) by (plugin)
```

## Logging

### Log Format

The audit plugin writes structured JSON to stderr (one entry per line). Each entry follows this schema:

```json
{
  "timestamp": "2026-02-27T12:00:00Z",
  "level": "INFO",
  "event": "scheduled",
  "pod": {
    "name": "my-pod",
    "namespace": "default",
    "privacy": "high",
    "region": "us-west1",
    "zone": "us-west1-a",
    "org": "alpha"
  },
  "node": "worker-0",
  "policy": {
    "regionEnabled": true,
    "privacyEnabled": true
  },
  "filters": []
}
```

Events: `"scheduled"` (success), `"scheduling_failed"` (all nodes filtered), `"filter_details"` (debug-level per-node reasons).

### Fluentd / Fluent Bit

Parse scheduler logs as JSON. The container writes to stderr, which Kubernetes captures at `/var/log/containers/nexa-scheduler-*.log`. Example Fluent Bit parser:

```ini
[PARSER]
    Name        nexa_audit
    Format      json
    Time_Key    timestamp
    Time_Format %Y-%m-%dT%H:%M:%SZ
```

### Loki

If using Grafana Loki with Promtail, logs are automatically collected from the container. Query examples:

```logql
{namespace="nexa-system", container="nexa-scheduler"} | json | event="scheduling_failed"
```

```logql
{namespace="nexa-system", container="nexa-scheduler"} | json | pod_privacy="high"
```

### Log Levels

The scheduler inherits kube-scheduler's verbosity flag:

```yaml
# In values.yaml or deployment args:
args:
  - --v=2    # default: INFO-level audit logs
  - --v=5    # debug: includes per-node filter_details entries
```

The audit plugin's debug mode is controlled by verbosity level 5+.

## CI/CD

### Building from Source

```bash
# Build the binary
go build -o bin/nexa-scheduler ./cmd/scheduler

# Run tests
go test ./...

# Run linter
golangci-lint run

# Build container image
docker build -t nexascheduler/nexa-scheduler:$(git rev-parse --short HEAD) .
```

### Helm Values per Environment

**Development (Kind):**

```bash
helm install nexa-scheduler deploy/helm/nexa-scheduler/ \
  --namespace nexa-system --create-namespace \
  --set image.tag=dev \
  --set image.pullPolicy=Never \
  --set scheduler.leaderElect=false
```

**Staging / Production:**

```bash
helm install nexa-scheduler deploy/helm/nexa-scheduler/ \
  --namespace nexa-system --create-namespace \
  --set image.tag=v0.1.0 \
  --set image.repository=registry.example.com/nexa-scheduler \
  --set replicaCount=2 \
  --set scheduler.leaderElect=true \
  --set policy.privacy.strictOrgIsolation=true
```

Key production settings:
- `replicaCount: 2` with `scheduler.leaderElect: true` for high availability
- `policy.privacy.strictOrgIsolation: true` for cluster-wide org isolation
- Use a private registry for the container image

### Non-Helm Deployment

Static manifests are available at `deploy/manifests/`. Apply in order:

```bash
kubectl apply -f deploy/manifests/namespace.yaml
kubectl apply -f deploy/manifests/serviceaccount.yaml
kubectl apply -f deploy/manifests/clusterrole.yaml
kubectl apply -f deploy/manifests/clusterrolebinding.yaml
kubectl apply -f deploy/manifests/configmap-scheduler.yaml
kubectl apply -f deploy/manifests/configmap-policy.yaml
kubectl apply -f deploy/manifests/deployment.yaml
kubectl apply -f deploy/manifests/service.yaml
```

Edit `deploy/manifests/deployment.yaml` to change the image tag and `deploy/manifests/configmap-policy.yaml` for policy settings.

## Policy Management

### ConfigMap Structure

The policy ConfigMap (`nexa-scheduler-config` in `nexa-system`) contains a single key `policy.json`:

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

### Hot Reload

Policy changes take effect automatically when the informer cache syncs (typically within seconds). No scheduler restart required.

To update the policy:

```bash
kubectl edit configmap nexa-scheduler-config -n nexa-system
```

Or patch programmatically:

```bash
kubectl patch configmap nexa-scheduler-config -n nexa-system \
  --type merge -p '{"data":{"policy.json":"{\"regionPolicy\":{\"enabled\":true},\"privacyPolicy\":{\"enabled\":true,\"defaultPrivacy\":\"high\",\"strictOrgIsolation\":true}}"}}'
```

### Validation

The scheduler validates the ConfigMap on every read:
- Missing `policy.json` key: error (fail-closed — pods not scheduled)
- Malformed JSON: error (fail-closed)
- Invalid `defaultPrivacy` value: error (only `""`, `"standard"`, `"high"` accepted)
- Valid JSON with unknown fields: silently ignored (forward-compatible)

## RBAC

The scheduler's ClusterRole provides:

| Access | Resources | Why |
|--------|-----------|-----|
| Read | Pods, Nodes, ConfigMaps, Namespaces, Services, PVs, PVCs, PDBs, ReplicaSets, StatefulSets, StorageClasses, CSI resources, ResourceSlices, ResourceClaims, DeviceClasses | Required by kube-scheduler framework informers |
| Write | Pods/binding (create), Pods/status (patch/update), Events (create/patch/update) | Scheduler must bind pods and report status |
| Write | Leases (create/get/update) | Leader election (when enabled) |

The scheduler has **no write access to Nodes or ConfigMaps**. It cannot modify the labels or policies it reads.
