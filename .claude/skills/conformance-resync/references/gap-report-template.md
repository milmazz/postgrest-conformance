# Gap report template

The report is the **input to the per-area agent dispatch**, not a document for
reading. Every row must carry an `area` and a proposed id band, because the
loop prompt splits work on exactly those two fields. A row without them cannot
be dispatched and will be dropped.

Post as a tracking issue titled:
`conformance re-sync: v<old> → v<new> — N gaps across M areas`

---

## Template

```markdown
## Re-sync summary

| | |
|---|---|
| Pinned before | `v16.0` (`ac464c3…`) |
| Upstream latest | `v16.2` (`…`) published `2026-08-21` |
| Releases spanned | v16.1, v16.2 |
| Cases at run | 805 |
| **Oracle result at new pin** | **803/805 — 2 behavioral drifts** |
| `oracle validate` | clean / N findings |
| HPC coverage | measured at `v16.0` (stale — not re-run) |
| It-block census | 579/1266 cited (45.7%) |

### Channel A — behavioral drift (regressions, always highest priority)

Cases whose recorded `expect:` no longer matches the real binary.

| Case | Area | What changed | Upstream cause |
|---|---|---|---|
| 1461 | auth | … | v16.1 #5159 JWT clock fix |

> Each row was confirmed against a **freshly rebuilt fixture DB**. Issue #22
> (suite not idempotent, case 1305) was ruled out for every row.

### Channel B — new upstream behavior (no case exists)

| Upstream change | Area | Caseable? | Proposed band |
|---|---|---|---|
| `jwt-role-claim-key` legacy JSPath deprecation warning | config | yes | 1749–17xx |

Changes that produce **no** case are listed here too, with the reason —
docs-only, packaging, or a module in `COVERAGE.md` → *Scope*. Silence is
indistinguishable from an oversight.

### Channel C — coverage gaps (enhancement backlog)

| Rank | Area | Gap | Evidence | Est. cases | Proposed band |
|---|---|---|---|---|---|
| 1 | select | spread embedding, remaining shapes | `Plan` 366 missed exprs; `SpreadQueriesSpec` 2/43 | 12–18 | 11139–11199 |
| 2 | errors | fuzzy parent-relationship hints | `noRelBetweenHint.fuzzySetOfParents` unused | 4–6 | 1527–1549 |

Evidence must name the **specific unused declaration or spec file with its
ratio** — not a module percentage. A row reading "Error at 43%" is not
actionable; "`suggestParent` never exercised" is.

### Dispatch plan

| Area | Channels | Cases | Band | Depends on |
|---|---|---|---|---|
| auth | A, B | 3 | 11819–11830 | — |
| errors | C | 5 | 1527–1549 | — |

Bands verified non-colliding against `INDEX.md` on <date>.
```

---

## Rules that keep the report dispatchable

- **One row, one area.** A gap spanning two areas is two rows with a stated
  dependency, never one row with a slash. The dispatch splits on area; a
  slashed row deadlocks two agents on the same files.
- **Bands are allocated in the report, not by the agents.** Agents working in
  parallel cannot negotiate id ranges — they would collide. Verify every
  proposed band against `INDEX.md` before posting, and check whether the area's
  primary band has room before opening an overflow band (`INDEX.md` documents
  the rules and the five existing overflow bands).
- **Estimate cases honestly.** The estimate sets the agent's scope. An area
  estimated at 4 that is really 30 produces an agent that either sprawls or
  stops mid-way.
- **Mark stale HPC rows with their pin.** Evidence measured at a superseded
  version is still useful for ranking and misleading as a current claim.
- **Regressions never share a PR with enhancements.** Channel A is a fix to an
  existing case; Channels B and C add new ones. Mixing them makes the fix
  un-revertable without losing the additions.
