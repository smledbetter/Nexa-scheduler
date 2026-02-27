# Quickstart Guide

Run the Nexa Scheduler on a local Kind cluster in under 10 minutes.

## Prerequisites

| Tool | Minimum version | Check |
|------|----------------|-------|
| Docker | 20.10+ | `docker version` |
| Kind | 0.20+ | `kind version` |
| kubectl | 1.28+ | `kubectl version --client` |
| Helm | 3.12+ | `helm version` |
| Go | 1.23+ | `go version` |

## 1. Create a Kind Cluster

Create a cluster with 3 worker nodes (the scheduler needs multiple nodes to demonstrate placement decisions):

```bash
cat <<EOF | kind create cluster --name nexa-demo --config - --wait 120s
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
EOF
```

Verify:

```bash
kubectl get nodes
```

Expected output (node names may differ):

```
NAME                      STATUS   ROLES           AGE   VERSION
nexa-demo-control-plane   Ready    control-plane   60s   v1.x.x
nexa-demo-worker          Ready    <none>          45s   v1.x.x
nexa-demo-worker2         Ready    <none>          45s   v1.x.x
nexa-demo-worker3         Ready    <none>          45s   v1.x.x
```

## 2. Build and Load the Scheduler Image

From the repository root:

```bash
docker build -t nexascheduler/nexa-scheduler:demo .
kind load docker-image nexascheduler/nexa-scheduler:demo --name nexa-demo
```

## 3. Install via Helm

```bash
helm install nexa-scheduler deploy/helm/nexa-scheduler/ \
  --namespace nexa-system \
  --create-namespace \
  --set image.tag=demo \
  --set image.pullPolicy=Never \
  --wait \
  --timeout 120s
```

Verify the scheduler is running:

```bash
kubectl get pods -n nexa-system
```

Expected output:

```
NAME                              READY   STATUS    RESTARTS   AGE
nexa-scheduler-xxxxxxxxxx-xxxxx   1/1     Running   0          30s
```

## 4. Label Worker Nodes

The scheduler uses `nexa.io/*` labels on nodes to make placement decisions. Label the 3 workers to simulate a multi-region, multi-org cluster:

```bash
# Get worker node names
WORKERS=($(kubectl get nodes --no-headers -o custom-columns=':metadata.name' | grep worker))

# Worker 0: US West, wiped, belongs to org "alpha"
kubectl label node ${WORKERS[0]} \
  nexa.io/region=us-west1 \
  nexa.io/zone=us-west1-a \
  nexa.io/wiped=true \
  nexa.io/last-workload-org=alpha

# Worker 1: US West, NOT wiped, no org assignment
kubectl label node ${WORKERS[1]} \
  nexa.io/region=us-west1 \
  nexa.io/zone=us-west1-b

# Worker 2: EU West, wiped, belongs to org "beta"
kubectl label node ${WORKERS[2]} \
  nexa.io/region=eu-west1 \
  nexa.io/zone=eu-west1-a \
  nexa.io/wiped=true \
  nexa.io/last-workload-org=beta
```

Verify labels:

```bash
kubectl get nodes -L nexa.io/region,nexa.io/wiped,nexa.io/last-workload-org
```

## 5. Schedule Sample Pods

### Region-filtered pod

This pod requests `us-west1`. It should land on Worker 0 or Worker 1 (both are in us-west1):

```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: region-test
  labels:
    nexa.io/region: "us-west1"
spec:
  schedulerName: nexa-scheduler
  containers:
    - name: test
      image: busybox:latest
      command: ["sleep", "300"]
EOF
```

Check placement:

```bash
kubectl get pod region-test -o wide
```

The `NODE` column should show a `us-west1` worker. The EU worker is filtered out.

### High-privacy pod

This pod requires a wiped node and belongs to org "alpha". Only Worker 0 qualifies (wiped + org=alpha):

```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: privacy-test
  labels:
    nexa.io/privacy: "high"
    nexa.io/org: "alpha"
    nexa.io/region: "us-west1"
spec:
  schedulerName: nexa-scheduler
  containers:
    - name: test
      image: busybox:latest
      command: ["sleep", "300"]
EOF
```

Check placement:

```bash
kubectl get pod privacy-test -o wide
```

The pod should land on Worker 0 (the only us-west1 node that is wiped and belongs to org alpha).

### Unschedulable pod

This pod requires a wiped node for org "gamma", but no node matches. It should stay Pending:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: rejected-test
  labels:
    nexa.io/privacy: "high"
    nexa.io/org: "gamma"
spec:
  schedulerName: nexa-scheduler
  containers:
    - name: test
      image: busybox:latest
      command: ["sleep", "300"]
EOF
```

After 10 seconds:

```bash
kubectl get pod rejected-test
```

Expected: `STATUS` is `Pending`. No node satisfies the constraints (no wiped node belongs to org gamma).

## 6. Verify Audit Logs

The scheduler logs every placement decision as structured JSON:

```bash
kubectl logs -n nexa-system -l app.kubernetes.io/name=nexa-scheduler | grep '"event"'
```

You should see entries like:

```json
{"timestamp":"2026-02-27T12:00:00Z","level":"INFO","event":"scheduled","pod":{"name":"region-test","namespace":"default","region":"us-west1"},"node":"nexa-demo-worker","policy":{"regionEnabled":true,"privacyEnabled":true}}
{"timestamp":"2026-02-27T12:00:01Z","level":"INFO","event":"scheduled","pod":{"name":"privacy-test","namespace":"default","privacy":"high","region":"us-west1","org":"alpha"},"node":"nexa-demo-worker","policy":{"regionEnabled":true,"privacyEnabled":true}}
{"timestamp":"2026-02-27T12:00:02Z","level":"INFO","event":"scheduling_failed","pod":{"name":"rejected-test","namespace":"default","privacy":"high","org":"gamma"},"policy":{"regionEnabled":true,"privacyEnabled":true},"filters":[...]}
```

## 7. View Metrics

Port-forward to the scheduler's secure endpoint:

```bash
kubectl port-forward -n nexa-system svc/nexa-scheduler 10259:10259 &
```

Fetch metrics (the scheduler uses HTTPS with a self-signed cert):

```bash
curl -sk https://localhost:10259/metrics | grep nexa_
```

Key metrics to look for:

```
nexa_filter_results_total{plugin="NexaRegion",result="accepted"} ...
nexa_filter_results_total{plugin="NexaPrivacy",result="rejected"} ...
nexa_scheduling_duration_seconds_bucket{result="scheduled",...} ...
nexa_isolation_violations_total{reason="cross_org"} ...
nexa_policy_evaluations_total{plugin="NexaRegion",result="success"} ...
```

Stop the port-forward:

```bash
kill %1
```

## 8. Cleanup

```bash
kubectl delete pod region-test privacy-test rejected-test --ignore-not-found
helm uninstall nexa-scheduler -n nexa-system
kind delete cluster --name nexa-demo
```

## Next Steps

- [Architecture](architecture.md) -- how the scheduler works internally
- [Threat Model](threat-model.md) -- security analysis and attack surfaces
- [Integration Guide](integration.md) -- monitoring, logging, and CI/CD setup
