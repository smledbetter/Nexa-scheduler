Run all quality gates in order. Fix any failures before moving on.

```bash
export PATH="$HOME/go/bin:$PATH"
```

1. `go build ./...`
2. `golangci-lint run`
3. `go test ./...`
4. `go test -cover ./...`

If any gate fails: diagnose the root cause, fix it, and re-run all gates from the beginning. Do not skip or work around failures.
