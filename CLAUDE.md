# CLAUDE.md

Working notes for this repository: the things that are easy to get wrong and
cost a round trip to rediscover.

## Layout gotcha: the Go module is not at the repo root

`go.mod` lives at `tools/oracle/`, so every `go` command must run from there.
From the repo root you get `cannot find main module`.

```sh
cd tools/oracle && go test ./...          # not `go test ./tools/...`
cd tools/oracle && go run ./cmd/oracle validate
```

Because each tool call starts in the repo root, the shell-friendly form is
`cd /abs/path/to/repo/tools/oracle && go ...` — an absolute `cd`, not a
relative one carried over from a previous call.

## Common commands

| What | Command |
|---|---|
| **All database-free gates** | `scripts/check` |
| **Rebuild the fixture DB + run the suite** | `scripts/fresh-db` |
| Tree check alone | `cd tools/oracle && go run ./cmd/oracle validate` |
| Go tests alone | `cd tools/oracle && go test ./...` |
| Regenerate `fixtures/06_area_schemas.sql` | `elixir tools/regen_area_schemas.exs` |
| Prove fixtures match bier's loader | `BIER=/path/to/bier tools/verify_equivalence.sh` |

Prefer the two scripts: they are the sequences that actually get run, and
they remove the `cd tools/oracle` footgun below.

The oracle reads `PGHOST` (default `localhost`) and `PGPORT` (default `6432`)
— see `tools/oracle/internal/db/db.go`. The defaults already match the
`oracle-pg` container, so you only pass them to point somewhere else.

`oracle validate` checks the area tables in **both** `INDEX.md` and
`HARNESS.md` §7. They spell their rows in different column orders and both
carry per-area counts; they diverged silently once, which is why both are
parsed now.

## `scripts/`

Both scripts are `#!/usr/bin/env bash` with `set -euo pipefail`, matching
`tools/verify_equivalence.sh`. Both are idempotent — a second run reports
that there is nothing to do and changes nothing — and both use absolute
paths throughout: each resolves its own repo root from `${BASH_SOURCE[0]}`
and then drives git with `git -C "$REPO_ROOT"`, so neither depends on the
current directory. Set `REPO_ROOT` to point either at another checkout.

Note bash here is 3.2 (macOS system bash): no `mapfile`, no associative
arrays. Both scripts stay inside that dialect.

### `scripts/sync-main`

Fast-forwards the default branch to its remote and prunes remote-tracking
refs the remote has deleted.

```sh
scripts/sync-main [--remote NAME] [--quiet]
```

- Resolves the default branch from `refs/remotes/<remote>/HEAD`.
- `git fetch --prune --tags`. The `--prune` is the point: this repo merges
  PRs with `gh pr merge --squash --delete-branch`, so merged branches vanish
  upstream and their local `origin/*` refs linger until pruned.
- Fast-forwards **only**. Never rewrites history, never switches your branch.
- If the default branch is checked out somewhere, merges in place so the
  files follow; if it is checked out nowhere, moves the ref alone via a
  fast-forward-only refspec.
- Exits 1 without touching anything if the branch is ahead of the remote, or
  if its checkout is dirty.

### `scripts/prune-merged-branches-and-worktrees`

Removes worktrees and local branches whose work already landed.

```sh
scripts/prune-merged-branches-and-worktrees [-n|--dry-run] [--remote NAME]
```

**Why this is not `git branch --merged`.** A squash merge rewrites the
branch into one new commit, so the original commits are never ancestors of
`main` and `--merged` does not list them. Verified in a sandbox reproducing
this repo's workflow: of a squash-merged branch, a truly merged branch and
an unmerged one, `--merged` saw only the second. Branches are therefore
classified two ways:

- **merged** — tip is an ancestor of the default branch. Deleted with
  `git branch -d`.
- **squashed** — the branch's *tree*, replayed as a single commit on the
  merge base, is already an equivalent patch on the default branch
  (`git commit-tree` + `git cherry`). Deleted with `git branch -D`, since
  git will not see it as merged. **The tip sha is printed with a
  `git branch <name> <sha>` restore command**, because `-D` is the one
  irreversible-looking step here (the object survives until `git gc`).

Anything else is left alone and reported. It never touches: the default
branch, a **locked** worktree (the Claude harness locks the one backing a
live session), a worktree with local changes, a worktree containing your
current directory, the main worktree, or a branch checked out in a surviving
worktree.

`--dry-run` is faithful: it accounts for the branches that the worktree
removals would free, so its verdicts match the real run line for line rather
than reporting those branches as still checked out.

`.claude/worktrees/` is gitignored — the branch and its commits live in the
shared object store, so only the checkout is local and disposable.

### `scripts/check`

Every gate that needs no database: `gofmt`, `go vet`, `oracle validate`,
`go test ./...`.

```sh
scripts/check [--fail-fast] [--fresh] [--quiet]
```

- Read-only, so re-running is free.
- Runs **all** steps even when one fails, then prints a summary and exits 1 —
  knowing three of four gates are green is more useful than stopping at the
  first red. `--fail-fast` stops at the first failure instead.
- `--fresh` clears the Go test cache first, so `(cached)` results really
  re-run.
- Ordered cheapest-first (gofmt → vet → validate → test), so `--fail-fast`
  surfaces the likeliest problem soonest.
- `gofmt -l` exits 0 whether or not it finds anything, so the gate is on its
  output, not its status — a detail worth preserving if you edit it.

### `scripts/fresh-db`

Drops and rebuilds the fixture database from `fixtures/01..07`, then runs the
full suite against the pinned binary.

```sh
scripts/fresh-db [--db NAME] [--no-run]
```

- **Destructive by design**: it runs `DROP DATABASE` on
  `postgrest_conf_oracle`, which exists only to be rebuilt. The name is
  printed before any drop. Nothing else is touched.
- Reach for it when a suite failure might be leftover state rather than a
  real defect — sequence values surviving an aborted earlier run have caused
  exactly that, and a clean rebuild is what distinguishes the two. When it
  fails after a rebuild, the failures are real.
- Checks the server is reachable first (via `pg_isready` when present) and
  names the container in the error, rather than dying part-way through a
  teardown.
- `--no-run` stops after the rebuild.
- The pinned binary is fetched on demand into `tools/oracle/.cache/` and
  reused, so only the first run needs the network.

## Docs conventions worth knowing before editing

`COVERAGE.md` and `INDEX.md` deliberately mix two kinds of statement, and
they must be treated differently when the corpus grows:

- **Dated snapshots** — "re-derived at the 762-case state", per-batch refresh
  boxes appended newest-first. These are history. Do not update them; a bulk
  find-and-replace on the case count corrupts the audit trail.
- **Unqualified present-tense claims** — "Cross-reference of the 762 cases",
  "now out of 724 HTTP cases". These are drift and must be re-derived from
  disk, not adjusted by arithmetic.

The `## Review status` table in `COVERAGE.md` is the exception among tables:
it is live status, updated in place with "Re-audited \<date\>" annotations.

Fixture provenance comments cite upstream line ranges. Re-fetch the file and
check the range boundaries rather than extending an existing one — the
ranges are the traceability the suite sells, and an over-wide range silently
attributes one fold's rows to another.
