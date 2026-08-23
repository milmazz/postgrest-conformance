# Fixture chain and file ownership

The conformance database is built by running a **numbered chain of SQL files**
in order, against two different databases:

1. `01_roles.sql` — run against the `postgres` maintenance database (or
   whichever admin/bootstrap database your cluster uses). It creates the
   `postgrest_test_anonymous`, `postgrest_test_default_role`, and
   `postgrest_test_author` roles idempotently.
2. Create the target database with the collation pinned to byte ordering —
   this is required for cases whose expected ordering depends on it (e.g. the
   `simple_pk` `order=k.desc` case expects `xyyx` before `xYYx`, which holds
   under byte ordering but not under a linguistic collation):

   ```sql
   CREATE DATABASE <db> TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C';
   ```
3. Run `02_base.sql` through `07_analyze.sql` in order, against the target
   database, each with `psql -v ON_ERROR_STOP=1 -q -f <file>` and the `PGTZ=UTC`
   environment variable set. `PGTZ=UTC` matters because some fixture seeds use
   timestamps without an explicit offset (e.g. a `timestamptz` literal in
   `domain_representations`); loading under UTC makes those the same absolute
   instants the cases were captured against.

   ```sh
   psql -d postgres -v ON_ERROR_STOP=1 -q -f fixtures/01_roles.sql
   psql -d postgres -c 'DROP DATABASE IF EXISTS <db>' \
     -c "CREATE DATABASE <db> TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"
   for f in fixtures/0{2,3,4,5,6,7}_*.sql; do
     PGTZ=UTC psql -d <db> -v ON_ERROR_STOP=1 -q -f "$f" || break
   done
   ```

## The chain

| File | Contents |
|---|---|
| `01_roles.sql` | Idempotent role creation. Runs against `postgres`, before the target database exists. |
| `02_base.sql` | The consolidated fixtures: schemas, tables, views, functions, and seed data for every area. Byte-identical to the upstream fixture file this suite was extracted from — see "Provenance" below. |
| `03_supplement.sql` | A small, human-owned supplement loaded right after `02_base.sql` and before area-schema mirroring. May reference `02_base.sql` objects; nothing in `02_base.sql` may depend on it. |
| `04_postgis.sql` | PostGIS-dependent objects: `test.shops` (GeoJSON cases 1616-1618) and the isolated `geotest` schema (geo+json feature tests: mutations/RPC/embeds). **Requires the PostGIS extension** to be installed and available in the target database. `geotest` is deliberately not exposed through the suite's schema list, so the frozen cases (including the OpenAPI document cases) never see it. |
| `05_corrections.sql` | Post-load seed corrections that align the consolidated fixtures with values specific cases assert but the merged seed data doesn't produce (see the file's own header comment for the two corrections and why). Confined to the `test` schema; idempotent. |
| `06_area_schemas.sql` | Generated — mirrors `test` into each pure table/data area schema (`operators`, `ordering`, `pagination`, `representations`, `mutations`, `headers`, `config`, `domain_representations`, `rpc`, `auth`) as auto-updatable views (with a handful of isolated real tables where writes must not leak into shared state), so requests carrying `Accept-Profile: <area>` resolve to real exposed schemas. Relations that are themselves *views* in `test` are mirrored by copying the view's own definition rather than selecting from the view — a pass-through mirror would be a two-hop view chain whose PK ancestry PostgREST cannot trace on a single-schema instance (issue #9, case 1824); views carrying INSTEAD OF triggers keep the pass-through so the trigger still fires. The generator asserts at regen time that every inlined view is single-hop over base relations and carries no view options that definition-copying would drop. See `tools/` for the generator. |
| `07_analyze.sql` | `ANALYZE;` — refreshes planner statistics. The `count=planned`/`count=estimated` pagination cases assume analyzed tables so that planner row estimates match the small fixture tables; this must run after every file that inserts data. |

## `inputs/`

`rpc.sql` and `headers.sql` are **live generator inputs**, not historical
provenance: the `06_area_schemas.sql` generator reads them at generation time
to build the real `rpc` and `headers` area schemas via text remapping
(`\btest\b` → the target schema name, and for `headers.sql`, `\bprivate\b` →
`headers_private`). They are human-owned — edited only in reviewed commits —
and carry invariants a careless edit will silently break:

1. `rpc.sql` must keep every object `test`-qualified and contain no string
   literal with the bare token `test` (the generator remaps `\btest\b` →
   `rpc`).
2. `headers.sql` must keep the literal marker line `-- Multi-schema tables`
   separating its test/private portion from the multi-schema portion, and no
   string literal may contain the bare tokens `test`/`private` (the generator
   splits on the marker and remaps those tokens).

## `provenance/`

Everything else copied from the original fixture-fragment set (per-area
`*.sql` files and their `*.delta.sql` write channels) is **frozen and
historical** — kept for traceability back to the spec-research process that
built this suite, but **not authoritative**. `02_base.sql` has since gained
objects and seed decisions these fragments never had. Do not "reconcile"
`02_base.sql` against them, and do not extend them directly: new fixture needs
go through the `<area>.delta.sql` write channel (new objects only, never
duplicating DDL that already exists in `02_base.sql`), which is then folded
into `02_base.sql` by a reviewed change.

## Ownership

| File(s) | Owner / writer | Role |
|---|---|---|
| `02_base.sql` | PR review | **Primary artifact.** The authoritative DDL+seed set the frozen case expectations were verified against. Never regenerated wholesale — it embeds merge decisions (superset seeds, collision renames, post-merge additions) that exist nowhere else. |
| `provenance/<area>.delta.sql` | PR review | **Write channel.** New objects only — never duplicate DDL that already exists in `02_base.sql`. Folded into `02_base.sql` by a reviewed change, then emptied. |
| `03_supplement.sql` | **human only, PR review** | Environment/harness-support supplement, loaded right after `02_base.sql` and before area-schema mirroring. Conformance cases must never depend on objects that exist only there. |
| `inputs/rpc.sql`, `inputs/headers.sql` | **human only, PR review** | **Live generator inputs** — see above. |
| `04_postgis.sql`, `05_corrections.sql`, `07_analyze.sql` | PR review | Fixed chain files; edited like any other tracked file, through review. |
| `06_area_schemas.sql` | generated | Do not hand-edit — regenerate from `inputs/` + `02_base.sql`/`03_supplement.sql` via the `tools/` generator and commit the result. |
| every other file in `provenance/` | frozen (historical) | Not authoritative — see above. Do not extend. |

## Invariants

1. `02_base.sql` must load standalone into a fresh database
   (`psql -v ON_ERROR_STOP=1`); `03_supplement.sql` may reference
   `02_base.sql` objects, never the reverse.
2. Every relation a conformance case references must exist after the full
   chain (`01` through `07`) has run.
3. See `inputs/` above for the `rpc.sql`/`headers.sql` remapping invariants.
4. PostGIS must be installed in the target database before `04_postgis.sql`
   runs. Cases 1616-1618 and the `geotest`-backed geo+json feature areas
   depend on it; every other area does not.
