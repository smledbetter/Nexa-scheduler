# Nexa Scheduler

Compliance-aware workload placement for Kubernetes. Nexa enforces privacy, isolation, and data sovereignty at scheduling time — with a complete audit trail.

> Run your workloads where they belong — safely, automatically, and with a full audit trail.

## The Problem

Default Kubernetes schedulers optimize for utilization, not isolation. In shared or multi-tenant clusters, sensitive workloads (healthcare, legal, finance) risk co-location with untrusted neighbors. Compliance requirements like HIPAA, GDPR, and SOC2 are hard to enforce at scheduling time, and auditors need evidence of *where* workloads ran and *why*.

## What Nexa Does

Nexa runs as a separate scheduler alongside `kube-scheduler`. Pods opt in with `schedulerName: nexa-scheduler` and declare their constraints via `nexa.io/*` labels:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: medical-ai-inference
  labels:
    nexa.io/org: "hospital-a"
    nexa.io/privacy: "high"
    nexa.io/region: "us-central1"
    nexa.io/confidential: "required"
spec:
  schedulerName: nexa-scheduler
  containers:
    - name: analyzer
      image: medical-llm:latest
```

What happens:
1. **Webhook** validates label provenance — is `hospital-a` authorized in this namespace?
2. **Region Filter** rejects nodes not in `us-central1`
3. **Privacy Filter** rejects dirty nodes and nodes used by other orgs
4. **Confidential Filter** rejects non-TEE nodes
5. **Scoring** ranks surviving nodes (exact zone > region match, recently wiped > stale)
6. **Audit** logs the decision as structured JSON with full reasoning
7. If no node qualifies, the pod stays **Pending** — fail-closed, never fail-open

## Features

| Feature | Description |
|---------|-------------|
| **Region & Zone Affinity** | Filter and score nodes by `nexa.io/region` and `nexa.io/zone` labels |
| **Privacy-Aware Scheduling** | Org isolation, node cleanliness (`nexa.io/wiped`), scored placement |
| **Confidential Compute** | TEE capability filtering (`tdx`, `sev-snp`), disk encryption, remote attestation |
| **Node Cooldown** | Reject nodes with stale wipes via configurable cooldown period |
| **Admission Webhook** | Validates `nexa.io/*` label provenance per namespace. Fail-closed |
| **Policy via CRD + ConfigMap** | `NexaPolicy` CRD with ConfigMap fallback. Hot-reloadable |
| **Node State Controller** | Manages node labels based on pod lifecycle |
| **Audit Logging** | Structured JSON logs of every placement decision with per-node rejection reasons |
| **Compliance Reports** | Offline CLI produces HIPAA/SOC2/GDPR artifacts from audit logs |
| **Prometheus Metrics** | Scheduling duration, filter results, isolation violations, policy evaluations |

## How It Relates to Other Tools

| Tool | What it decides | Relationship |
|------|----------------|--------------|
| **Kueue / Volcano** | *When* does this job run? (queuing, fairness) | Complement. Kueue admits, Nexa places safely |
| **OPA Gatekeeper** | *Can* this pod exist? (admission policy) | Different boundary. Gatekeeper validates at admission, Nexa at scheduling |
| **Taints/Affinities** | Binary allow/deny | Nexa adds scored placement, temporal freshness, org isolation, and audit |

## Prerequisites

- Docker 20.10+
- [Kind](https://kind.sigs.k8s.io/) 0.20+
- kubectl 1.28+
- Helm 3.12+
- Go 1.23+ (to build from source)

## Quick Start

### 1. Create a Kind cluster

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

### 2. Build and load the image

```bash
docker build -t nexascheduler/nexa-scheduler:demo .
kind load docker-image nexascheduler/nexa-scheduler:demo --name nexa-demo
```

### 3. Install via Helm

```bash
helm install nexa-scheduler deploy/helm/nexa-scheduler/ \
  --namespace nexa-system \
  --create-namespace \
  --set image.tag=demo \
  --set image.pullPolicy=Never \
  --wait --timeout 120s
```

### 4. Label worker nodes

```bash
kubectl label node nexa-demo-worker \
  nexa.io/region=us-west1 nexa.io/zone=us-west1-a \
  nexa.io/wiped=true nexa.io/last-workload-org=alpha

kubectl label node nexa-demo-worker2 \
  nexa.io/region=eu-west1 nexa.io/zone=eu-west1-a \
  nexa.io/wiped=true nexa.io/last-workload-org=beta

kubectl label node nexa-demo-worker3 \
  nexa.io/region=us-west1 nexa.io/zone=us-west1-b
```

### 5. Test scheduling

**Region filtering** — pod lands in us-west1, not EU:

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

kubectl get pod region-test -o wide
```

**Privacy isolation** — pod lands only on the wiped node matching org "alpha":

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

kubectl get pod privacy-test -o wide
```

**Fail-closed** — no node matches org "gamma", pod stays Pending:

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

kubectl get pod rejected-test
# STATUS: Pending
```

### 6. View audit logs

```bash
kubectl logs -n nexa-system -l app.kubernetes.io/name=nexa-scheduler | grep '"event"'
```

### 7. View metrics

```bash
kubectl port-forward -n nexa-system svc/nexa-scheduler 10259:10259 &
curl -sk https://localhost:10259/metrics | grep nexa_
kill %1
```

### 8. Clean up

```bash
kubectl delete pod region-test privacy-test rejected-test --ignore-not-found
helm uninstall nexa-scheduler -n nexa-system
kind delete cluster --name nexa-demo
```

## Project Structure

```
cmd/
  scheduler/         # scheduler binary
  controller/        # node state controller
  webhook/           # admission webhook
  compliance/        # compliance report CLI
pkg/
  plugins/           # scheduler framework plugins
    region/          # region/zone affinity (Filter + Score)
    privacy/         # privacy & node cleanliness (Filter + Score)
    confidential/    # TEE & encrypted disk (Filter + Score)
    audit/           # structured JSON audit logging (PostBind)
  policy/            # policy engine (CRD + ConfigMap providers)
  metrics/           # Prometheus metric definitions
  nodestate/         # node state tracking
  webhook/           # admission webhook handler
  compliance/        # compliance report generator
deploy/
  helm/              # Helm charts (scheduler, node-controller, webhook)
  manifests/         # raw YAML for non-Helm users
docs/                # documentation
```

## Label Reference

### Pod Labels (set by workload submitters)

| Label | Values | Purpose |
|-------|--------|---------|
| `nexa.io/region` | e.g., `us-west1` | Geographic region constraint |
| `nexa.io/zone` | e.g., `us-west1-a` | Availability zone constraint |
| `nexa.io/privacy` | `standard`, `high` | Privacy level requirement |
| `nexa.io/org` | e.g., `alpha` | Organization identity |
| `nexa.io/confidential` | `required` | Requires TEE-capable node |

### Node Labels (set by operators / node state controller)

| Label | Values | Purpose |
|-------|--------|---------|
| `nexa.io/region` | e.g., `us-west1` | Node's geographic region |
| `nexa.io/zone` | e.g., `us-west1-a` | Node's availability zone |
| `nexa.io/wiped` | `true` | Node has been sanitized |
| `nexa.io/last-workload-org` | e.g., `alpha` | Last org that used this node |
| `nexa.io/tee` | `tdx`, `sev-snp` | Hardware TEE capability |
| `nexa.io/confidential` | `true` | Node supports confidential compute |
| `nexa.io/disk-encrypted` | `true` | Encrypted disk |

## Documentation

- [Quick Start Guide](docs/quickstart.md) — detailed walkthrough with verification steps
- [Architecture](docs/architecture.md) — scheduling pipeline, plugin design, policy engine
- [Threat Model](docs/threat-model.md) — security analysis and attack surfaces
- [Integration Guide](docs/integration.md) — monitoring, logging, Kueue co-deployment
- [Roadmap](docs/ROADMAP.md) — milestone status and sprint history
- [PRD](docs/PRD.md) — product requirements and design rationale

## License

TBD
