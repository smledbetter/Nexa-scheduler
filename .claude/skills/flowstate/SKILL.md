---
name: flowstate
description: Flowstate sprint process — planning, execution, shipping, and metrics workflow. Invoked when starting or running a sprint.
---

# Flowstate Sprint Workflow

This project uses the Flowstate sprint process. When asked to "start the next sprint" or "run a sprint," follow this workflow.

## File Locations

- **Flowstate dir**: `~/.flowstate/nexa-scheduler/`
- **Config**: `~/.flowstate/nexa-scheduler/flowstate.config.md` (quality gates, agent strategy)
- **Baselines**: `~/.flowstate/nexa-scheduler/metrics/baseline-sprint-N.md`
- **Retrospectives**: `~/.flowstate/nexa-scheduler/retrospectives/sprint-N.md`
- **Metrics**: `~/.flowstate/nexa-scheduler/metrics/`
- **Metrics collection**: Use `mcp__flowstate__collect_metrics` MCP tool (or legacy `~/.flowstate/nexa-scheduler/metrics/collect.sh`)
- **Progress**: `~/.flowstate/nexa-scheduler/progress.md` (operational state for next session)
- **Roadmap**: `docs/ROADMAP.md` (in this repo -- create if missing)
- **Skills**: `.claude/skills/` (in this repo)

## How to Determine the Next Sprint

1. If no `docs/ROADMAP.md` exists, this is Sprint 0 (see below).
2. Read `docs/ROADMAP.md` -- find the first phase not marked done.
3. Find the highest-numbered baseline in `~/.flowstate/nexa-scheduler/metrics/` -- that's your sprint number.
4. Read that baseline for starting state, gate commands, and H7 audit instructions.

## Sprint 0: Project Setup (planning only -- no code)

Sprint 0 is a dedicated planning sprint. It produces the roadmap, baseline, and conventions that all future sprints depend on. No code is written. It still gets full metrics tracking.

**Phase 1+2: RESEARCH then PLAN**

Read these files:
- `PRD.md` (fully -- every section)
- `~/.flowstate/nexa-scheduler/flowstate.config.md`
- All files in `.claude/skills/`
- `~/.flowstate/hypotheses.json`

Then do ALL of the following:

1. **Verify gate commands.** Run each gate command below. If any don't work for this project (wrong tool, missing dependency), update them in this file AND in `~/.flowstate/nexa-scheduler/flowstate.config.md`. Record what works and what doesn't.
  1. `go build ./...`
  2. `golangci-lint run`
  3. `go test ./...`
  4. `go test -cover ./...`

2. **Create `docs/ROADMAP.md`.**
   - Break PRD milestones into sprint-sized phases. Each phase = one sprint.
   - Right-sizing guide: a phase should be deliverable in 10-60 minutes of active agent time, produce 500-2500 LOC, and have a clear "done" state that gates can verify.
   - Phases that are mostly research or refactoring will be smaller. Phases that build new features from scratch will be larger.
   - Number phases starting from 1 (Sprint 0 is this planning sprint).
   - Include a "Current State" section at the top (tests, coverage, LOC, milestone status).

3. **Fill in the Conventions section** in `CLAUDE.md`:
   - Language, framework, test runner
   - Lint rules and coverage floors
   - Coding standards specific to this stack
   - Any constraints from the PRD (e.g., "no .unwrap() on network data", "strict mode")

4. **Write the initial baseline** at `~/.flowstate/nexa-scheduler/metrics/baseline-sprint-1.md`:
   - Current git SHA, test count (0 if greenfield), coverage status
   - Gate commands and whether each passes right now
   - 5 H7 instructions picked from `.claude/skills/` to audit in Sprint 1

5. **Commit**: `git add -A && git commit -m "sprint 0: project setup"`

When done, say: "Ready for Phase 3: SHIP whenever you want to proceed."

**Phase 3: SHIP**

Sprint 0's Phase 3 follows the same steps as all sprints (collect metrics, write import JSON, write retro). The differences for Sprint 0:
- `tests_total`: 0 (or current count if pre-existing)
- `tests_added`: 0
- `coverage_pct`: null
- `loc_added`: LOC from git diff --stat (roadmap, baseline, conventions -- not application code)
- `gates_first_pass`: null (no code gates to run)
- `gates_first_pass_note`: "planning sprint -- no code gates"
- Phase 3 steps 6-8 below still apply (retro, baseline already written in step 4, roadmap already written in step 2)
- Hypothesis results: at minimum H1 and H11 (does the process work for this project type?)

Then follow steps 1-8 in Phase 3 below (skip steps that Sprint 0 already completed above).

---

## Phase 1+2: THINK then EXECUTE (Sprint 1+)

Read these files first:
- `PRD.md`
- `docs/ROADMAP.md` (find this sprint's phase)
- The current baseline (see above)
- `~/.flowstate/nexa-scheduler/progress.md` (if exists -- operational state from last session)
- `~/.flowstate/nexa-scheduler/flowstate.config.md`
- The previous sprint's retro (if exists)
- All files in `.claude/skills/`
- `~/.flowstate/hypotheses.json` (canonical hypothesis IDs, names, valid results)

**THINK**: Use Plan mode. Iterate on the plan until solid, considering all 5 skill perspectives (PM, UX, Architect, Production Engineer, Security Auditor):
0. FEASIBILITY CHECK: List new external dependencies, verify they exist in the registry, run a minimal spike on the highest-risk task. Flag unverified or experimental deps with a fallback plan. If the spike fails, revise scope before proceeding.
1. Write acceptance criteria in Gherkin format for the phase scope
2. Produce an implementation plan (group tasks by file dependency; parallel where no shared files)
3. For each task: files to read, files to write

**EXECUTE**: After planning, switch to auto-accept mode and implement:
- **Choose the right multi-agent strategy for the sprint scope:**
  - **Sequential (default):** Implement in the main session. Best when files reference each other or total LOC < 800.
  - **Subagents:** Use `Task` tool with `isolation: "worktree"` for 2-3 independent packages that share no files. Each subagent gets its own copy of the repo. Best for parallel plugin development (e.g., region + privacy plugins simultaneously).
  - **Agent Teams:** Use `TeamCreate` + named teammates for large sprints (1200+ LOC) with 3+ genuinely independent workstreams. Each teammate is a full session with its own context. Best for phases like CRD + Node State Controller + migration path.
  - **Writer/Reviewer:** After implementation, spawn a separate subagent (fresh context, no bias) to review the code for edge cases and convention compliance before running gates.
- Evaluate which strategy fits during THINK phase and document the choice in the plan.
- Run quality gates after every meaningful change (use `/gate` command)
- Commit atomically after completing logical units of work
- Save gate output to `~/.flowstate/nexa-scheduler/metrics/sprint-N-gates.log`
- If any gate fails: fix, re-run, max 3 cycles
- Optional preventive gates (run after core gates pass):
  - `bash ~/Sites/Flowstate/tools/deps_check.sh` (verify new deps exist in registry)
  - `bash ~/Sites/Flowstate/tools/sast_check.sh` (static security analysis)
  - `bash ~/Sites/Flowstate/tools/deadcode_check.sh` (detect unused exports/deps)

When all gates pass, say: "Ready for Phase 3: SHIP whenever you want to proceed."

## Phase 3: SHIP

1. **Collect metrics** using Flowstate MCP tools:
   - Call `mcp__flowstate__sprint_boundary` with project_path and sprint_marker to find the boundary timestamp
   - Call `mcp__flowstate__list_sessions` with project_path to find the session ID(s) for this sprint
   - Call `mcp__flowstate__collect_metrics` with project_path, session_ids, and the boundary timestamp as "after"
   - Save the raw metrics response to `~/.flowstate/nexa-scheduler/metrics/sprint-N-metrics.json`

2. **Write import JSON** at `~/.flowstate/nexa-scheduler/metrics/sprint-N-import.json`:
   - Start from the MCP metrics response (`sprint-N-metrics.json`) as the base
   - Add these fields:
     ```json
     {
       "project": "nexa-scheduler",
       "sprint": N,
       "label": "NS SN",
       "phase": "[phase name from roadmap]",
       "metrics": {
         "...everything from sprint-N-metrics.json...",
         "tests_total": "<current test count>",
         "tests_added": "<tests added this sprint>",
         "coverage_pct": "<current coverage % or null>",
         "lint_errors": 0,
         "gates_first_pass": "<true|false>",
         "gates_first_pass_note": "<note if false, empty string if true>",
         "loc_added": "<LOC from git diff --stat>",
         "loc_added_approx": false,
         "task_type": "<feature|bugfix|refactor|infra|planning|hardening>",
         "rework_rate": "<from sprint-N-metrics.json, or null>",
         "judge_score": "<[scope, test_quality, gate_integrity, convention, diff_hygiene] 1-5 each, or null>",
         "judge_blocked": "<true if judge prevented stopping, false otherwise, or null>",
         "judge_block_reason": "<reason string if blocked, or null>",
         "coderabbit_issues": "<number of CodeRabbit issues on PR, or null>",
         "coderabbit_issues_valid": "<number human agreed were real, or null>",
         "mutation_score_pct": "<mutation score if run, or null>"
       },
       "hypotheses": [
         // Use IDs and names from ~/.flowstate/hypotheses.json
         // Valid results: confirmed, partially_confirmed, inconclusive, falsified
         {"id": "H1", "name": "<from hypotheses.json>", "result": "...", "evidence": "..."},
         {"id": "H5", "name": "<from hypotheses.json>", "result": "...", "evidence": "..."},
         {"id": "H7", "name": "<from hypotheses.json>", "result": "...", "evidence": "..."}
       ]
     }
     ```
   - The schema matches `sprints.json` entries exactly -- same field names, same types
   - Validate: call `mcp__flowstate__import_sprint` with dry_run=true
   - Fix any errors before proceeding. Warnings (auto-corrections) are ok.

3. **Write retrospective** at `~/.flowstate/nexa-scheduler/retrospectives/sprint-N.md`:
   - What was built (deliverables, test count, files changed, LOC)
   - Metrics comparison vs previous sprint
   - What worked / what failed, with evidence
   - H7 audit: check the 5 instructions listed in the baseline
   - Hypothesis results table (include at minimum H1, H5, H7)
   - Change proposals as diffs (with `- Before` / `+ After` blocks)

4. **Do NOT apply skill changes** -- proposals stay in the retro for human review

5. **Roadmap review** -- Before writing the next baseline, review `docs/ROADMAP.md` against what this sprint revealed. Ask:
   - **Scope check:** Should adjacent phases merge (too small separately) or split (too large)? Compare estimated LOC to actual LOC from this sprint as a calibration.
   - **Order check:** Should any later phase move earlier (blocker or prerequisite discovered) or later (less critical than assumed)?
   - **Dependency check:** Did this sprint surface a prerequisite (test helpers, new deps, tooling) that a future phase assumes exists but doesn't? Add it to that phase's deliverables.
   - If changes are needed, apply them to `docs/ROADMAP.md` and commit separately with a descriptive message. If no changes are needed, note "Roadmap reviewed — no changes" in the retro.

6. **Commit**: `git add -A && git commit -m "sprint N: [description]"`

7. **Write next baseline** at `~/.flowstate/nexa-scheduler/metrics/baseline-sprint-{N+1}.md`:
   - Current git SHA, test count, coverage %, lint error count
   - Gate commands and current status
   - 5 H7 instructions to audit next sprint (rotate from skills)

8. **Update roadmap**: mark this phase done in `docs/ROADMAP.md`, update Current State section

9. **Write progress file** at `~/.flowstate/nexa-scheduler/progress.md`:
   - What was completed this sprint (list of deliverables)
   - What failed or was deferred (and why)
   - What the next session should do first
   - Any blocked items or external dependencies awaiting resolution
   - Current gate status (all passing? which ones?)
   This is operational state for the next agent session, not analysis. Overwrite any previous progress.md.

10. **Completion check** -- print this checklist with [x] or [MISSING] for each:
   - metrics/sprint-N-metrics.json exists (raw MCP metrics response)
   - metrics/sprint-N-import.json exists (complete import-ready JSON, validated via MCP dry_run)
   - retrospectives/sprint-N.md has hypothesis table (H1, H5, H7) and change proposals
   - metrics/baseline-sprint-{N+1}.md exists with SHA, tests, coverage, gates, H7 instructions
   - progress.md written (current state for next session)
   - docs/ROADMAP.md updated
   - Code committed
   Fix any MISSING items before declaring done.
