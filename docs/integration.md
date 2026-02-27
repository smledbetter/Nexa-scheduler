# Integration Guide

## Running Alongside the Default Scheduler

Nexa Scheduler runs as a secondary scheduler. It only handles pods with `schedulerName: nexa-scheduler`. All other pods continue to use the default `kube-scheduler`.

To opt a pod into Nexa scheduling:

```yaml
spec:
  schedulerName: nexa-scheduler
```

Pods without this field (or with `schedulerName: default-scheduler`) are ignored by Nexa. Both schedulers can run simultaneously without conflict.

## Running Nexa alongside Kueue

[Kueue](https://kueue.sigs.k8s.io/) is the Kubernetes-native job queueing system. Kueue controls **when** a workload runs (quota, fairness, priority). Nexa controls **where** a workload lands (privacy, region, compliance). They complement each other and share no state, CRDs, or APIs.

### Interaction Model

```
Job submitted (suspend: true)
        │
        ▼
   ┌─────────┐
   │  Kueue   │  Checks quota, fairness, priority
   └────┬─────┘  Sets suspend: false when admitted
        │
        ▼
   ┌─────────┐
   │  Nexa    │  Filters nodes by region, privacy, org
   └────┬─────┘  Scores and binds pod to best node
        │
        ▼
   Pod running on compliant node
```

1. You submit a Job with `spec.suspend: true` and a `kueue.x-k8s.io/queue-name` label.
2. Kueue evaluates quota and fairness. When resources are available, it sets `suspend: false`.
3. The Job controller creates a Pod. Because `schedulerName: nexa-scheduler` is set in the pod template, the pod enters Nexa's scheduling queue.
4. Nexa filters and scores nodes using privacy, region, and org policies, then binds the pod.

If Kueue never admits the workload (quota exhausted), no pod is created and Nexa is never involved.

### Prerequisites

- Kueue v0.10+ installed (v0.16.x recommended, see compatibility matrix below)
- Nexa Scheduler installed (any version)
- Job pod template must set `schedulerName: nexa-scheduler`

### Label Propagation

Kueue manages Jobs, not bare Pods. Nexa reads labels from the Pod, not the Job. Labels must appear in **both** the Job metadata and the pod template:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: privacy-batch-job
  labels:
    kueue.x-k8s.io/queue-name: team-queue  # Kueue reads this
spec:
  suspend: true  # Kueue manages this field
  template:
    metadata:
      labels:
        kueue.x-k8s.io/queue-name: team-queue  # propagated to pod
        nexa.io/region: us-west1                # Nexa reads this
        nexa.io/privacy: high                   # Nexa reads this
        nexa.io/org: alpha                      # Nexa reads this
    spec:
      schedulerName: nexa-scheduler
      restartPolicy: Never
      containers:
        - name: training
          image: my-training:v1
          resources:
            requests:
              cpu: "2"
              memory: "4Gi"
```

If `nexa.io/*` labels are only on the Job (not the pod template), Nexa will not see them and the pod will be scheduled without privacy or region constraints.

### ResourceFlavor Alignment

Kueue [ResourceFlavors](https://kueue.sigs.k8s.io/docs/concepts/resource_flavor/) can include `nodeLabels` that constrain which nodes a workload runs on. These constraints are **additive** to Nexa's region filter — both must be satisfiable.

**Aligned configuration** (recommended):

```yaml
# Kueue ResourceFlavor
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: us-west1
spec:
  nodeLabels:
    nexa.io/region: us-west1  # matches Nexa's region label
```

When a workload is admitted through this flavor, Kueue adds `nodeLabels` as a nodeSelector on the pod. Nexa's region filter independently verifies the same constraint. Both agree — no conflict.

**Potential misconfiguration:**

If a ResourceFlavor specifies `nexa.io/region: us-west1` but the pod template requests `nexa.io/region: eu-west1`, the intersection is empty and the pod will stay Pending. Diagnose with:

```bash
# Check Kueue workload admission status
kubectl get workloads -n <namespace> -o wide

# Check Nexa scheduler logs for filter rejections
kubectl logs -n nexa-system -l app.kubernetes.io/name=nexa-scheduler | grep scheduling_failed
```

### Installation Example

```bash
# Install Kueue (v0.16.1)
kubectl apply --server-side -f \
  https://github.com/kubernetes-sigs/kueue/releases/download/v0.16.1/manifests.yaml

# Install Nexa Scheduler
helm install nexa-scheduler deploy/helm/nexa-scheduler/ \
  --namespace nexa-system --create-namespace \
  --set image.tag=v0.1.0

# Install Nexa Node Controller (if using node state tracking)
helm install nexa-node-controller deploy/helm/nexa-node-controller/ \
  --namespace nexa-system

# Install Nexa Webhook (if using label validation)
helm install nexa-webhook deploy/helm/nexa-webhook/ \
  --namespace nexa-system \
  --set tls.caBundle=<base64-ca-bundle>
```

No shared Helm values are required. Each component has independent configuration.

### Kueue Resource Setup

A minimal Kueue setup for use with Nexa:

```yaml
# ResourceFlavor aligned with Nexa regions
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: us-west1
spec:
  nodeLabels:
    nexa.io/region: us-west1
---
# ClusterQueue with quota
apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  name: team-cluster-queue
spec:
  namespaceSelector: {}
  resourceGroups:
    - coveredResources: ["cpu", "memory"]
      flavors:
        - name: us-west1
          resources:
            - name: cpu
              nominalQuota: "100"
            - name: memory
              nominalQuota: "200Gi"
---
# LocalQueue in the team's namespace
apiVersion: kueue.x-k8s.io/v1beta1
kind: LocalQueue
metadata:
  name: team-queue
  namespace: ml-team
spec:
  clusterQueue: team-cluster-queue
```

### Version Compatibility

| Kueue Version | Nexa Version | Status | Notes |
|---------------|--------------|--------|-------|
| v0.16.x | v0.1.0+ | Tested | Smoke tested with 3 scenarios |
| v0.10.x–v0.15.x | v0.1.0+ | Expected compatible | Same `v1beta1` API |
| < v0.10 | — | Untested | Earlier API versions may differ |

### Troubleshooting

**Pod stays Pending after Kueue admits it:**
- Check that the pod template has `schedulerName: nexa-scheduler` (not the default scheduler).
- Check Nexa scheduler logs for filter rejections: the pod may fail region or privacy constraints.

**Kueue never admits the workload:**
- Verify the LocalQueue references a valid ClusterQueue: `kubectl get localqueue -n <ns> -o wide`.
- Check ClusterQueue quota: `kubectl get clusterqueue <name> -o yaml` — look at `status.flavorsUsage`.

**Labels not picked up by Nexa:**
- Ensure `nexa.io/*` labels are on `.spec.template.metadata.labels`, not just `.metadata.labels`.
- Use `kubectl get pod <name> -o jsonpath='{.metadata.labels}'` to verify labels on the created pod.

**ResourceFlavor conflict:**
- If both Kueue and Nexa constrain region, the intersection must be non-empty. A pod requesting `nexa.io/region: eu-west1` admitted through a `us-west1` flavor will never schedule.

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
