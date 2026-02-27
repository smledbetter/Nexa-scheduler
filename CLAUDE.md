# Nexa Scheduler

Enable organizations to run sensitive AI and batch workloads on shared Kubernetes clusters with strong, automated privacy and compliance controls — without sacrificing performance, simplicity, or cost efficiency.

## Workflow

- Start each sprint in a fresh session. One sprint = one session.
- Use Plan mode first. Iterate until the plan is solid, then switch to auto-accept for implementation.
- Run `/gate` after every meaningful code change. Do not batch gate fixes to the end.
- Use subagents only for parallel independent work (3+ files, zero overlap) or investigation. Implement in the main session.
- `/clear` between sprint phases (THINK/EXECUTE done → clear → SHIP).

## Conventions

### Language & Toolchain

- **Language:** Go 1.23+
- **Build:** `go build ./...`
- **Test runner:** `go test ./...`
- **Coverage:** `go test -cover ./...` (threshold TBD after Sprint 1)
- **Lint:** `golangci-lint run` (config in `.golangci.yml`)
- **Module:** Go modules (`go.mod`)

### Dependencies

- **Scheduler Framework:** `k8s.io/kube-scheduler` (out-of-tree scheduler entrypoint)
- **Client library:** `k8s.io/client-go` (informers, watchers)
- **API types:** `k8s.io/api`, `k8s.io/apimachinery`
- **Metrics:** `github.com/prometheus/client_golang`
- **Logging:** `k8s.io/klog/v2` (standard for k8s components)
- Pin k8s dependencies to a single release version (e.g., all v0.31.x). Mixing k8s dependency versions causes build failures.

### Project Structure

```
cmd/
  scheduler/         # scheduler binary entrypoint
  controller/        # node state controller entrypoint (Phase 7+)
pkg/
  plugins/           # scheduler framework plugins (Filter, Score, PostBind)
    region/          # region/zone affinity
    privacy/         # privacy & node cleanliness
    audit/           # audit logging plugin
  policy/            # policy engine (ConfigMap reader, evaluation)
  metrics/           # Prometheus metric definitions
  nodestate/         # node state tracking types and helpers
deploy/
  helm/              # Helm chart
  manifests/         # raw YAML for non-Helm users
docs/                # documentation
```

### Coding Standards

- Follow [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).
- **Error handling:** Return errors, don't panic. Wrap errors with `fmt.Errorf("context: %w", err)` for stack context. Never swallow errors silently.
- **Naming:** Use short, clear names. Avoid stutter (`policy.PolicyEngine` → `policy.Engine`). Exported names get doc comments.
- **Interfaces:** Define at the consumer, not the producer. Keep interfaces small (1-3 methods). The scheduler framework interfaces are the exception — implement what the framework requires.
- **Tests:** Table-driven tests for Filter/Score plugins. Use `testing.T` subtests. Test files live next to source files (`foo.go` → `foo_test.go`).
- **No `init()` functions.** Pass dependencies explicitly.
- **Context:** Thread `context.Context` through all operations. Respect cancellation.
- **Kubernetes types:** Use typed clients, not `unstructured.Unstructured`, except for CRD work.

### Gate Commands

```
go build ./...
golangci-lint run
go test ./...
go test -cover ./...
```

IMPORTANT: PATH must include `$HOME/go/bin` for golangci-lint. Run: `export PATH="$HOME/go/bin:$PATH"`

## Known Issues & Gotchas

- **k8s framework imports:** Plugin interfaces (`FilterPlugin`, `ScorePlugin`, `Handle`) are in `k8s.io/kubernetes/pkg/scheduler/framework`. Data types (`CycleState`, `NodeInfo`, `Status`) are in `k8s.io/kube-scheduler/framework`. Always import both. Follow the `sigs.k8s.io/scheduler-plugins` pattern for `go.mod` replace directives.
- **k8s v1.34+ interface changes:** `CycleState` and `NodeInfo` are interfaces passed by value (not pointer). `Score` takes `NodeInfo`, not `string`. Check `go doc` for exact signatures after `go mod tidy`.
