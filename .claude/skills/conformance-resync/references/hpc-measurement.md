# HPC implementation-coverage measurement

Measures which PostgREST **expressions** the conformance suite executes, by
running the suite against a PostgREST built with GHC's Haskell Program
Coverage instrumentation. Answers "how much of the implementation do we
touch?", which the it-block census cannot.

**This does not run in scheduled CI, by design.** It needs a from-source
`stack build --enable-coverage` of PostgREST (GHC 9.10.3), which is a 45–90+
minute build and a multi-GB tree. The scheduled workflow runs the cheap
channels; this is `workflow_dispatch` or local.

## Where the rig lives, and why that matters

Everything sits under `scratch/coverage/`, which is **gitignored**. The rig is
untracked local state:

```
scratch/coverage/
  postgrest/                 # upstream source checkout at the pinned commit
    .stack-work/dist/aarch64-linux/ghc-9.10.3/hpc/     # .mix files (the coverage index)
    covbin/postgrest                                   # the instrumented binary (~66 MB)
  postgrest-cov-wrapper      # per-process HPCTIXFILE wrapper, handed to `oracle run -bin`
  run-suite.sh               # runs the suite in the golang container
  report-hpc.sh              # sums tix, emits reports, in the haskell container
  hpcsummary/main.go         # condenses per-module output into a sortable table
  tix/                       # one .tix per PostgREST process (93 from the suite.4 run)
  FINDINGS.md                # the written-up result
```

Consequences to plan around:

- **A fresh clone has none of this.** Reconstructing it is the build, not a
  checkout.
- **The paths are host-specific.** `report-hpc.sh` hardcodes
  `aarch64-linux/ghc-9.10.3`; `hpcsummary/main.go` hardcodes an absolute path
  under `/Users/milmazz`. Both need editing on a different arch or user.
- **The binary is pin-specific.** A binary built at v16.0 measures v16.0. After
  a re-pin, prior numbers describe the *old* implementation — either rebuild or
  label every derived row with the pin it came from.

## Procedure

1. **Check out upstream at the pinned commit** into `scratch/coverage/postgrest`
   (the sha from `PIN`, not just the tag).

2. **Build instrumented**, then copy the binary to `covbin/postgrest`:
   ```sh
   stack build --enable-coverage
   ```
   Must be a **glibc/dynamic** build, not the static musl release build — the
   release artifact carries no `.mix` index and cannot be measured.

3. **Run the suite against the wrapper.** `postgrest-cov-wrapper` gives every
   PostgREST process a unique `HPCTIXFILE` and then `exec`s the real binary, so
   `SIGTERM` from the oracle's `instance.Stop` reaches PostgREST directly and
   the RTS writes its tix on clean exit. Without the `exec`, the wrapper eats
   the signal and you get zero tix files.

   ```sh
   go run ./cmd/oracle run -bin /repo/scratch/coverage/postgrest-cov-wrapper \
     -report /repo/scratch/coverage/report.json
   ```

   **The suite must still pass 762/762 (or the current count).** If the
   instrumented binary diverges from the release binary, the measurement is
   describing different software than the suite pins and the numbers are void.

4. **Sum and report** (`report-hpc.sh`, in a `haskell:9.10.3` container so
   `hpc` matches the building GHC exactly — a mismatched `hpc` will not read
   the `.mix` files):
   ```sh
   hpc sum --union --output=suite.tix tix/*.tix
   hpc report suite.tix --hpcdir=$HPCDIR --per-module
   ```

5. **Condense**: `go run ./hpcsummary` → module, expressions %, alternatives %,
   booleans %, raw missed-expression count.

## Two environment traps that cost real time

- **libpq version.** The instrumented glibc binary links a newer libpq than
  Debian bookworm ships. `run-suite.sh` installs `libpq5` +
  `postgresql-client-18` from the PGDG apt repo. Distro libpq fails at runtime.

- **Locale / stdio encoding.** The oracle's `BuildEnv` passes children only
  `PGRST_*` and `PATH`, so the GHC RTS sees no locale and falls back to ASCII
  stdio encoding. Instances whose config names the non-ASCII `SPECIAL` schema
  then **crash while logging it**. The official static musl binary defaults to
  UTF-8 and hides this; the glibc build honors locale. The wrapper therefore
  sets `LC_ALL=C.UTF-8` in the exec chain. Remove that line and the run fails
  in a way that looks like a suite bug.

## Reading the output

Use the **scoped** denominator for the headline, not the whole library. The
scope is the request-handling surface a black-box HTTP suite can reach:
`ApiRequest*`, `Plan*`, `Query*`, `Response*`, `RangeQuery`, `MediaType`,
`Error*`, `Auth`/`Auth.Jwt`/`Auth.Types`, `Cors`, `App`, `MainTx`. Excluded as
startup/infra/observability: `AppState*`, `Cache.Sieve`, `CLI`, `Client`,
`Config*`, `Logger*`, `Metrics`, `Network`, `Observation`, `SchemaCache*`,
`Admin`, `Unix`, `Debounce`, `TimeIt`, `Version`, `Auth.JwtCache`.

Mine **unused alternatives and named local functions**, not the declaration
percentage — `hpc` counts derived `Show`/`ToJSON` instances and record
accessors as declarations, which drags `*.Types` modules down for reasons that
have nothing to do with behavior.

**Boolean coverage is the sharpest signal.** At suite.4 it read 52% against 84%
expression coverage: nearly half the branch conditions were only ever observed
taking one value. The suite *runs through* those branches without ever
*discriminating* them — exactly the gap a coverage percentage hides.

## Baseline (suite.4, pinned v16.0, measured 2026-08-23)

| Measure | Value |
|---|---|
| Expression coverage, whole library (60 modules) | 84.0% (16,971/20,195) |
| Expression coverage, scoped request path | 88.8% (11,261/12,685) |
| Case-alternative coverage, scoped | 78.2% (1,070/1,369) |
| Boolean branch coverage, whole binary | 52% (156/295) |
| Upstream it-blocks cited by `cases/` | 45.7% (579/1,266) |
| … cited by `cases/` or the `spec/` model | 50.6% (640/1,266) |

Full write-up, including the ranked misses: `scratch/coverage/FINDINGS.md`.
