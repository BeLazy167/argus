# Quick Wins — Ship Now

Commit and deploy the already-coded changes:
1. Remove worker hard cap (3 → `MAX_CONCURRENT_REVIEWS`, default 10) — `review.go:123`
2. Blast radius file fetching — `review.go` + `context.go` (already coded)
3. Tracing instructions in base prompt — `review.go` (already coded)
4. Specialist scope boundaries removed — `specialists.go` (already coded)

---

# Lead Agent Architecture — Full Design Doc

## Problem

Current architecture: 4 specialists run as independent workers from a shared queue. No communication between them. Scoring layer deduplicates after the fact. This misses:
- Cross-file bugs where one specialist's finding should inform another's analysis
- Impact chain bugs where callers break due to changed return types
- Arithmetic/logic chains that span multiple files
- Scenarios that predict real breakage but aren't connected to review findings

## Solution: Coordinated Agent Team

A Lead Agent coordinates 6 specialist agents that run in parallel, with findings synthesized in a cross-check phase.

## Architecture

```
                    ┌─────────────────┐
                    │   Triage Stage   │ (same as current)
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  Lead Agent     │ Phase 1: BRIEFING
                    │  (Brief)        │ Reads full diff + memory + blast radius
                    └────────┬────────┘ Produces file briefs + cross-cutting concerns
                             │
          ┌──────────────────┼──────────────────┐
          │                  │                  │
    ┌─────▼─────┐    ┌──────▼──────┐    ┌──────▼──────┐
    │ Per-File   │    │ PR-Level    │    │ PR-Level    │
    │ Specialists│    │ Simulation  │    │ Blast Radius│
    │ (parallel) │    │ Agent       │    │ Agent       │
    ├────────────┤    ├─────────────┤    ├─────────────┤
    │ Bug Hunter │    │ Runs stored │    │ Traces deps │
    │ Security   │    │ scenarios   │    │ from graph  │
    │ Archit.    │    │ against     │    │ Fetches     │
    │ Regression │    │ the diff    │    │ caller code │
    └─────┬──────┘    └──────┬──────┘    └──────┬──────┘
          │                  │                  │
          │     Phase 2a: PARALLEL EXECUTION    │
          │     All agents run independently    │
          │                  │                  │
          └──────────────────┼──────────────────┘
                             │
                    ┌────────▼────────┐
                    │  Lead Agent     │ Phase 2b: BROADCAST
                    │  (Broadcast)    │ Collects Phase 2a findings
                    └────────┬────────┘ Extracts critical cross-agent signals
                             │           Broadcasts to relevant agents
          ┌──────────────────┼──────────────────┐
          │                  │                  │
    ┌─────▼─────┐    ┌──────▼──────┐    ┌──────▼──────┐
    │ Targeted   │    │ Targeted    │    │ Targeted    │
    │ Second     │    │ Second      │    │ Second      │
    │ Pass       │    │ Pass        │    │ Pass        │
    │ (only if   │    │ (only if    │    │ (only if    │
    │  flagged)  │    │  flagged)   │    │  flagged)   │
    └─────┬──────┘    └──────┬──────┘    └──────┬──────┘
          │                  │                  │
          │     Phase 2c: FOCUSED SECOND PASS   │
          │     Only agents with relevant       │
          │     cross-findings re-examine       │
          │                  │                  │
          └──────────────────┼──────────────────┘
                             │
                    ┌────────▼────────┐
                    │  Lead Agent     │ Phase 3: CROSS-CHECK
                    │  (Synthesis)    │ Sees ALL findings (both passes)
                    └────────┬────────┘ Dedup + cross-reference + gap analysis
                             │
                    ┌────────▼────────┐
                    │   Post to       │
                    │   GitHub        │
                    └─────────────────┘
```

## Communication Layer: Hybrid Broadcast

After Phase 2a (independent work), the Lead collects all findings and identifies **cross-agent signals** — findings from one agent that another agent needs to know about. Only agents with relevant signals get a second pass.

### How Broadcast Works

**Lead Broadcast prompt (Phase 2b):**
```
You've received findings from 6 agents reviewing a PR. Identify cross-agent signals —
cases where one agent's finding is relevant to another agent's work.

For each signal:
1. Which agent found it?
2. Which agent(s) should re-examine based on this finding?
3. What specific question should they answer?

Examples of cross-agent signals:
- Security found auth bypass in session.ts → Bug Hunter should check if callers of
  session.validate() handle the new failure mode
- Simulation predicts scenario "payment flow" breaks → Architecture should verify
  error propagation in the payment chain
- Blast Radius shows api.ts depends on changed return type → Regression should
  verify api.ts still handles the new return shape

Only flag signals where a second pass would likely find something new.
Return [] if no cross-agent signals are needed.

Output JSON:
[{
  "from_agent": "security",
  "to_agent": "bug_hunter",
  "signal": "Auth bypass found in session.validate() — returns true on 401",
  "question": "Check callers of session.validate() — do they handle a false positive auth?"
  "files_to_check": ["src/api/handler.ts", "src/middleware/auth.ts"]
}]
```

**Second Pass prompt (Phase 2c):**
Each targeted agent gets a focused, narrow prompt:
```
A teammate found something relevant to your domain:

Signal from {from_agent}: {signal}

Question for you: {question}

Files to examine:
{file contents of files_to_check}

Review these files specifically for the question above. Only report findings
directly related to this signal. Return [] if the code handles this correctly.
```

### When Second Pass is Skipped

- If Lead Broadcast returns `[]` → skip Phase 2c entirely, go to Phase 3
- If PR is small (<3 files) → skip broadcast, cross-check is enough
- If non-deep review → entire team architecture is skipped (single-pass mode)

### Cost of Communication Layer

| Scenario | Extra calls | Extra tokens |
|----------|-------------|-------------|
| No signals found | +1 (broadcast) | ~2K |
| 2 signals, 2 agents re-examine | +1 (broadcast) + 2 (second pass) | ~5K |
| 4 signals, all agents re-examine | +1 (broadcast) + 4 (second pass) | ~8K |
| Average expected | +1 broadcast + ~1.5 second passes | ~4K (~$0.01) |

## Agent Definitions

### Agent 1: Lead Agent

**Role:** Coordinator. Runs twice — once to brief, once to cross-check.

**Phase 1 — Briefing:**
- Input: Full diff (abbreviated), PR description, memory context, blast radius nodes, scenario list
- Output: `LeadBrief` — per-file focus areas for each specialist + cross-cutting concerns
- Model: Synthesis-tier (more capable, bigger context)
- Token budget: ~2K input, ~1K output

**Phase 3 — Cross-Check:**
- Input: All findings from all 6 agents + original brief + cross-cutting concerns
- Tasks:
  1. **Deduplicate** — multiple specialists flag same issue → keep the best explanation
  2. **Cross-reference** — "Security found auth bypass in session.ts, Bug Hunter found caller doesn't check return in api.ts → these are the SAME vulnerability chain"
  3. **Gap analysis** — review cross-cutting concerns from Phase 1. Were they all addressed? If not, flag them.
  4. **Severity calibration** — consistent across all specialists
  5. **Quality filter** — remove speculative, vague, linter-catchable findings
  6. **Integrate simulation results** — if Simulation Agent found scenario failures, connect them to relevant specialist findings
  7. **Integrate blast radius** — if Blast Radius Agent found breaking callers, merge with specialist findings
- Output: Final deduplicated, cross-referenced comment array
- Replaces: Current scoring stage (scoring.go becomes optional)

### Agent 2: Bug Hunter (per-file)

Same as current specialist. Receives lead brief's `bug_hunter_focus` for each file.
Now also receives blast radius dependent file contents.

### Agent 3: Security Auditor (per-file)

Same as current specialist. Receives `security_focus` per file.

### Agent 4: Architecture Reviewer (per-file)

Same as current specialist. Receives `architecture_focus` per file.

### Agent 5: Regression Reviewer (per-file)

Same as current specialist. Receives `regression_focus` per file.

### Agent 6: Simulation Agent (PR-level)

**Role:** Runs stored scenarios against the PR diff to predict breakage.

- Input: Changed files + diff, relevant scenarios from DB (`FindRelevantScenarios`), blast radius nodes, key file contents
- Already implemented in `simulation.go` — `SimulationEngine.RunSimulations()`
- Currently wired but rarely triggers (needs scenarios to exist)
- Output: `[]SimulationResult` — scenario name, passes/fails, confidence, root cause, impact, suggestion
- The Lead Cross-Check integrates these with specialist findings

**What changes:** Currently runs inside `synthesize()` after all reviews. Move to parallel execution alongside specialists.

### Agent 7: Blast Radius Agent (PR-level)

**Role:** Analyzes dependency impact beyond the diff.

- Input: Changed file paths, code graph (nodes + edges from DB), dependent file contents (fetched from GitHub)
- Uses existing `GetBlastRadius()` recursive CTE query
- LLM call traces: "Given these changes and these dependent callers, what breaks?"
- Output: Impact analysis — which callers break, what return type assumptions are violated, what error paths are missed

**System prompt:**
```
You are a dependency impact analyzer. Given changed code and the source code of
dependent files (callers, consumers), identify what breaks.

For each dependent:
1. What does it assume about the changed code? (return type, error behavior, side effects)
2. Do the changes violate those assumptions?
3. What's the concrete failure mode? (crash, silent wrong result, data corruption)

Only report concrete breaking changes with evidence from both the changed code AND
the dependent code. Don't speculate about dependents you can't see.

Output JSON array:
[{"dependent_file": "...", "dependent_symbol": "...", "assumption_violated": "...",
  "failure_mode": "...", "severity": "critical|warning"}]
```

**Token budget:** ~3K input (changed files + up to 3 dependent files × 200 lines), ~500 output

## Data Structures

### LeadBrief

```go
type LeadBrief struct {
    FileBriefs   map[string]FileBrief `json:"file_briefs"`
    CrossCutting []string             `json:"cross_cutting"`
}

type FileBrief struct {
    Summary           string `json:"summary"`
    BugHunterFocus    string `json:"bug_hunter_focus"`
    SecurityFocus     string `json:"security_focus"`
    ArchitectureFocus string `json:"architecture_focus"`
    RegressionFocus   string `json:"regression_focus"`
}
```

### BlastRadiusAnalysis

```go
type BlastRadiusImpact struct {
    DependentFile       string `json:"dependent_file"`
    DependentSymbol     string `json:"dependent_symbol"`
    AssumptionViolated  string `json:"assumption_violated"`
    FailureMode         string `json:"failure_mode"`
    Severity            string `json:"severity"`
}
```

### AgentResult (unified)

```go
type AgentResult struct {
    AgentName       string           // "bug_hunter", "security", "simulation", "blast_radius", etc.
    FileReviews     []FileReview     // per-file findings (specialists)
    SimResults      []SimulationResult  // scenario results (simulation agent)
    BlastImpacts    []BlastRadiusImpact // impact analysis (blast radius agent)
}
```

## Pipeline State Machine Changes

```
Current states:
  triaging → reviewing → enriching → scoring → pass2 → synthesizing → posting

New states:
  triaging → briefing → executing → broadcasting → cross_checking → pass2 → synthesizing → posting
             (Lead P1)  (6 agents   (Lead P2b:     (Lead P3:
                         parallel)   find signals,   dedup, merge,
                                     targeted 2nd    quality filter)
                                     pass if needed)
```

- `briefing` — Lead Agent Phase 1 (new)
- `executing` — all 6 agents run in parallel (replaces `reviewing`)
- `broadcasting` — Lead identifies cross-agent signals, triggers targeted second passes (new)
- `cross_checking` — Lead Agent Phase 3 (replaces `scoring` + `enriching`)

## Execution Flow (Go implementation)

```go
func (o *Orchestrator) executeAgentTeam(ctx context.Context, run *PipelineRun) error {
    // Phase 1: Lead Brief
    brief, err := o.leadBrief(ctx, run)
    run.LeadBrief = brief

    // Phase 2a: Parallel Execution (all 6 agents independent)
    g, gCtx := errgroup.WithContext(ctx)
    var mu sync.Mutex
    var allResults []AgentResult

    // Specialist agents (per-file, parallel)
    for _, specialist := range AllSpecialists() {
        s := specialist
        for _, file := range deepFiles {
            f := file
            g.Go(func() error {
                review := reviewFile(gCtx, run, f, s, brief)
                mu.Lock()
                allResults = append(allResults, AgentResult{AgentName: string(s), FileReviews: []FileReview{review}})
                mu.Unlock()
                return nil
            })
        }
    }

    // Simulation Agent (PR-level)
    g.Go(func() error {
        simResults := o.simEngine.RunSimulations(gCtx, simRequest)
        mu.Lock()
        allResults = append(allResults, AgentResult{AgentName: "simulation", SimResults: simResults})
        mu.Unlock()
        return nil
    })

    // Blast Radius Agent (PR-level)
    g.Go(func() error {
        impacts := o.analyzeBlastRadius(gCtx, run)
        mu.Lock()
        allResults = append(allResults, AgentResult{AgentName: "blast_radius", BlastImpacts: impacts})
        mu.Unlock()
        return nil
    })

    g.Wait()

    // Phase 2b: Lead Broadcast — identify cross-agent signals
    signals := o.leadBroadcast(ctx, run, allResults, brief)

    // Phase 2c: Targeted Second Pass (only if signals found)
    if len(signals) > 0 {
        g2, g2Ctx := errgroup.WithContext(ctx)
        for _, signal := range signals {
            sig := signal
            g2.Go(func() error {
                // Fetch files_to_check content
                fileContents := fetchSignalFiles(g2Ctx, o.ghClient, run, sig.FilesToCheck)
                secondPassFindings := o.agentSecondPass(g2Ctx, run, sig, fileContents)
                mu.Lock()
                allResults = append(allResults, AgentResult{
                    AgentName: sig.ToAgent + "_second_pass",
                    FileReviews: secondPassFindings,
                })
                mu.Unlock()
                return nil
            })
        }
        g2.Wait()
    }

    // Phase 3: Lead Cross-Check (dedup, merge, quality filter)
    run.FileReviews = o.leadCrossCheck(ctx, run, allResults, brief)
    return nil
}
```

### Signal Type

```go
type CrossAgentSignal struct {
    FromAgent    string   `json:"from_agent"`
    ToAgent      string   `json:"to_agent"`
    Signal       string   `json:"signal"`
    Question     string   `json:"question"`
    FilesToCheck []string `json:"files_to_check"`
}
```
```

## LLM Call Summary

| Phase | Agent | Calls | Model | Tokens (est.) |
|-------|-------|-------|-------|---------------|
| Briefing | Lead | 1 | synthesis | 3K in, 1K out |
| 2a Execution | Bug Hunter | N files | review | ~1K per file |
| 2a Execution | Security | N files | review | ~1K per file |
| 2a Execution | Architecture | N files | review | ~1K per file |
| 2a Execution | Regression | N files | review | ~1K per file |
| 2a Execution | Simulation | 1 per scenario (max 5) | synthesis | 2K per scenario |
| 2a Execution | Blast Radius | 1 | synthesis | 3K in, 500 out |
| 2b Broadcast | Lead | 1 | synthesis | 4K in, 500 out |
| 2c Second Pass | Targeted agents | ~1.5 avg | review | ~2K per pass |
| 3 Cross-Check | Lead | 1 | synthesis | 5K in, 2K out |
| **Total (6-file PR)** | | **~30 calls** | | **~50K tokens** |

vs Current deep review: ~27 calls, ~35K tokens. Delta: +3 calls, +15K tokens (~$0.05)

## What This Replaces

| Current | New | Notes |
|---------|-----|-------|
| `scoring.go` ScoreComments | Lead Cross-Check | LLM-based dedup + quality + cross-reference in one pass |
| `simulation.go` RunSimulations (in synthesize) | Simulation Agent (parallel) | Moves from post-review to parallel with specialists |
| Blast radius as context injection | Blast Radius Agent | From passive context to active analysis |
| `enrichPRDescription` | Stays as-is (post-review goroutine) | Not part of the review team |
| `extractArchitectureGraph` | Stays as-is (post-review) | Not part of the review team |

## Migration Path

### Phase A: Quick wins (ship now)
- Remove worker cap 3 → 10
- Blast radius file fetching (already coded)
- Tracing prompts (already coded)
- Scope boundaries removed (already coded)

### Phase B: Lead Agent (build later)
1. Add `LeadBrief`, `CrossAgentSignal`, `BlastRadiusImpact`, `AgentResult` types to `types.go`
2. Add `leadBrief()` — Phase 1, synthesis model
3. Pass briefs to specialist prompts in `buildFileReviewPrompt`
4. Add `analyzeBlastRadius()` — Blast Radius Agent LLM call
5. Move `RunSimulations` to parallel execution alongside specialists
6. Add `leadBroadcast()` — Phase 2b, identify cross-agent signals
7. Add `agentSecondPass()` — Phase 2c, targeted re-examination
8. Add `leadCrossCheck()` — Phase 3, replaces scoring
9. Wire up `errgroup`-based parallel execution in `executeAgentTeam()`
10. Update state machine (briefing → executing → broadcasting → cross_checking)
11. Make scoring optional (fallback if lead agent fails)

### Phase C: Optimization (later)
- Lead Brief could be skipped for small PRs (<3 files)
- Blast Radius Agent skipped if no code graph data exists
- Simulation Agent skipped if no scenarios exist
- Adaptive worker count based on PR size

## Files to Modify (Phase B)

1. `internal/pipeline/orchestrator.go` — `leadBrief()`, `leadCrossCheck()`, `analyzeBlastRadius()`, state machine
2. `internal/pipeline/review.go` — pass `LeadBrief` to specialists, remove worker cap
3. `internal/pipeline/types.go` — `LeadBrief`, `BlastRadiusImpact`, `AgentResult` structs
4. `internal/pipeline/scoring.go` — make optional (guard with `run.LeadAgentEnabled`)
5. `internal/pipeline/simulation.go` — extract `RunSimulations` for parallel use
6. `internal/pipeline/context.go` — already modified (blast radius with file contents)

## Risks

- Lead Brief adds 2-5s latency before specialists start
- Cross-Check with all findings could be 5K+ tokens (context limit risk on smaller models)
- If Lead model is weak, briefs unhelpful → specialists ignore them
- Removing scoring removes the only deterministic quality gate → keep as fallback
- Blast Radius Agent depends on code graph data existing (empty for new repos)
- More tokens per review (~$0.03 more per review at Sonnet pricing)

## Success Metrics

- Benchmark: re-run PRs #20-#37, compare recall/precision vs current
- Target: catch 3+ of the 8 currently-missed bugs (globalRegistry, double division, healthCheck 401, etc.)
- Dedup rate: <5% duplicate comments (vs current ~18 in deep review)
- Latency: not more than 20% slower despite +2 LLM calls (parallelism compensates)
