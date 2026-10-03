# Changelog

## Unreleased — re-pin to PostgREST v16.4

**The pin moves v16.0 → v16.4, and 812 -> 824 cases.** Spans upstream v16.1,
v16.2, v16.3 and v16.4. Closes #26, #33, #34 and #35 (the four scheduled
drift reports for this span).

**Behavioral drift: one case.** Before any case changed, the suite ran
**811/812** against the v16.4 binary on a freshly rebuilt fixture database.
The single failure was case 1711, as #26 predicted. v16.2
([PostgREST#5171](https://github.com/PostgREST/postgrest/pull/5171)) accepts the
pre-v16 leading-dot JSPath again, as *deprecated* syntax, so `.role.other` now
loads. 1711 now pins `@@.role.other`, which upstream's `invalidroleclaimkeys`
moved to and which neither grammar accepts. Downstream (bier#99): the message
1711 asserts is unchanged, but its input is not.

**New cases, one per caseable changelog line:**

- **config, deprecated `jwt-role-claim-key` syntax — 7 cases (11700-11706).**
  Covers acceptance and `--dump-config` normalization (`.roles.user_role` dumps
  as `."roles"."user_role"`), the startup WARNING, and each of the five
  restored string-comparison operators (`==`, `!=`, `^==`, `==^`, `*==`),
  mirroring upstream's one-operator-per-file `jspath-str-op-dump{1..5}.config`
  goldens. The WARNING is emitted while the config loads, so `--dump-config`
  prints it to stderr and no running server is needed.
- **config, `jwt-cache-max-entries` through the in-database source — 1 case
  (11707).** v16.4 ([#5269](https://github.com/PostgREST/postgrest/pull/5269))
  fixes `dbSettingsNames`, which listed the nonexistent
  `jwt_cache_max_lifetime`. `spec/config.yaml` had recorded that bug as a
  QUIRK; the entry now records the fix.
- **auth, deprecated syntax evaluated against real JWTs — 3 cases
  (11819-11821).** Covers a dotted path, the `^==` filter (which RFC 9535 does
  not have), and the object-vs-string 401.
- **openapi, root document of a mixed-case schema — 1 case (1690).** v16.4
  ([#5158](https://github.com/PostgREST/postgrest/pull/5158)) quotes the
  schema name before `::regnamespace`. The fixture is upstream's `"SCHEMA_v3"`:
  the schema, its comment and a USAGE grant, folded through the new
  `fixtures/provenance/openapi.delta.sql`. The case is routed like 1654: an
  id-selected `db-schemas` override in the oracle router and in HARNESS.md
  §2.3/§2.5. **bier's harness must add the same `variant_extra_opts(1690)`
  clause when it bumps the pin.**

Each new case was also run against the v16.0, v16.2 and v16.3 binaries. Every
one fails before the release that introduced its behavior and passes from that
release on, so each case is anchored to its changelog line, not merely
compatible with it.

**Not caseable, recorded only:** v16.1's JWT clock fix
([#5159](https://github.com/PostgREST/postgrest/pull/5159)) and v16.3's
sporadic PGRST303 and log-timestamp fixes (#5196, #5213). All three need a
controllable clock. All 69 pre-existing auth cases pass unchanged at v16.4. No
case either for the docs, packaging, CI and `test/io` reorganization changes.

**Bands.** config's primary band was full, so config opens the tree's sixth
5-digit band, **[11700..11799]**, declared closed in `spec/config.yaml` when
opened. auth grows contiguously to 11800-11821. openapi takes 1690 and leaves
1689 unused, because `spec/openapi.yaml` still holds the withdrawn 1689 for
restoration.

**Re-anchoring.** All 2224 raw citations now carry the `v16.4` ref, but the ref
was the easy part. Between the tags upstream reformatted every Haskell file
(stylish-haskell → fourmolu) and split `test/io/test_io.py` and `test_auth.py`
into `test_log.py`, `test_config.py`, `test_connection.py` and
`test_reloading.py`. So nearly every `#L` anchor had moved even where behavior
had not, and `oracle validate` checks the ref, not the line. Every URL anchor,
bare prose reference (`Error.hs#L224`, `RangeSpec.hs:163`, `… at L303`) and
`source_line:` field was re-mapped mechanically:

- **Exact:** an LCS alignment of the two versions of each file, ignoring
  whitespace and commas.
- **Split lines:** a line fourmolu split across several maps to the first of
  them.
- **Moved files:** split-file refs are aligned function by function.

Every match that was not exact was reviewed by hand. Prose refs whose file was
ambiguous (226 continuations such as "… at :1266") were resolved by hand
against the v16.0 text. References that cite v14.12, and the suite's own
`fixtures.sql`, were left alone.

Verified: `oracle validate` clean over 824, `go test ./...` green,
`fixtures/06_area_schemas.sql` regenerates byte-identical, and a full run
against the pinned v16.4 binary on a freshly rebuilt fixture database —
**TOTAL 824/824**.

## v16.0.0-suite.5

**805 -> 812 cases.** All three spec passes are the dispatched ranks of this
cycle's upstream re-sync (#26) — the first end-to-end run of the new
`conformance-resync` skill, which measured the tree against PostgREST v16.2 and
ranked the coverage holes it found. Every gap it ranked 1-3 is closed here; the
pin itself does **not** move (see the drift note below).

- **config: `db-aggregates-enabled` via the in-database source — 1 case
  (1749).** Three cases already drive `ALTER ROLE … SET pgrst.*` (1724, 1725,
  1744), but none used `db-aggregates-enabled` — so the tree recorded that the
  key exists and is PGRST123-gated (11120/11121) without ever recording that it
  is *reloadable*, though it is the first entry of upstream's `dbSettingsNames`
  (`Config/Database.hs#L47`). 1749 is a `kind: cli` / `--dump-config` case in
  the shape of 1724 with upstream's own three values, which make the assertion
  undeniable: the config file says `true`, the role setting says `'false'`, the
  golden dump says `false`. Neither the file nor the parser default can satisfy
  it by accident. The model gains `precedence.reloadable_keys_cased`. Closes
  #24.
- **select: resolved-empty spread projections — 2 cases (11139-11140).** 11138
  pinned only the *literally* empty spread `...processes()`, decidable at the
  parser. Nothing covered a spread whose projection is non-empty as written but
  contributes no columns once resolved. 11140
  (`...processes(...process_costs())`) is a 200 with four bare names; 11139
  (`...processes(process_costs())`) is a **400** — a nested empty embed is not
  the same thing as a nested empty spread. Issue #25 predicted a 200 for the
  11139 spelling; the pinned binary said otherwise, and the case pins what it
  returned. Closes #25.
- **errors: PGRST200's fuzzy PARENT hint — 4 cases (1527-1530).** An HPC run
  named `noRelBetweenHint.fuzzySetOfParents` and `suggestParent` as never
  executed. This is a second, independent fuzzy hinter, distinct from the
  PGRST205 table hint of 1520/1521 — different candidate set, different
  threshold. Before this the tree asserted only its silent leg (1124). 1529 is
  the load-bearing one: the hint names `managers` (0.4) even though `menagerie`
  (0.6) is closer, which is direct evidence that the candidate set is
  `HM.keys allRels` (`Error.hs#L327`), not every table.

**The re-sync found one behavioral drift, and the tree stays at v16.0.** The
candidate run against the v16.2 binary was 804/805: case 1711
(`config/validation/invalid-role-claim-key`) no longer exits nonzero, because
v16.2 ([PostgREST#5171](https://github.com/PostgREST/postgrest/pull/5171)) makes
`jwt-role-claim-key` backwards compatible with the pre-v16 JSPath grammar that
v16.0's move to RFC 9535 had made invalid. 1711 is **correct at this pin** and
its own `notes:` already say why; it must be rewritten as part of any future
re-pin, not now. Downstream consumers that treat 1711's message as immutable
(bier#99 cites it that way) should read that constraint as version-dependent.
Two further v16.2 behaviors are caseable but deliberately unwritten at a v16.0
pin: legacy-JSPath normalization in `--dump-config`, and the deprecation warning
`Config/DeprecatedJSPath.hs` emits. The v16.1 JWT clock fix produces no
observable drift — all 69 auth cases pass against v16.2 unchanged.

Tooling, all post-suite.4:

- **`oracle validate` now guards the CLI-set claims restated in prose** across
  the documents (`internal/validate/clishape.go`). That was suite.4's second
  recorded limitation and the drift both of that cycle's reviews had to catch by
  hand. Closes #21.
- **The `conformance-resync` skill and a scheduled drift workflow** encode the
  upstream check that had been run by hand. The workflow runs only the cheap
  channels: it tests a candidate binary via `oracle run -bin` *without*
  re-pinning, so it answers "would a re-pin break anything?" before anyone edits
  `PIN`. HPC measurement stays a documented local procedure — it needs a
  from-source `stack build --enable-coverage` (45-90+ min, multi-GB tree).
  Running the workflow's logic by hand against the live release feed, and then
  auditing the paths that hand-run had not exercised, found six defects before
  it was trusted, one of which (`inputs.open_issue != false` evaluating false on
  every `schedule` event) would have suppressed the tracking issue that is the
  whole point of the schedule.
- **`scripts/`** gains four workflow helpers — `check` (every database-free
  gate), `fresh-db` (rebuild the fixture DB, then run the suite), `sync-main`,
  and `prune-merged-branches-and-worktrees` — plus the repo's first `CLAUDE.md`.
- **CI fails the tree job on unresolved conflict markers.** Nothing in CI read
  the prose files, so a rebase that left `<<<<<<< HEAD` in `COVERAGE.md` passed
  all eleven checks green on a real PR this cycle.

Known limitations carried into this release:

- The suite is still **not idempotent against an unreloaded fixture database**
  (#22, unchanged from suite.4). Postgres sequences are non-transactional, so
  `db-tx-end=rollback` discards rows but not sequence advances; case 1305 fails
  on a second run without `oracle db-setup`. CI is unaffected.
- **HPC coverage was not re-measured** at v16.2; the figures driving the gap
  ranking are at `v16.0`. Ranks 4-5 of the gap report — the remaining spread
  shapes, and embed disambiguation / plan shapes — are recorded, not dispatched.
  #26 stays open for them.

Verified at the tag: `oracle validate` clean over 812, `go test ./...` green,
and a full run against the pinned v16.0 binary on a freshly loaded fixture
database — **TOTAL 812/812**.

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
