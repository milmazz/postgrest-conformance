---
name: conformance-resync
description: Use when checking whether the conformance suite has drifted from PostgREST upstream - a new release appeared, the pin looks stale, or a coverage/gap report is wanted. Detects release drift, re-pins the tree, re-runs the oracle to find behavioral divergence, folds in it-block census and HPC coverage gaps, and emits a ranked gap report.
---

# Conformance re-sync

Answers one question: **what does this suite no longer say correctly about
PostgREST?** Drift arrives through three independent channels, and a run that
checks only one will report "clean" while the other two rot.

| Channel | Detects | Cost |
|---|---|---|
| **Release drift** | Upstream shipped a version the tree isn't pinned to | seconds |
| **Behavioral drift** | A case's `expect:` no longer matches what PostgREST returns | ~2 min |
| **Coverage gaps** | Behavior upstream has that the tree never transcribed | minutes (census) / hours (HPC) |

Release drift is the trigger; behavioral drift is the *regression* signal;
coverage gaps are the *enhancement* backlog. The gap report merges all three.

## Prerequisites

- **Docker running** — the fixture Postgres. Check with `docker info`.
- **Go ≥ 1.23** on PATH. The default `go` on this machine is 1.17 (x86_64);
  use `/opt/homebrew/opt/go/bin/go` (1.27 arm64) or put it on PATH first.
- `psql` on PATH, and `gh` authenticated.

Run everything from the repo root unless a step says otherwise. The Bash tool
resets cwd between calls — **use absolute paths or re-`cd` in every call.**

## Step 1 — Detect release drift

```sh
grep '^postgrest:' PIN                                              # pinned tag
gh api repos/PostgREST/postgrest/releases/latest --jq .tag_name     # upstream tag
```

If they match, there is no release drift. **Do not stop** — skip to Step 4;
coverage gaps accumulate without any upstream release. If they differ, collect
the delta for the gap report:

```sh
gh api 'repos/PostgREST/postgrest/releases?per_page=30' \
  --jq '.[] | "\(.tag_name)\t\(.published_at)\n\(.body)\n"'
```

Read the changelog entries between pinned and latest, and classify each into a
conformance area (the 17 in `INDEX.md` → *Area ↔ id band ↔ fixture fragment*).
A changelog line that is docs-only, packaging-only, or touches a module the
suite scopes out (see `COVERAGE.md` → *Scope*) produces no case — say so
explicitly in the report rather than dropping it silently.

## Step 2 — Re-pin the tree

Re-pinning is **not** a one-line edit. `oracle validate` enforces that every
`raw.githubusercontent.com/PostgREST/postgrest/<ref>/` URL in `spec/` and
`cases/` carries the exact tag in `PIN` — ~2165 of them. All of it moves
together or `validate` fails.

1. **`PIN`** — update both lines. The commit is the tag's actual sha:
   ```sh
   gh api repos/PostgREST/postgrest/git/ref/tags/<newtag> --jq .object.sha
   ```
   A tag object (not a commit) needs one more deref:
   `gh api repos/PostgREST/postgrest/git/tags/<sha> --jq .object.sha`.

2. **`tools/oracle/bin.sha256`** — all four platform rows. Regenerate rather
   than hand-edit:
   ```sh
   for p in linux-static-aarch64 linux-static-x86-64 macos-aarch64 macos-x86-64; do
     f=postgrest-<newtag>-$p.tar.xz
     curl -fsSL -o "/tmp/$f" "https://github.com/PostgREST/postgrest/releases/download/<newtag>/$f"
     shasum -a 256 "/tmp/$f" | sed "s|/tmp/||"
   done
   ```

3. **Every cited URL.** Rewrite the ref across both trees, then verify no
   stragglers:
   ```sh
   grep -rl 'PostgREST/postgrest/<oldtag>/' spec/ cases/ \
     | xargs sed -i '' 's|PostgREST/postgrest/<oldtag>/|PostgREST/postgrest/<newtag>/|g'
   grep -rc 'PostgREST/postgrest/<oldtag>/' spec/ cases/ | grep -v ':0$'   # must be empty
   ```

4. **⚠ Verify the line anchors.** This is the step that makes re-pinning real
   work. Anchors are `#L255`-style line references into upstream source, and
   **line numbers shift between tags even when the cited behavior does not.**
   A mechanical ref rewrite produces URLs that resolve but point at the wrong
   line. Do not skip this and do not assume a green `validate` covers it —
   `validate` checks the *ref*, never the *line*.

   For a patch-level bump touching few files, spot-check every anchor in files
   the changelog touched. For anything wider, run the `bier-spec-audit` skill
   with `args.pinned=<newtag>` — that is exactly its job.

## Step 3 — Behavioral drift (the oracle)

Runs all cases against the newly pinned real binary. Every failure here is, by
construction, a suite defect — a wrong expectation, a fixture gap, or a
harness contract gap.

```sh
export PATH=/opt/homebrew/opt/go/bin:$PATH
make -C tools/oracle db-up                                 # ~2 min first time (compiles pg-safeupdate)
cd tools/oracle && go run ./cmd/oracle db-setup
cd tools/oracle && go run ./cmd/oracle fetch               # verifies against bin.sha256
cd tools/oracle && go run ./cmd/oracle run -report report.json
cd tools/oracle && go run ./cmd/oracle validate            # tree-wide invariants
```

Read `report.json` for the failures. Teardown when done:
`go run ./cmd/oracle db-teardown && make -C tools/oracle db-down`.

**Known trap:** the suite is not idempotent against an unreloaded fixture DB —
case 1305 fails on a second run (issue #22). If a case fails, rebuild the DB
(`db-teardown && db-setup`) and re-run before believing it. Distinguishing a
real drift from this artifact is mandatory before it reaches the report.

## Step 4 — Coverage gaps

Two instruments, deliberately different. Read them together: the suite can
*execute* most of the request path while *discriminating* far fewer behaviors.

**a. It-block census (cheap, every run).** How much of upstream's `test/spec`
the tree cites. Diff the upstream spec tree between the two tags to find new
and changed it-blocks:

```sh
gh api repos/PostgREST/postgrest/compare/<oldtag>...<newtag> \
  --jq '.files[] | select(.filename | startswith("test/spec")) | .filename'
```

A new it-block in a file the census already scores low is a double signal —
new behavior *and* a thin area.

**b. HPC expression coverage (expensive, on demand).** Which PostgREST
expressions the suite actually executes. Not part of the scheduled run: it
needs an instrumented build of PostgREST from source. Procedure and its
hazards: [`references/hpc-measurement.md`](references/hpc-measurement.md).

When HPC has not been re-run at the new pin, **say so in the report** and mark
the HPC-derived rows with the pin they were measured at. Stale HPC numbers
presented as current are worse than no numbers.

## Step 5 — The gap report

Template and worked example:
[`references/gap-report-template.md`](references/gap-report-template.md).

Rank by **cases-per-unit-of-evidence**, not raw percentage. The strongest
signals, in order:

1. A case that **now fails** against the new binary (regression — always top).
2. A whole module at or near **0%** coverage with a directly caseable surface.
3. Unused **case-alternatives** and named local functions in a big module —
   behavior the suite runs past without discriminating.
4. A low **it-block citation ratio** in a spec file whose area is otherwise
   well covered.

Ignore raw declaration percentage as a ranking key: `hpc` counts derived
`Show`/`ToJSON` instances and record accessors, so `*.Types` modules read
artificially low. That is noise, not behavior.

Every gap row must carry the **area** and a **proposed id band**, because the
per-area dispatch in the loop prompt splits on exactly those two fields.

## Hard rules

- **Never edit a case to make the oracle pass.** A failure is evidence about
  the suite; decide whether the *expectation* was wrong, and record why.
- **`fixtures/06_area_schemas.sql` is generated** — `tools/regen_area_schemas.exs`
  produces it, and CI diffs it byte-for-byte. Never hand-edit.
- **Id bands must not collide.** `INDEX.md` documents five overflow bands and
  the rules for opening a sixth. Derive the band from the `feature:` prefix on
  disk, never from a stale narrative sentence in `INDEX.md`.
- **A green `validate` is not a green tree.** It checks refs, schema, and band
  bookkeeping — not whether a citation supports its claim.
