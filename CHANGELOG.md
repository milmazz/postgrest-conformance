# Changelog

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
