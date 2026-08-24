# oracle — the conformance runner

`oracle` executes every case in [`cases/`](../../cases) against a real,
pinned PostgREST binary (the version and commit recorded in
[`PIN`](../../PIN)) and reports every divergence between what PostgREST
actually returned and what the case's `expect:` block records. It is the
tool that turns `cases/` from "a pile of YAML someone hopes is right" into
a suite that has actually been run against the software it describes.

"Oracle" is used here in the software-testing sense: a *test oracle* is the
authoritative mechanism a test suite consults to decide whether an
observed result is correct. Real PostgREST is that oracle for this suite —
the name is not a reference to Oracle Database, and this tool has nothing
to do with it. See [`doc.go`](doc.go) for the same note in code form.

This is internal tooling for the suite's own maintainers, not a supported
consumer API, and it is deliberately narrow: it never modifies `cases/`,
`spec/`, or `fixtures/`. It only reads them, drives PostgREST processes
against them, and reports what it saw. Every failure it reports is, by
construction, a suite defect (a wrong expectation, a fixture gap, or a
`HARNESS.md` contract gap) — see "Failure routing" below.

## Prerequisites

- **Go** (see [`go.mod`](go.mod) for the toolchain version) to build and run
  the CLI.
- **Docker** to run the fixture PostgreSQL instance (PostgreSQL 17 +
  PostGIS + `pg_safeupdate`, per [`Makefile`](Makefile)'s `db-up` target).
- **`psql`** on `PATH` — the fixture chain (`fixtures/*.sql`) is loaded via
  `psql -v ON_ERROR_STOP=1 -q -f <file>`, invoked as a subprocess by
  `oracle db-setup`.

## Command sequence

From the repository root:

```sh
cd tools/oracle
make db-up                    # boot the fixture Postgres container (PostGIS + pg_safeupdate)
go run ./cmd/oracle db-setup  # load the fixture chain into a fresh database
make run                      # fetch the pinned PostgREST binary and run every case
```

`make run` is `go run ./cmd/oracle run -report report.json`. When you're
done:

```sh
go run ./cmd/oracle db-teardown  # drop the fixture database and its role
make db-down                     # remove the fixture container
```

If the fixture database is stale or you're unsure of its state, rebuild it
rather than debugging around it — it's cheap:

```sh
go run ./cmd/oracle db-teardown && go run ./cmd/oracle db-setup
```

Each subcommand can also be run standalone: `go run ./cmd/oracle fetch`
downloads and verifies the pinned binary (checksummed against
[`bin.sha256`](bin.sha256)) without running anything, printing its cached
path.

## `oracle validate` — the tree check

`oracle validate` (or `make validate`) is the suite tree check, run from
anywhere inside the repository, no database or PostgREST binary needed. Per case file it checks YAML
parseability, `case.schema.json` (draft 2020-12) validity, loadability
through the runner's own strict loader (`internal/cases`), id uniqueness,
the filename-prefix == id rule, and that `source:` cites
`raw.githubusercontent.com` at the exact tag pinned in `PIN`; for the tree
as a whole it checks `INDEX.md`'s "Area ↔ id band" table (per-area counts,
band membership, and the `Total: N cases` line) against what is on disk.
It prints one line per finding and exits non-zero on any violation.

`oracle validate` began as a port of `tools/validate.py`, with parity
enforced by test while both ran side by side in CI; the Python script was
removed after the v16.0.0-suite.3 transition cycle (issue #16) and this is
now the sole tree check. One guarantee from that era is still enforced:
`internal/cases`'s pyyaml cross-check keeps every published case parsing
identically under YAML 1.1 (pyyaml) and YAML 1.2 (yaml.v3), so consumers
on either dialect see the same corpus (see the `internal/validate` package
comment; the test skips without python3+pyyaml, and CI's tree job always
runs it).

## Environment variables

The Postgres connection is read once, by `internal/db.FromEnv`, and used by
both `db-setup`/`db-teardown` and `run` (which also passes the resulting
`db-uri` to every PostgREST instance it boots):

| Variable | Default | Notes |
|---|---|---|
| `PGHOST` | `localhost` | |
| `PGPORT` | `6432` | Matches `Makefile`'s `DB_PORT`, not Postgres' usual `5432` — chosen so `db-up`'s container doesn't collide with a local Postgres. |
| `PGUSER` | `postgres` | Must have full privileges over the target database (the fixture chain creates roles, schemas, extensions). |
| `PGPASSWORD` | `postgres` | |

`run` never inherits any other ambient `PGRST_*`/`PG*` variable from your
shell into the PostgREST processes it boots (`internal/instance.BuildEnv`
builds each child's environment explicitly from its own config map plus
`PATH`) — a case's actual behavior is never accidentally shaped by whatever
happens to be exported in your terminal.

**Test guards.** The package tests that need real infrastructure (a real
PostgREST binary, a real loaded fixture database) are skipped unless
explicitly opted into, so `go test ./...` is safe to run with nothing
booted:

| Variable | Gates |
|---|---|
| `ORACLE_TEST_BIN` | Set to a real PostgREST binary path to run `internal/cliexec` and `internal/instance` tests that actually start one. |
| `ORACLE_TEST_DB_URI` | Set alongside `ORACLE_TEST_BIN` (a `postgresql://` URI to a loaded fixture database) to run `internal/instance` tests that boot an instance and talk to it. |
| `ORACLE_TEST_DB` | Set to any non-empty value, with `make db-up` already running, to run `internal/db` tests that exercise the real fixture-loading chain. |

## Flags (`oracle run`)

| Flag | Default | Meaning |
|---|---|---|
| `-cases` | (all) | Comma-separated case ids to run, e.g. `-cases 1117,1125`. |
| `-areas` | (all) | Comma-separated area names to run, e.g. `-areas select,filters`. Combined with `-cases`, a case must satisfy both (they narrow each other, not union). |
| `-bin` | (fetched per `PIN`) | Path to a PostgREST binary to use instead of fetching the pinned release. |
| `-db` | `postgrest_conf_oracle` | Target database name (must already be loaded via `db-setup`). |
| `-report` | `report.json` | Path to write the machine-readable JSON report (`{results, findings, total, passed}`). |
| `-skip-cli` | `false` | Skip `request.kind: cli` cases (the `config` `--dump-config` and startup-validation cases). |
| `-skip-http` | `false` | Skip HTTP cases. |

`run` prints a human-readable summary to stdout (per-area `passed/total`,
each failing case's detail with its `source:` citation, every
`route.CrossCheckHarness` finding, and a `TOTAL passed/total` line) in
addition to writing the JSON report, and exits non-zero iff any selected
case failed or the run was interrupted.

## Failure routing

Failures are suite defects by definition (this runner tests the suite, not
PostgREST). Route them through CONTRIBUTING.md: re-verify the citation via
the bier-spec-audit workflow, or open delta-channel fixture work. Never
hand-edit cases/ to make this runner pass.

This runner has no skip mechanism, and none should be added to it — see
`HARNESS.md` §6 ("Divergence convention") for where a *consumer's* deliberate
divergence belongs (never here) and `CONTRIBUTING.md` ("Divergences belong
to consumers, not here") for the same rule stated for this repository. A
run that reports fewer than 805/805 is not this tool asking for a
workaround; it is the tool doing its job.
