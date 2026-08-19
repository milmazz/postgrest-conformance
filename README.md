# postgrest-conformance

A language-agnostic conformance suite for PostgreSQL-backed REST API servers, derived from PostgREST v16.0. Modeled after the [JSON Schema Test Suite](https://json-schema.org/draft/2020-12/json-schema-test-suite.html), each case describes a HTTP request, expected response, and optional assertions about response body structure and HTTP headers.

**Derived from:** [milmazz/bier@6024c62](https://github.com/milmazz/bier/commit/6024c62)

## Repository Layout

| Path | Contents |
|------|----------|
| `cases/` | 762 YAML test cases, one per file, named by case ID |
| `spec/` | 16 area specification YAMLs (auth, filters, mutations, etc.) + `url_grammar.md` reference |
| `fixtures/` | Database initialization SQL (PostgreSQL 14+) |
| `PIN` | Upstream PostgREST version and commit hash |
| `case.schema.json` | JSON Schema for case YAML validation |

## Quick Start for Implementers

1. **Initialize the test database:** Run `fixtures/01_init.sql` through `fixtures/07_teardown.sql` with `psql -v ON_ERROR_STOP=1` under `PGTZ=UTC` into a `LC_COLLATE 'C'` database. See `fixtures/README.md` for details.

2. **Configure your server:** Set up your PostgreSQL-backed API server to accept requests on `localhost:3000`. Refer to `HARNESS.md` for connection parameters, auth setup, and role configuration.

3. **Run test cases:** For each YAML file in `cases/`, execute the HTTP request, collect the response, and assert per the case's expected response structure using the schema in `case.schema.json`.

## Versioning

Versions follow `v<postgrest-major>.<postgrest-minor>.<postgrest-patch>-suite.<suite-revision>`. The `PIN` file records the exact PostgREST upstream version and commit hash that defined this suite.

## Divergences

Conformance suites record what the reference implementation (PostgREST v16.0) does. Implementers may maintain their own skip list for cases that diverge from PostgREST's behavior by design. This suite itself does not track divergences — only the reference behavior.

## License

MIT License. See `LICENSE` for details.

Conformance cases are derived from the test suite of [PostgREST](https://github.com/PostgREST/postgrest), © PostgREST contributors, MIT License.
