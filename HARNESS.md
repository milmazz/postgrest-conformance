# HARNESS.md — the implementer contract

This document tells you everything you need to run `cases/` against your own
PostgreSQL-backed REST API server without reading any of bier's source. It is
a formalization of the reference harness bier used to derive and verify this
suite (`test/support/conformance_server.ex`, `test/support/http_case.ex`,
`test/support/conformance_assertions.ex`, and `docs/CONFORMANCE_IMPL.md` in
[milmazz/bier](https://github.com/milmazz/bier)) — read as *what a conformant
implementer must reproduce*, not as bier-specific instructions.

Every case is a black box: one HTTP request (or, for the `config` area, one
CLI invocation), one expected response. Nothing here requires you to run
bier itself.

---

## 1. Database build

Build the fixture database by running the numbered chain in `fixtures/` in
order, against two different databases. Full detail (including SQL snippets
and file ownership) lives in [`fixtures/README.md`](fixtures/README.md); the
contract is:

1. **`01_roles.sql`** runs against your admin/maintenance database (e.g.
   `postgres`) — it idempotently creates `postgrest_test_anonymous`,
   `postgrest_test_default_role`, and `postgrest_test_author`.
2. **Create the target database** with the collation pinned to byte ordering:

   ```sql
   CREATE DATABASE <db> TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C';
   ```

3. **`02_base.sql` through `07_analyze.sql`** run against the target
   database, in order, each via `psql -v ON_ERROR_STOP=1 -q -f <file>`, with
   the `PGTZ=UTC` environment variable set:

   | File | Contents |
   |---|---|
   | `02_base.sql` | Consolidated schemas, tables, views, functions, seed data for every area. |
   | `03_supplement.sql` | Small human-owned supplement loaded right after `02_base.sql`. |
   | `04_postgis.sql` | PostGIS-dependent objects (`test.shops`, the `geotest` schema). **Requires the PostGIS extension.** |
   | `05_corrections.sql` | Post-load seed corrections aligning consolidated seed data with what specific cases assert. |
   | `06_area_schemas.sql` | Generated: mirrors `test` into each pure table/data area schema as auto-updatable views (pass-through for tables; relations that are themselves *views* in `test` are mirrored by inlining their definitions — see `fixtures/README.md`), so `Accept-Profile: <area>` resolves to a real exposed schema. |
   | `07_analyze.sql` | `ANALYZE;` — refreshes planner statistics (the `count=planned`/`count=estimated` pagination cases assume analyzed tables). |

**PostgreSQL / PostGIS requirement.** The reference implementation develops
and tests this suite against **PostgreSQL 17** with the **PostGIS**
extension installed and available in the target database before
`04_postgis.sql` runs — cases 1616–1618 and the PostGIS-backed feature areas
depend on it; every other area does not. This suite does not itself assert or
require a specific minimum PostgreSQL version below 17; if you validate
against an older server, verify independently.

**Why `LC_COLLATE 'C'`.** Some cases assert an ordering that only holds under
byte (C-locale) collation, not a linguistic one. Case **1606**
(`GET /simple_pk?order=k.desc`) expects the row keyed `xyyx` before the row
keyed `xYYx` — true under byte ordering, false under most linguistic
collations. Load the target database with `LC_COLLATE 'C' LC_CTYPE 'C'` as
shown above.

**Why `PGTZ=UTC`.** Some fixture seeds use timestamp literals with no explicit
UTC offset (e.g. a `timestamptz` literal in the `domain_representations`
area). Loading with `PGTZ=UTC` makes those literals resolve to the same
absolute instants the cases were captured against; loading under a different
session timezone shifts them.

---

## 2. Server configuration

The reference harness (bier) ran **two named server instances** that differ
only in auth configuration, plus a small number of **per-case variant
instances** for cases whose `config:` block cannot be honored by a shared
instance. Running the suite against real PostgREST v16.0 (the oracle runner
in `tools/oracle/`) showed that the single wide no-auth instance is **not
portable** — real PostgREST's embed-ambiguity detection scans every exposed
schema — so the no-auth side is now specified as a **per-area single-schema
layout** (§2.1.1) sharing the §2.1 option set; the "auth" instance (§2.2)
keeps the original wide shape. Route each case to the correct
instance/config (§2.3) before sending its request.

### 2.1 Shared no-auth base option set ("bulk")

No JWT secret, no anonymous role, no pre-request hook configured, so
requests run as the connecting database user (no role switching). Serves
every case whose `schema:` is not `"auth"` and whose request path is not
`/`. (Earlier revisions also excluded a `schema: "openapi"` label here; no
case carries it — it fell out of the contract with the `openapi` schema,
per the `db_schemas` note below.)

> **Portability note — anonymous role.** "No anonymous role configured" is a
> bier behavior that real PostgREST cannot reproduce: without `db-anon-role`,
> PostgREST answers every JWT-less request with **401**. The closest portable
> equivalent — what the oracle runner does — is
> `db-anon-role = <the connecting database user>`, which preserves the stated
> semantics exactly (requests run as the connecting user, no role switching).
> Apply it to every no-auth instance; the auth instance (§2.2) keeps its own
> explicit `postgrest_test_anonymous`.

Full option set (`ConformanceServer.base_opts/0`), copied verbatim — the DB
connection fields are placeholders for *your* server and database, everything
else is a fixed value the cases assert against:

| Key | Value | Notes |
|---|---|---|
| `hostname` | `"localhost"` | |
| `port` | `5432` | |
| `database` | `"bier_test"` | → your test database (name is arbitrary; must be the DB built in §1). |
| `username` | from `PGUSER`/`USER` env, default `"postgres"` | Must have full privileges over the target database. |
| `password` | from `PGPASSWORD` env | |
| `pool_size` | `10` | |
| `db_schemas` | per instance — see §2.1.1 | Ordered; on each instance the **first** entry is the default schema used when a request carries no profile header. The full no-auth label set across the layout is `test`, `operators`, `ordering`, `pagination`, `representations`, `mutations`, `rpc`, `headers`, `config`, `domain_representations`, `observability`, `v1`, `v2`, `SPECIAL "@/\#~_-`, `تست`. Earlier revisions listed all of these **plus `"openapi"`** on one wide instance: `openapi` is **dropped from the contract** — no fixture creates a schema by that name, real PostgREST refuses to boot when a listed schema does not exist, and no case sends `Accept-Profile: openapi` (the openapi-area cases run against `test` or `openapi_no_comment`). |
| `db_schema_aliases` | `%{"unicode" => "تست"}` | Profile-label aliases accepted as `Accept-Profile` that are not literal schema names. |
| `db_profile_default` | `"v1"` | Multi-schema profile routing (the `headers` area): the default profile resolves to `v1` and is echoed back as `v1`. |
| `db_profile_schemas` | `["v1", "v2", "SPECIAL \"@/\\#~_-"]` | Seeds the `PGRST106` hint listing valid profiles. |
| `db_extra_search_path` | `["public"]` | |
| `db_max_rows` | `nil` | No global cap. |
| `db_max_rows_by_schema` | `%{"config" => 2}` | `db-max-rows=2` applies **only** to the `config` schema (cases 1700/1701); every other area is uncapped on the same instance. A per-schema cap is a bier option with no real-PostgREST equivalent — portably, honor those two cases' own `config: {db-max-rows: 2}` blocks as variant instances instead (§2.3). |
| `db_plan_enabled` | `true` | |
| `db_tx_end` | `:rollback` | Every request's transaction is rolled back after the response is computed, so writes never persist — keeping concurrent async cases on the shared fixture DB from contaminating each other. This instance does **not** honor a `Prefer: tx=` override: PostgREST only honors a per-request `tx=` override when `db-tx-end` is configured to *allow* it, which this harness's config does not. `tx=commit`/`tx=rollback` remain valid, accepted Prefer tokens — a client may send either and it is recognized like any other preference — but neither changes whether the transaction actually commits. Case 1230's note pins this explicitly ("no `Prefer: tx=commit` override path"); cases 1004 and 1021 each send `Prefer: tx=commit` and their writes must still not persist. |
| `db_safe_update_tables` | `["safe_update_items", "safe_delete_items"]` | bier option with no real-PostgREST equivalent — see §2.5 for the portable form (cases 1387–1389 run on a dedicated instance whose database connection preloads the `safeupdate` extension). |
| `jwt_aud` | `nil` | |
| `server_cors_allowed_origins` | `"http://example.com, http://example2.com"` | |
| `server_timing_enabled` | `true` | Most cases assert `Server-Timing` is present; the few needing it absent (1497/1758/1763) run on their own variant instance instead. |
| `server_trace_header` | `"X-Request-Id"` | Case 1573 (`headers_no_blank`) needs it **unset** and runs on its own variant instance instead (§2.3) — with it set, PostgREST echoes a blank `X-Request-Id: ` on every response whose request carried no matching header. |
| `log_level` | `:error` | |

### 2.1.1 Per-area single-schema layout

**Do not build one wide instance exposing every schema above at once.** Real
PostgREST resolves an **unqualified embed target** (e.g. `select=id,users(id)`)
by scanning relationship candidates across *every* schema named in
`db-schemas`, not just the request's active profile — table/column resolution
is profile-scoped, but embed-ambiguity detection is not. Because
`fixtures/06_area_schemas.sql` mirrors the entire `test` schema into each
pure table/data area schema as views (so each area's profile resolves to a
real exposed schema), a single wide instance sees the same
`tasks`/`users`/etc. relationship independently offered by **eight** schemas
and reports an N-way ambiguous embed (**PGRST201**) even for a request
against the default `test` schema that has no ambiguity at all. Seven cases
fail this way on the wide layout (1117, 1125, 1198, 1199, 1213, 1222,
11415); the full by-hand repro is in
`docs/superpowers/notes/2026-08-22-oracle-first-run-triage.md`, Finding 1.

The portable layout — matching upstream PostgREST's own per-suite
single-schema test configs, and what the oracle runner builds — is
**thirteen no-auth instances**, each carrying the §2.1 option set verbatim
and differing *only* in `db_schemas`:

- **Eleven single-schema instances**, one per area label:
  `test`, `operators`, `ordering`, `pagination`, `representations`,
  `mutations`, `rpc`, `headers`, `config`, `domain_representations`,
  `observability` — each with `db_schemas: ["<label>"]`.
- **`multi`**: `db_schemas: ["v1", "v2", "SPECIAL \"@/\\#~_-"]` — `v1`
  first, so the default profile resolves to `v1` (this is what §2.1's
  bier-specific `db_profile_default`/`db_profile_schemas` options encode;
  on real PostgREST the schema-list order provides it). Serves the
  multi-schema profile-routing cases: `schema: multi` (the `v1`/`v2` pair)
  **plus the six id-selected `schema: headers` exceptions below**.
- **`unicode`**: `db_schemas: ["تست"]` — serves `schema: unicode`.

Route each non-auth case to the instance matching its `schema:` label
(absent/`"public"`/`"test"` → the `test` instance; `unicode` → the `تست`
instance; `multi` → the `multi` instance) — **with one id-selected
exception**: the six headers-area profile-routing cases **1557, 1558, 1559,
1560, 1574, 1583** carry `schema: headers` but exercise `v1`/`v2`/`SPECIAL`
multi-schema behavior, and must route to the **`multi`** instance. On the
single-schema `headers` instance they demonstrably fail: 1557 asserts the
default-profile `Content-Profile: v1` echo, which PostgREST emits only when
more than one schema is exposed; 1560 asserts the 406/`PGRST106` hint
listing `v1, v2, SPECIAL "@/\#~_-` (and 1583 reuses that 406 scenario to
assert `Vary` is absent on errors); 1558/1559/1574 read or write
`v2`/`SPECIAL` via their own explicit profile headers. Like §2.5's
1387–1389, the selection is by case id — the label alone does not encode it.

**Skip §3 step 4's profile-header injection for every case routed to
`multi`** — both `schema: multi` and the six exceptions. Those labels are
fixture labels, not schema names exposed on that instance: injecting
`Accept-Profile: headers` there turns e.g. 1557's 200 into a 406, and the
cases that need a specific profile already carry it in their own
`request.headers`. On a single-schema instance the case's label is already
the default schema, so the injection is a harmless no-op there — it still
matters on the auth instance (§2.2), the layout's one remaining
multi-schema instance with injection.

**Why view mirrors are inlined (case 1824, issue #9 — resolved).**
PostgREST traces a view's column ancestry through `pg_depend` to find the
source table's primary key (used to compute `Location` on insert), and that
traversal only sees relations in schemas listed in `db-schemas`. A
pass-through area mirror of a relation that is itself a *view* in `test`
(e.g. `domain_representations.datarep_todos_computed` over
`test.datarep_todos_computed`) is a two-hop chain whose intermediate hop is
invisible on a single-schema instance — the chain can't be completed and
`Location` is silently omitted. This is the same cross-schema-introspection
behavior as the ambiguity above, surfaced by the per-area split. It is why
`06_area_schemas.sql` mirrors `test` *views* by **inlining their
definitions** (single hop over the same base tables) instead of selecting
from the view — see the §1 fixture table and `fixtures/README.md`. An
implementer generating their own mirrors must preserve that property.

### 2.2 Shared "auth" instance

Everything in §2.1, **plus**:

| Key | Value | Notes |
|---|---|---|
| `db_anon_role` | `"postgrest_test_anonymous"` | |
| `db_pre_request` | `"auth.switch_role"` | Runs inside the request's transaction. |
| `jwt_secret` | `"reallyreallyreallyreallyverysafe"` | HS256 secret, matching PostgREST's own `testCfg` default (≥ 32 chars). |

Serves cases whose `schema:` is `"auth"`, **or** whose request path is
exactly `/` (the root/OpenAPI document resolves the anonymous role to
filter which routes it lists, even though it never switches the database
role for the request itself). No case carries a `schema: "openapi"` label
(earlier revisions named it here alongside the since-dropped `openapi`
schema); the openapi-area cases route by their `/` path — including 1654,
whose `openapi_no_comment` label rides the root-path rule onto its §2.5
variant.

Unlike the no-auth side (§2.1.1), the auth instance keeps the **wide**
`db_schemas` list — every label from §2.1's set (minus the dropped
`openapi`) plus `auth`, i.e. `["test", "operators", "ordering",
"pagination", "representations", "mutations", "rpc", "headers", "config",
"domain_representations", "observability", "auth", "v1", "v2",
"SPECIAL \"@/\\#~_-", "تست"]`. **Residual risk:** because of the
cross-schema embed-ambiguity behavior described in §2.1.1, an auth-routed
case using an *unqualified* embed would hit the same false PGRST201 on this
instance. No such case exists today — do not add one without revisiting
this layout.

### 2.3 Per-case variant instances

A small set of cases declare a `config:` block in their YAML that neither
shared instance can honor (e.g. a different `jwt-secret`, a disabled
anonymous role, a non-default `openapi-mode`). Each such case gets its **own**
dedicated server instance, built by layering on top of whichever shared
config it would otherwise use:

1. Start from `base_opts` (§2.1) if the case is not an auth/root-path
   case per the §2.2 rule, else `auth_opts` (§2.1 + §2.2). Under the
   per-area layout (§2.1.1) "the §2.1 base" means the instance config the
   case's `schema:` label would otherwise route to — the table below writes
   this as **bulk**.
2. Merge in the case's own `config:` block, translated per §2.4 below.
3. Merge in any **hard-coded extra options** for that specific case id
   (see §2.5). Three further cases — **1387–1389** — need a variant
   instance with **no** `config:` block at all, selected purely by case id;
   they are documented in §2.5, not in this table.

**Every** case that declares a `config:` block needing a variant instance,
its base, its declared `config:`, and the resulting server option(s) — plus
**1654**, which declares no `config:` of its own but keeps a row here
because its variant is built entirely from §2.5's hard-coded
`db_schemas` override (unlike 1387–1389, which need no table row at all):

| Case id | Base | Case's `config:` | Resulting option(s) |
|---|---|---|---|
| 1129 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` (aggregate functions are disabled by default) |
| 1130 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1131 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1132 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1133 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1139 | bulk | `url-use-legacy-target-names: false` | `url_use_legacy_target_names: false` |
| 1140 | bulk | `url-use-legacy-target-names: false` | `url_use_legacy_target_names: false` (same overlay as 1139) |
| 1147 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1148 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1149 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 1466 | auth | `jwt-secret: cmVhbGx5cmVhbGx5cmVhbGx5cmVhbGx5dmVyeXNhZmU=`, `jwt-secret-is-base64: true` | both — the §2.2 secret base64-encoded, with the base64 flag set |
| 1467 | auth | `jwt-secret: asymmetric_jwk_public_key` | `jwt_secret: <RS256 public JWK, see §2.6>` |
| 1468 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1469 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1470 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1471 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1472 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1473 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1475 | auth | `db-pre-request: test.switch_role` | `db_pre_request: "test.switch_role"` (overrides the auth base's `auth.switch_role`) |
| 1476 | auth | `db-pre-request: test.switch_role` | `db_pre_request: "test.switch_role"` |
| 1477 | auth | `db-pre-request: test.switch_role` | `db_pre_request: "test.switch_role"` |
| 1491 | auth | `db-anon-role: null` | `db_anon_role: nil` (overrides the auth base's anon role — anonymous access is disabled) |
| 1492 | auth | `db-anon-role: null` | `db_anon_role: nil` (same overlay as 1491, exercised with a *valid* token instead of none — may share 1491's instance) |
| 1493 | auth | `jwt-secret: null` | `jwt_secret: nil` (overrides the auth base's secret; `db_anon_role` stays set, so auth stays "applicable" and a presented token hits the no-secret path) |
| 1494 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` (no token sent — anonymous access must still succeed) |
| 1497 | auth | `jwt-cache-max-entries: 86400`, `server-timing-enabled: false` | both — `jwt_cache_max_entries: 86400`, `server_timing_enabled: false` |
| 1498 | auth | `jwt-role-claim-key: "$.postgrest.a_role"` | `jwt_role_claim_key: "$.postgrest.a_role"` |
| 1499 | auth | `jwt-role-claim-key: "$.customObject.manyRoles[1]"` | `jwt_role_claim_key: "$.customObject.manyRoles[1]"` |
| **1654** | auth (root path) | *(none — hard-coded, see §2.5)* | `db_schemas: ["openapi_no_comment"]` |
| 1677 | auth (root path) | `openapi-mode: ignore-privileges` | `openapi_mode: "ignore-privileges"` |
| 1678 | auth (root path) | `openapi-mode: disabled` | `openapi_mode: "disabled"` |
| 1680 | auth (root path) | `openapi-security-active: true` | `openapi_security_active: true` |
| 1682 | auth (root path) | `db-root-spec: root` | `db_root_spec: "root"` |
| 1700 | bulk | `db-max-rows: 2` | `db_max_rows: 2` (the portable form of §2.1's `db_max_rows_by_schema` cap for the `config` area) |
| 1701 | bulk | `db-max-rows: 2` | `db_max_rows: 2` |
| 1703 | bulk | `server-cors-allowed-origins: ""` | `server_cors_allowed_origins: ""` (empty list ⇒ CORS allows any origin) |
| 1742 | auth (root path, `OPTIONS /`) | `server-cors-allowed-origins: ""` | `server_cors_allowed_origins: ""` |
| 1495 | auth | `jwt-secret: asymmetric_jwks_public_key` | `jwt_secret: <RS256 public JWK wrapped as a JWK Set, see §2.6>` |
| 1517 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1518 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1522 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1573 | bulk | `server-trace-header: ""` | `server_trace_header: ""` (unset — upstream runs its `noBlankHeader` test with no trace header configured; on a trace-header-configured instance PostgREST echoes a blank `X-Request-Id: ` when the request sends none, tripping `headers_no_blank`) |
| 1758 | bulk | `server-timing-enabled: false` | `server_timing_enabled: false` |
| 1763 | bulk | `server-trace-header: ""` | `server_trace_header: ""` (unset — the trace header must NOT be echoed) |
| **1764** | auth (root path) | `log-level: error` | `log_level: :error` **plus** the hard-coded `jwt_secret: nil` from §2.5 (both apply — auth base's `db_anon_role` stays set, so the request still routes through JWT resolution and hits the no-secret 500) |
| 1765 | bulk | `log-level: warn` | `log_level: :warn` |
| 1766 | bulk | `log-level: info` | `log_level: :info` |
| 1767 | bulk | `log-level: crit` | `log_level: :crit` |
| 11115 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` (same overlay as 1129–1133/1147–1149 — may share their instance) |
| 11116 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 11117 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 11118 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` |
| 11119 | bulk | `db-aggregates-enabled: true` | `db_aggregates_enabled: true` (the PGRST127 rejection fires with aggregates ENABLED — it is a to-many-spread limitation, not the PGRST123 disallowed error) |
| 11800 | auth | `jwt-role-claim-key: '$["https://www.example.com/roles"][0].value'` | same key |
| 11801 | auth | `jwt-role-claim-key: "$.myDomain[3]"` | same key |
| 11802 | auth | `jwt-role-claim-key: "$.myRole"` | same key |
| 11803 | auth | `jwt-role-claim-key: '$.realm_access.roles[?(@ == "postgrest_test_author")]'` | same key |
| 11804 | auth | `jwt-role-claim-key: '$.realm_access.roles[?(@ != "other")]'` | same key |
| 11805 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud"` | both keys |
| 11806 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud"` | both keys |
| 11807 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud[0]"` | both keys |
| 11808 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud[1]"` | both keys |
| 11815 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 11816 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 11817 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 11818 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |

Each variant instance is otherwise identical to its base (same schemas, same
fixtures) and serves only its one case, so a small connection pool is fine.

### 2.4 Translating a case's `config:` block into server options

A case's `config:` map uses PostgREST's own kebab-case config names.
Translate each entry to your server's config surface with these rules
(copied from `ConformanceServer.translate/1`, lines 141–157 of
`test/support/conformance_server.ex`):

```elixir
defp translate(config) do
  Enum.map(config, fn
    {"db-schemas", v} when is_binary(v) -> {:db_schemas, [v]}
    {"log-level", v} when is_binary(v) -> {:log_level, String.to_atom(v)}
    {k, v} -> {k |> String.replace("-", "_") |> String.to_atom(), resolve(v)}
  end)
end

defp resolve("asymmetric_jwk_public_key"), do: @asymmetric_jwk_public_key
defp resolve("asymmetric_jwks_public_key"),
  do: ~s({"keys":[) <> @asymmetric_jwk_public_key <> "]}"
defp resolve(value), do: value
```

In plain terms:

- **General rule:** a kebab-case key (`server-cors-allowed-origins`) becomes
  the matching snake_case option name (`server_cors_allowed_origins`); the
  value passes through unchanged as parsed from YAML (`null` → your
  language's "unset"/nil, `true`/`false` as booleans, strings as strings).
- **`db-schemas`:** if the YAML value is a single scalar string (one schema),
  wrap it in a one-element list — your config option almost certainly expects
  a list even when there's only one schema.
- **`log-level`:** the YAML value is a plain string (e.g. `"error"`); convert
  it to whatever enum/atom type your log-level option expects.
- **Symbolic placeholders:** two config values are *symbolic names*, not
  literal config values — `asymmetric_jwk_public_key` and
  `asymmetric_jwks_public_key`. Resolve them to the real JWK values in §2.6
  before applying the config. Every other value is used as-is.

### 2.5 Hard-coded extra options (`variant_extra_opts`)

A handful of cases need an extra option that is **not** expressed anywhere
in their case YAML's `config:` block — it's a fact about the harness's
fixture layout, not about PostgREST config: the two `variant_extra_opts`
cases below (1654, 1764), plus the safe-update trio (1387–1389) at the end
of this section. (Case 1764 also happens to carry an ordinary
`config: {log-level: error}` block of its own, translated the normal way per
§2.4 — that part is unrelated to the hard-coded option below; see its §2.3
table row.) Copied verbatim from `ConformanceServer.variant_extra_opts/1`
(lines 112–117):

```elixir
# Case 1654 asserts the default title/description when the exposed schema has
# no COMMENT; expose a comment-less schema so the shared "test" schema (which
# has a comment needed by case 1656) is not affected.
defp variant_extra_opts(1654), do: [db_schemas: ["openapi_no_comment"]]
# Case 1764 asserts the no-JWT-secret 500 path (PGRST300); its instance must
# run without a secret even though auth_opts configures one (db_anon_role
# keeps auth applicable so resolve/JWT runs and yields PGRST300).
defp variant_extra_opts(1764), do: [jwt_secret: nil]
defp variant_extra_opts(_id), do: []
```

Concretely:

- **Case 1654** needs a schema exposed under the label `openapi_no_comment`
  that is identical to your `openapi`/root-document schema but carries no
  `COMMENT ON SCHEMA`, so the OpenAPI document falls back to its default
  title (`"PostgREST API"`) and description
  (`"This is a dynamic API generated by PostgREST"`). Route this case's
  server instance to that schema instead of your commented `test`/`openapi`
  schema (whose comment is asserted by a different, non-variant case).
- **Case 1764** needs an instance with no JWT secret configured at all
  (`jwt_secret: nil`/unset) while still having an anonymous role configured,
  so that presenting *any* bearer token routes through JWT verification and
  hits the "server lacks JWT secret" 500 (`PGRST300`) rather than silently
  falling back to anonymous access.

**Cases 1387–1389 (safe-update / safe-delete)** carry **no** `config:` block
at all and are selected purely by case id: they assert the behavior of the
PostgreSQL **`safeupdate`** extension (an UPDATE/DELETE without a WHERE
clause is rejected), which bier expresses through its own
`db_safe_update_tables` option (§2.1) — an option real PostgREST does not
have. The portable equivalent, used by the oracle runner, is a dedicated
variant instance (base: the `mutations` area instance) whose **database
connection preloads the extension**, e.g. by appending
`?options=-csession_preload_libraries%3Dsafeupdate` to the connection
URI/`db-uri`. This requires the `pg_safeupdate` extension to be installed in
the fixture PostgreSQL (the oracle's `Makefile` `db-up` container ships it);
no other case may run on this instance — with `safeupdate` loaded,
*every* unfiltered UPDATE/DELETE fails.

### 2.6 Asymmetric (RS256) JWK values

Cases 1467 and 1495 configure an asymmetric `jwt-secret` and present an
RS256-signed token. This is the exact **public** key value PostgREST's own
test suite verifies against (`testCfgAsymJWK` in
`test/spec/SpecHelper.hs` upstream) — the matching private key is
upstream-only and never appears in this suite; only verification is
exercised:

```json
{"alg":"RS256","e":"AQAB","key_ops":["verify"],"kty":"RSA","n":"0etQ2Tg187jb04MWfpuogYGV75IFrQQBxQaGH75eq_FpbkyoLcEpRUEWSbECP2eeFya2yZ9vIO5ScD-lPmovePk4Aa4SzZ8jdjhmAbNykleRPCxMg0481kz6PQhnHRUv3nF5WP479CnObJKqTVdEagVL66oxnX9VhZG9IZA7k0Th5PfKQwrKGyUeTGczpOjaPqbxlunP73j9AfnAt4XCS8epa-n3WGz1j-wfpr_ys57Aq-zBCfqP67UYzNpeI1AoXsJhD9xSDOzvJgFRvc3vm2wjAW4LEMwi48rCplamOpZToIHEPIaPzpveYQwDnB1HFTR1ove9bpKJsHmi-e2uzQ","use":"sig"}
```

Case 1467 configures this JWK bare, as the literal value of `jwt-secret`.
Case 1495 exercises a JWK **Set** instead of a bare JWK — configure
`jwt-secret` as `{"keys":[<the JWK above>]}` — to prove your server accepts
both shapes.

Both cases send the same token, a literal `Authorization: Bearer …` header
(not a `sign_with`-minted one, since neither test's key is in §3.2's known
secret set):

```
Authorization: Bearer eyJhbGciOiJSUzI1NiJ9.eyJyb2xlIjogInBvc3RncmVzdF90ZXN0X2F1dGhvciJ9Cg.CBOYWDvqgAR0YYnZnyDGTQi6AJLc2Pds6_eV3YuBG6I36mj_h05eLhkEKNEDA5ZteMzCiY83P60rC_xtxVd7B6vo3BeF5uoanPS3rrbuHzKPwzsrgrD_CqvEuJ4n7Q9epkQiLsNkcexneENZDRqFjbwZx3DrXiCWwlK3Ytr5NAIGxmy0od-0xNpb2U1nXQyO_Q3mumWFViRt4tmFn_3goDHNKG3Ha_AzImfUNvHnWL78kAc4rbn15vLtWXD8PwtSnZaB4lY4V6RfsaW937srQsmRetvytM1i_bHBnjkjQLAqGbXPyItjtlXPs0uGNBadE8-wgkLtfmSCC4v2DjUthw
```

which decodes to header `{"alg":"RS256"}`, payload
`{"role": "postgrest_test_author"}`, both expecting `status: 200`.

---

## 3. Request execution

For each case's `request:` block:

1. **Method** — `request.method` (`GET`/`HEAD`/`POST`/`PATCH`/`PUT`/`DELETE`/
   `OPTIONS`), sent as-is.
2. **Path** — `request.path` is sent **raw**, exactly as written in the case
   file, including its query string. It is the logical request target; if
   your HTTP client library needs characters percent-encoded before it will
   put them on the wire (spaces, quotes, non-ASCII bytes, a literal `%` that
   isn't already a valid `%XX` escape, etc.), encode only what your
   transport layer requires and leave everything else — existing `%XX`
   escapes, `+`, and the reserved delimiters `, ( ) = & ? / . :` and `*` —
   untouched, so the server decodes back to the exact same logical request.
   This is purely a client-side deliverability concern; it does not change
   what the server receives.
3. **Headers** — send `request.headers` (if present) verbatim, name and
   value as given.
4. **Schema → profile header.** A case's `schema:` field is a fixture-set
   label (§7), not itself a header. Send **no** profile header when `schema`
   is absent, `"public"`, or `"test"`; otherwise inject the label as the
   request's profile header — whose **name depends on the request method**:

   - `GET` / `HEAD` / `OPTIONS` → `Accept-Profile: <schema>`
   - `POST` / `PATCH` / `PUT` / `DELETE` (including a `POST` to
     `/rpc/<fn>`, regardless of the function's volatility) →
     `Content-Profile: <schema>`

   PostgREST selects a write's schema **only** from `Content-Profile`
   (PostgREST docs, `references/api/schemas.rst`, "Multiple schemas"). An
   `Accept-Profile` header on a write is not an error — it is **silently
   ignored** and the request resolves against the *default* schema instead
   (confirmed empirically against the pinned v16.0 binary; case 1011's
   `notes:` states the same rule). An implementer injecting only
   `Accept-Profile` therefore silently mis-routes every auto-labeled write.
   Earlier revisions of this step described only the `Accept-Profile` half;
   the method-dependent rule above supersedes them (issue #5).

   Injection uses put-new semantics: if the case's own `request.headers`
   already sets the header that would be injected, the case's value wins —
   don't overwrite it. Under the per-area layout (§2.1.1) the injection is
   a harmless no-op on single-schema instances (the label is already the
   default schema) and is **skipped entirely for cases routed to the
   `multi` instance** (see §2.1.1's routing rules — their labels are not
   schema names exposed there); it still matters on the auth instance.
5. **Request body** — a case carries at most one of these three request-body
   forms, in this precedence:
   - `body_raw` — a string sent **verbatim** on the wire (CSV, deliberately
     invalid JSON, raw octet bytes, or an empty string for a no-body POST).
   - `body_json` — always JSON-encoded before sending, even if it happens to
     already look like a string.
   - `body` — JSON-encoded unless the value is already a string, in which
     case it is sent as-is.
   No body key present ⇒ no request body.

### 3.1 JWT tokens (`request.jwt`)

Most auth cases carry a `request.jwt` block instead of a literal
`Authorization` header:

```yaml
jwt:
  sign_with: hs256_test_secret
  payload: { "role": "postgrest_test_author", "id": "jdoe" }
```

- `sign_with` names a known secret/algorithm pair (§3.2). Sign `payload` with
  that algorithm and secret, then send
  `Authorization: Bearer <compact JWS>` — **only if the case did not already
  set its own `Authorization` header** (don't overwrite an explicit one).
- **Sign the payload as-is.** Several cases deliberately carry claim types the
  *server* is expected to reject (e.g. a string `exp` instead of a number) —
  the token-minting step must not validate, coerce, or reject anything in
  `payload`; that is the server's job, and the case is testing it.
- Example, case **11800** (`auth/role-claim-key/quoted-key-path`): signs
  `{"https://www.example.com/roles": [{"value": "postgrest_test_author"}], "other": 666}`
  with `hs256_test_secret`, sent against a server configured with
  `jwt-role-claim-key: '$["https://www.example.com/roles"][0].value'`
  (§2.4/§7 `auth`), expecting `200`.
- Example, case **1468** (`auth/audience/string-match`): signs
  `{"exp": 9999999999, "role": "postgrest_test_author", "id": "jdoe", "aud": "youraudience"}`
  with `hs256_test_secret`, against a server configured with
  `jwt-aud: youraudience` (variant instance, §2.3), expecting `200`.

Some cases carry a **literal, pre-computed** `Authorization` header instead
of `request.jwt`, either because the token must be signed with a secret the
harness deliberately does not know (a "wrong secret", proving verification
failure) or to pin an exact wire-byte token. Example, case **1452**
(`auth/role/valid-grants-access`):

```
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoicG9zdGdyZXN0X3Rlc3RfYXV0aG9yIiwiaWQiOiJqZG9lIn0.B-lReuGNDwAlU1GOC476MlO0vAt9JNoHIlxg2vwMaO0
```

— header `{"alg":"HS256","typ":"JWT"}`, payload
`{"role":"postgrest_test_author","id":"jdoe"}`, HS256-signed with the harness
secret. Case **11809** (`auth/jwt/wrong-secret`) is the deliberate-failure
counterpart: header `{"alg":"HS256"}`, payload `{}`, signed with the literal
key `"wrong secret"` instead of the harness secret, expecting `401` /
`PGRST301` — the server must reject it at signature verification, before any
claim is read.

### 3.2 Known signing secrets

Only one `sign_with` key is used across the suite:

| `sign_with` value | Algorithm | Secret |
|---|---|---|
| `hs256_test_secret` | HS256 | `reallyreallyreallyreallyverysafe` |

This is the same value as `jwt_secret` in §2.2/§2.1 (auth instance). It
matches PostgREST's own `testCfg` default secret.

---

## 4. Assertion semantics

Every key under a case's `expect:` block must be checked; an assertion key
your runner doesn't recognize should be treated as an error (fail loudly, not
silently), not skipped.

- **`status`** — exact integer HTTP status code match.

- **`headers`** — a map of header name → expected value. For **each named
  header only**, compare case-insensitively by name, exact string equality
  on the value. This is **not** an exhaustive check of the response's full
  header set: headers not mentioned anywhere in `expect` may be present or
  absent freely and are never checked. Repeated response headers are folded
  into one string joined with `", "` before comparison — **except**
  `Set-Cookie`, whose values may legitimately contain commas (e.g. the
  `Expires` date) and so must never be comma-folded; join repeated
  `Set-Cookie` values with `"\n"` instead (this is the encoding case 1568
  asserts against).

- **`headers_present`** — a list of header names that MUST be present
  (case-insensitive name match); value is unconstrained.

- **`headers_absent`** — a list of header names that MUST NOT be present at
  all (case-insensitive name match) — e.g. `Content-Type` must be absent on
  an empty-body `201`/`204`.

- **`headers_match`** — a map of header name → regular expression the
  header's (folded) value must match, e.g. the `Server-Timing` `dur=<ms>`
  pattern.

- **`headers_absent_in_value`** — a map of header name → list of substrings
  that must NOT appear anywhere within that header's value (treating a
  missing header as an empty value) — e.g. asserting `plan`/`transaction`
  never appear in an `OPTIONS` response's `Server-Timing` value.

- **`headers_no_blank`** — when `true`, asserts that **no header in the
  entire response** (not just named ones) has a blank (all-whitespace)
  value — a regression guard against stray `Name: ` headers with an empty
  value.

- **`body_exact`** / **`body_json`** (synonyms — a case uses one or the
  other): the response body, JSON-decoded, must be **deep-equal** to the
  expected value. Because comparison happens on the *decoded* structure,
  key order and insignificant whitespace in the response's raw bytes don't
  matter for this specific assertion. When the expected value is `null` or
  absent, the response body itself must be the **empty string** (not `"{}"`
  or `"null"` — literally zero bytes). An expected value of `""` (the
  empty string — 27 cases use `body_exact: ""`) is the **same empty-body
  sentinel**: treat it exactly like `null`/absent (the response body must
  be zero bytes), not as a JSON string comparison — this matches the
  reference assertion code, which handles `""` and `nil` identically.

- **`body_contains`** — one or more substrings (a single string, or a list)
  that must each appear somewhere in the **raw** response body — used for
  non-JSON bodies (CSV, etc.) where exact byte comparison would be too
  brittle but exact-substring still matters.

- **`body_raw`** — the raw response body must equal the expected string
  **exactly**, byte for byte — used for CSV and other non-JSON payloads.

- **`body_jsonpath`** — a list of `{path, ...}` entries, each checked against
  the JSON-decoded body via JSONPath. Each entry carries exactly one
  predicate: `equals: <value>` (the value at `path` must equal it),
  `present: true` / `exists: true` (a value must exist at `path`), or
  `absent: true` (no value may exist at `path`).

- **`exit_code`**, **`dump_contains`**, **`stderr_contains`**,
  **`dump_reparse_stable`** — used only by `kind: cli` cases (the `config`
  area's CLI invocations): the process's exit code (an integer, or the
  literal string `"nonzero"` meaning "any non-zero code"), substrings the
  dumped config output or stderr must contain, and whether re-feeding the
  dumped config and re-dumping it produces byte-identical output. These do
  not apply to HTTP cases.

  The CLI set is **not** all `--dump-config`. `request.flag` takes three
  values — `--dump-config` (37 cases), `--ready` (4) and `--example` (1) —
  plus case 1719, whose `flag` is a positional config path. The first two
  groups differ in kind, and an implementer must treat them differently:

  - **`--dump-config` and the config-path case are startup validation.** The
    process parses configuration and exits; it never listens. It normally
    never reaches Postgres either — the exception is the **db-config** cases
    1724, 1725 and 1749, which carry `config.preconditions_sql` and run with
    `PGRST_DB_CONFIG=true`, so the dump is produced *after* reading the
    `pgrst.*` role settings out of the database. (Case 1744 carries the same
    `preconditions_sql` but sets `PGRST_DB_CONFIG=false` precisely to pin
    that the in-db source is then never consulted.)
  - **`--ready` (cases 1745–1748) is a health-check client.** It builds
    `http://<admin-server-host>:<admin-server-port>/ready` and performs a
    real outbound TCP connection. It still never reaches Postgres (`CLI.hs`
    dispatches to `PostgREST.Client.ready` before any app-state
    initialisation), so it needs no database — but it does depend on the
    host's network stack.

  > **Environmental precondition for case 1746.** 1746 asserts
  > `connection refused to http://localhost:1/ready`, which requires that
  > nothing listens on `localhost:1` **and** that the connect be actively
  > *refused* rather than filtered or dropped. On a host whose firewall
  > DROPs traffic to low ports, the connect instead hangs until the runner's
  > 30-second CLI timeout and the case fails with a timeout rather than a
  > mismatch. If you see exactly that failure, check the host's egress
  > filtering before suspecting the assertion. Cases 1747 and 1748 are
  > decided before any packet leaves the process (invalid URL, and special
  > hostname respectively), so neither depends on the network.

- **`status_text`** (defined in `case.schema.json` but **not implemented by
  the reference assertion code**) — the expected HTTP reason phrase. bier's
  own harness cannot assert it (its HTTP client, `Req`, doesn't expose the
  reason phrase) and tags the 3 cases that use it `:pending`, excluding them
  from its own run. If your HTTP client exposes the reason phrase, you can
  implement this assertion yourself; if not, you may skip these 3 cases the
  same way, with a note in your own divergence list (§6).

### 4.1 `body_exact` byte profile — why exact wire bytes still matter

`body_exact`/`body_json` compare *decoded* JSON, so whitespace and key order
in your raw response bytes don't affect those two assertions directly.
**However**, several cases separately assert an exact `Content-Length`
header value (via `headers`/`headers_match`) for a JSON response body — and
that value depends entirely on the exact byte count your server puts on the
wire. To match PostgREST's `Content-Length` byte-for-byte, reproduce its
rendering shape:

- **Parent (top-level) rows** are rendered *compact*: a bare row/record
  aggregated with `json_agg`, giving `{"k":v,"k2":v2}` objects (no space
  after the colon) separated by **a comma, a newline, and a space**
  (`, \n `) between elements — not `json_build_object`'s `"k" : v` spacing,
  and not a pre-rendered/re-encoded form (which drops the newline
  separator).
- **Embedded (nested) objects** — the result of `select=...,rel(...)` —
  are rendered through **jsonb** (`to_jsonb`/`json_agg(to_jsonb(...))`)
  instead: jsonb's own text output uses `": "` spacing (a space after the
  colon) and jsonb's key normalization, while the parent row around them
  stays compact as above.

If your JSON serializer doesn't reproduce this exact separator/spacing
profile, your `body_exact`/`body_json` assertions can still pass (decoded
comparison is forgiving) while your `Content-Length` assertions fail on the
same response. Also always emit `Content-Type: application/json;
charset=utf-8` on JSON responses.

---

## 5. Response compression

PostgREST **never compresses** its responses and **always emits an exact
`Content-Length`** header (never chunked transfer-encoding for a body it
computed itself). Reproduce both:

- Do not gzip/deflate/brotli-compress response bodies, and do not honor
  `Accept-Encoding` for compression purposes.
- Always compute and send a correct `Content-Length` for every response with
  a body (see §4.1 for the exact-byte-count implications).

The reference server (bier, running on Bandit) achieves this by booting with
`http_options: [compress: false]` — Bandit otherwise strips `Content-Length`
and may compress. If you write your own HTTP test client against your
server, be aware many HTTP client libraries silently decompress bodies and
**strip the `Content-Length` header** as part of that (Elixir's `Req` does,
via its `decompress_body` step) even when the wire response *did* carry a
correct value — so a client-side `Content-Length` assertion can spuriously
fail if your client library auto-decompresses. Disable your client's
automatic decompression and stop it from sending `Accept-Encoding` so the
server's real header survives untouched into your assertion code.

---

## 6. Divergence convention

This suite records what the reference implementation (PostgREST v16.0) does
— **it never records an implementer's deliberate divergence from that
behavior.** If your server intentionally does not, and will not, match a
specific case (a considered design decision, not a bug), keep that decision
in **your own** skip/divergence list, outside this repository — do not ask
for the case itself to be changed or removed.

For a concrete reference pattern, see how bier does this (its
`test/conformance/conformance_test.exs`, not part of this repository): a
private `@divergences` map of `case_id => reasoning`. Every entry is tagged
`:pending` at test time and fails with that reasoning printed, rather than
silently skipping. Critically, it also carries a `@divergence_pins` map
of `case_id => {path_into_expect, pinned_value}`, checked at compile
time — if a spec re-sync ever renumbers or rewrites that case so it no
longer asserts the exact upstream value the divergence was written against,
the **build fails** instead of the exemption silently going stale or
dangling. A minimal example (one entry) of the shape:

```elixir
@divergences %{
  1771 => "Server: bier/<version>, not postgrest/<version> — a Server header " <>
          "names the software that built the response, and wearing " <>
          "upstream's product token would misattribute Bier's bugs to " <>
          "PostgREST. The dialect is advertised through the OpenAPI " <>
          "document's externalDocs instead (#122)."
}
@divergence_pins %{1771 => {["headers_match", "Server"], "^postgrest/.+"}}
```

Whatever mechanism you use, every entry should require an explicit,
reviewed sign-off (not something a contributor can add unilaterally) and
should be hard to grow silently.

---

## 7. Areas

Each case is tagged by the first `/`-delimited segment of its `feature:`
field. Areas are derived from [`INDEX.md`](INDEX.md) — id bands below are
authoritative for *routing which schema/instance a case needs* (§2), but the
`feature:` prefix, not a case's numeric id, is the authoritative area
assignment if the two ever look inconsistent.

> **Five areas overflow into a 5-digit band** once their primary 50-wide band
> filled: `operators` (10200+), `select` (11100+), `mutations` (11400+),
> `auth` (11800+), `content_negotiation` (12400+). These sort *lexically*
> right after unrelated 4-digit ids, so `ls cases/ | sort -n` (numeric
> sort), never a plain `ls`, is required to read ids in area order.

| Area | Id band(s) | Cases | `schema:` label(s) used |
|---|---|---:|---|
| url_grammar | 1000–1035 | 36 | `test`, `multi`, `unicode`, `ordering` |
| operators | 1050–1099, 10200–10236 | 87 | `operators` |
| select | 1100–1149, 11100–11140 | 91 | `test` |
| filters | 1150–1199 | 50 | `test` |
| ordering | 1200–1232 | 33 | `ordering`, `test`, `mutations` |
| pagination | 1250–1288 | 39 | `pagination` |
| representations | 1300–1327, 1330–1333 | 32 | `representations`, `rpc` |
| mutations | 1350–1399, 11400–11405, 11407–11415 (no 11406) | 65 | `mutations` |
| rpc | 1400–1443 | 44 | `rpc`, `test` |
| auth | 1450–1499, 11800–11818 | 69 | `auth` |
| errors | 1500–1530 | 31 | `test` |
| headers | 1550–1584 | 35 | `headers`, `test` |
| content_negotiation | 1600–1649, 12400–12401 | 52 | `test` |
| openapi | 1650–1688 | 39 | `test`, `openapi_no_comment` |
| config | 1700–1749 | 50 | `config` |
| observability | 1750–1771 | 22 | `observability` |
| domain_representations | 1800–1836 | 37 | `domain_representations`, `test` |

**Total: 812 cases across 17 areas** (36+87+91+50+33+39+32+65+44+69+31+35+52+39+50+22+37 = 812).

Recover a case's area directly from its own file, no index lookup needed:

```sh
grep -h '^feature:' cases/1800_format_single_domain_column.yaml
# feature: domain_representations/read/format_single_column
```

See [`INDEX.md`](INDEX.md) for label caveats (e.g. `unicode` → `تست`,
`multi` → the `v1`/`v2` pair) and per-area sub-feature breakdowns.
