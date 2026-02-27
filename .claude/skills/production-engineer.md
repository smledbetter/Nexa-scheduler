---
name: production-engineer
description: Testing strategy, code quality gates, and TDD process. Consult during implementation and review phases.
---

# Production Engineer

## TDD Process

1. **Red** -- Write a failing test that describes the desired behavior.
2. **Green** -- Write the minimum code to make the test pass.
3. **Refactor** -- Clean up while keeping tests green.
4. **Commit** -- Each red-green-refactor cycle is one atomic commit when working solo. When delegated to a subagent, test + implementation in one commit is acceptable if tests are written first within the session.

Never write production code without a failing test first. Never refactor while tests are red.

## Quality Gates

All of these must pass before a milestone is considered complete:

1. Type check / build -- zero errors
2. Lint -- zero warnings (or at documented pre-existing baseline)
3. Tests -- all pass
4. Coverage -- meets project threshold (configure in flowstate.config.md)

Gate commands are project-specific. Configure them in `flowstate.config.md`.

## Test Organization

- Test files mirror the source structure.
- Group tests by module with descriptive names.
- One assertion per test when practical. Multiple assertions are fine if testing a single logical behavior.

## Conventions

- **Mock external deps.** File I/O, network calls, and environment access must be mocked.
- **Reset state between tests.** Ensure isolation. No shared mutable state across tests.
- **No test interdependence.** Tests must pass in any order and in isolation.
- **Deterministic tests.** No reliance on timing, random values, or system state.
- **Test behavior, not implementation.** Tests should survive refactors that preserve behavior.

## Dependency Management

- New dependencies require justification: what it provides, maintenance status, and audit history.
- Minimize transitive dependencies.
- Audit for known vulnerabilities regularly.

## Test Coverage Verification

Before declaring gates passed, confirm that every new or modified source file has a corresponding test file that was also added or modified in this sprint. Check with `git diff --name-only` against the sprint baseline SHA — if a source file appears without a matching test file, the feature is incomplete. This is the lightweight version of TDD enforcement: we don't require test-before-code commit ordering, but we do require tests to exist before shipping.

## Review Checklist

- [ ] New code has corresponding tests
- [ ] Tests describe behavior, not implementation details
- [ ] Mocks are minimal (only external boundaries)
- [ ] No flaky tests (timing, ordering, or environment dependent)
- [ ] Coverage meets project threshold
- [ ] Type check / build passes cleanly

## Anti-Patterns

- Testing private methods directly
- Merging code that "works but tests will come later"
- Mocking the module under test
- Tests that pass when the feature is broken
- Ignoring test failures with skip annotations in shipped code
