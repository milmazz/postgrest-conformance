# Changelog

## v16.0.0-suite.4

**762 -> 805 cases.** Both additions were picked by the HPC/it-block coverage
measurement taken after suite.3, which ranked the tree's implementation-coverage
holes by size; this release closes the top two.

- **select: spread embedding and aggregates on spreads — 39 cases
  (11100-11138).** The measurement found `SpreadQueriesSpec` at 2/43 it-blocks
  cited, `AggregateFunctionsSpec` at 8/41, and `PostgREST.Plan` carrying 366
  unexercised expressions. Both of the file's spread to-many contexts are now
  transcribed: the one-to-many half (11100-11121 — JSON-array flatten, parallel
  column arrays, empty-array vs single-null-element, four nested-spread
  cardinalities, embed filters, `!inner`, the array-ordering rules) and the
  many-to-many half through a junction relation (11122-11138), plus aggregates
  in to-one spreads, the **PGRST127** to-many-spread rejection (11119, the
  tree's first case for that code) and the **PGRST123** disallowed-by-default
  pair. Case 11125 pins a parser tolerance verbatim, stray trailing `)`
  included. `select` opens the tree's fifth overflow band and — following
  `filters` rather than the three areas that picked ad hoc — **declares** it
  closed as `[11100..11199]` in `spec/select.yaml`.
- **config: the `--ready` CLI health-check flag — 4 cases (1745-1748).**
  `PostgREST.Client` measured at 0% of 134 expressions. These cover four of its
  six exit paths — the whole surface reachable without a running instance: no
  admin-server-port, connection refused, the invalid URL built from port `-1`,
  and the special-hostname rejection. The model gains `cli.ready_flag` with a
  machine-readable `messages:` map (message, case, source and
  `message_source` per flavor). The two instance-dependent paths (success and
  not-ready) are recorded as `needed_assertion:` gaps naming the harness
  mechanism that would unlock them, and the success-path gap also names and
  rejects the cheap alternative, so it is harder to reopen wrongly.

`tools/validate.py` is **removed**; `oracle validate` (added in suite.3, run in
parity with the Python checker for one full transition cycle) is now the sole
tree validator. Python and pyyaml remain in CI's `tree` job for
`internal/cases`'s YAML-dialect cross-check. Closes #16.

Fixtures grew through the `select.delta.sql` channel opened this cycle: the
nine-table factories family, then the `operators` / `process_operator`
many-to-many pair, both folded into `02_base.sql` with
`06_area_schemas.sql` regenerated.

HARNESS.md §4 now documents the CLI split the `--ready` cases introduce —
`--dump-config` is startup validation, `--ready` is a health-check client that
performs real outbound TCP — including case 1746's environmental precondition:
the connection must be actively *refused*, not filtered, or the case times out
rather than mismatching.

Both spec passes were followed by an independent code review whose findings were
folded before merge. Neither review found a defect in a case, a fixture or a
test; both found bookkeeping drift, and between them they corrected a falsified
`spec/ordering.yaml` gap, two stale `PGRST127` claims (one a literal
`grep`-based assertion that had become false), the corpus-count census in four
documents, and provenance ranges that ran across fold boundaries.

Known limitations, both filed against this release:

- The suite is **not idempotent against an unreloaded fixture database**
  (#22). Postgres sequences are non-transactional, so the runner's
  `db-tx-end=rollback` discards rows but not sequence advances; case 1305
  asserts a `Location` derived from a serial PK and fails on a second run
  without `oracle db-setup`. CI is unaffected — it loads fresh every time.
- `oracle validate` guards the two machine-checked area tables but **not the
  CLI-set claims restated in prose** across five documents (#21), which is the
  drift both reviews this cycle had to catch by hand.

Verified at the tag: `oracle validate` clean over 805, `go test ./...` green,
and a full run against the pinned v16.0 binary on a freshly loaded fixture
database — **TOTAL 805/805**.

## v16.0.0-suite.3

First release machine-verified against real PostgREST: an internal Go runner
(`tools/oracle/`) now executes all 762 cases against the pinned v16.0 binary
in CI on every push and PR, and the tree check is folded into the same
codebase as `oracle validate` (parity with `tools/validate.py` is enforced by
test; both run side by side during a one-release transition).

Corpus corrections surfaced by the oracle:

- Cases 1360/1368/1373: PGRST205 "Could not find the table" messages are
  qualified by the request's active schema (`'mutations.fake'`, not
  `'test.fake'` — upstream's specs show `test.` only because their sole
  exposed schema is named `test`).
- Case 1573: pins `server-trace-header: ""` as a config precondition, since a
  configured trace header makes PostgREST echo a literal blank header on
  requests without one, tripping `headers_no_blank`. Upstream's `baseCfg`
  configures none.
- `fixtures/06_area_schemas.sql`: area-mirror views inline the test-view
  definitions instead of pointing across schemas, restoring the `Location`
  header on view inserts (case 1824).

HARNESS.md gained portability amendments surfaced by writing the runner —
per-area single-schema instance layout (avoiding false PGRST201 embed
ambiguity), config-variant instance routing, and inlined view mirrors — and
INDEX.md was reworked to match.

`fixtures/06_area_schemas.sql` no longer sets `transaction_timeout` (a
PostgreSQL 17+-only GUC that pg_dump >= 17 emits in its preamble); the fixture
chain now loads on PostgreSQL 15+ instead of failing under `ON_ERROR_STOP` on
PG 15/16. The `fixtures` CI job is now matrixed over PostGIS images for PG
15/16/17 to catch this class of regression.

## v16.0.0-suite.1

Initial release: 762 conformance cases derived from PostgREST v16.0 test suite.
