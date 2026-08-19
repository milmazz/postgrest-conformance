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
   | `06_area_schemas.sql` | Generated: mirrors `test` into each pure table/data area schema as auto-updatable views, so `Accept-Profile: <area>` resolves to a real exposed schema. |
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

The reference harness runs **two named server instances** that differ only in
auth configuration, plus a small number of **per-case variant instances**
for cases whose `config:` block cannot be honored by either shared instance.
Route each case to the correct instance/config (§2.3) before sending its
request.

### 2.1 Shared "bulk" instance (no auth)

No JWT secret, no anonymous role, no pre-request hook configured, so
requests run as the connecting database user (no role switching). Serves
every case whose `schema:` is not `"auth"`/`"openapi"` and whose request path
is not `/`.

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
| `db_schemas` | `["test", "operators", "ordering", "pagination", "representations", "mutations", "rpc", "headers", "config", "openapi", "domain_representations", "observability", "auth", "v1", "v2", "SPECIAL \"@/\\#~_-", "تست"]` | Ordered; the **first** entry (`"test"`) is the default schema used when a request carries no `Accept-Profile` header. |
| `db_schema_aliases` | `%{"unicode" => "تست"}` | Profile-label aliases accepted as `Accept-Profile` that are not literal schema names. |
| `db_profile_default` | `"v1"` | Multi-schema profile routing (the `headers` area): the default profile resolves to `v1` and is echoed back as `v1`. |
| `db_profile_schemas` | `["v1", "v2", "SPECIAL \"@/\\#~_-"]` | Seeds the `PGRST106` hint listing valid profiles. |
| `db_extra_search_path` | `["public"]` | |
| `db_max_rows` | `nil` | No global cap. |
| `db_max_rows_by_schema` | `%{"config" => 2}` | `db-max-rows=2` applies **only** to the `config` schema (cases 1700/1701); every other area is uncapped on the same instance. |
| `db_plan_enabled` | `true` | |
| `db_tx_end` | `:rollback` | Every request's transaction is rolled back after the response is computed, so writes never persist — keeping concurrent async cases on the shared fixture DB from contaminating each other. This instance does **not** honor a `Prefer: tx=` override: PostgREST only honors a per-request `tx=` override when `db-tx-end` is configured to *allow* it, which this harness's config does not. `tx=commit`/`tx=rollback` remain valid, accepted Prefer tokens — a client may send either and it is recognized like any other preference — but neither changes whether the transaction actually commits. Case 1230's note pins this explicitly ("no `Prefer: tx=commit` override path"); cases 1004 and 1021 each send `Prefer: tx=commit` and their writes must still not persist. |
| `db_safe_update_tables` | `["safe_update_items", "safe_delete_items"]` | |
| `jwt_aud` | `nil` | |
| `server_cors_allowed_origins` | `"http://example.com, http://example2.com"` | |
| `server_timing_enabled` | `true` | Most cases assert `Server-Timing` is present; the few needing it absent (1758/1763) run on their own variant instance instead. |
| `server_trace_header` | `"X-Request-Id"` | |
| `log_level` | `:error` | |

### 2.2 Shared "auth" instance

Everything in §2.1, **plus**:

| Key | Value | Notes |
|---|---|---|
| `db_anon_role` | `"postgrest_test_anonymous"` | |
| `db_pre_request` | `"auth.switch_role"` | Runs inside the request's transaction. |
| `jwt_secret` | `"reallyreallyreallyreallyverysafe"` | HS256 secret, matching PostgREST's own `testCfg` default (≥ 32 chars). |

Serves cases whose `schema:` is `"auth"` or `"openapi"`, **or** whose request
path is exactly `/` (the root/OpenAPI document resolves the anonymous role to
filter which routes it lists, even though it never switches the database
role for the request itself).

### 2.3 Per-case variant instances

A small set of cases declare a `config:` block in their YAML that neither
shared instance can honor (e.g. a different `jwt-secret`, a disabled
anonymous role, a non-default `openapi-mode`). Each such case gets its **own**
dedicated server instance, built by layering on top of whichever shared
config it would otherwise use:

1. Start from `base_opts` (§2.1) if the case is not an auth/openapi/root-path
   case per the §2.2 rule, else `auth_opts` (§2.1 + §2.2).
2. Merge in the case's own `config:` block, translated per §2.4 below.
3. Merge in any **hard-coded extra options** for that specific case id (only
   two such overrides exist — see §2.5).

**Every** case that needs a variant instance, its base, its declared
`config:`, and the resulting server option(s):

| Case id | Base | Case's `config:` | Resulting option(s) |
|---|---|---|---|
| 1139 | bulk | `url-use-legacy-target-names: false` | `url_use_legacy_target_names: false` |
| 1467 | auth | `jwt-secret: asymmetric_jwk_public_key` | `jwt_secret: <RS256 public JWK, see §2.6>` |
| 1468 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1469 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1470 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1471 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1472 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1473 | auth | `jwt-aud: youraudience` | `jwt_aud: "youraudience"` |
| 1491 | auth | `db-anon-role: null` | `db_anon_role: nil` (overrides the auth base's anon role — anonymous access is disabled) |
| 1493 | auth | `jwt-secret: null` | `jwt_secret: nil` (overrides the auth base's secret; `db_anon_role` stays set, so auth stays "applicable" and a presented token hits the no-secret path) |
| 1498 | auth | `jwt-role-claim-key: "$.postgrest.a_role"` | `jwt_role_claim_key: "$.postgrest.a_role"` |
| 1499 | auth | `jwt-role-claim-key: "$.customObject.manyRoles[1]"` | `jwt_role_claim_key: "$.customObject.manyRoles[1]"` |
| **1654** | auth (root path) | *(none — hard-coded, see §2.5)* | `db_schemas: ["openapi_no_comment"]` |
| 1677 | auth (root path) | `openapi-mode: ignore-privileges` | `openapi_mode: "ignore-privileges"` |
| 1678 | auth (root path) | `openapi-mode: disabled` | `openapi_mode: "disabled"` |
| 1680 | auth (root path) | `openapi-security-active: true` | `openapi_security_active: true` |
| 1682 | auth (root path) | `db-root-spec: root` | `db_root_spec: "root"` |
| 1703 | bulk | `server-cors-allowed-origins: ""` | `server_cors_allowed_origins: ""` (empty list ⇒ CORS allows any origin) |
| 1742 | auth (root path, `OPTIONS /`) | `server-cors-allowed-origins: ""` | `server_cors_allowed_origins: ""` |
| 1495 | auth | `jwt-secret: asymmetric_jwks_public_key` | `jwt_secret: <RS256 public JWK wrapped as a JWK Set, see §2.6>` |
| 1517 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1518 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1522 | bulk | `client-error-verbosity: minimal` | `client_error_verbosity: "minimal"` |
| 1758 | bulk | `server-timing-enabled: false` | `server_timing_enabled: false` |
| 1763 | bulk | `server-trace-header: ""` | `server_trace_header: ""` (unset — the trace header must NOT be echoed) |
| **1764** | auth (root path) | `log-level: error` | `log_level: :error` **plus** the hard-coded `jwt_secret: nil` from §2.5 (both apply — auth base's `db_anon_role` stays set, so the request still routes through JWT resolution and hits the no-secret 500) |
| 11800 | auth | `jwt-role-claim-key: '$["https://www.example.com/roles"][0].value'` | same key |
| 11802 | auth | `jwt-role-claim-key: "$.myRole"` | same key |
| 11803 | auth | `jwt-role-claim-key: '$.realm_access.roles[?(@ == "postgrest_test_author")]'` | same key |
| 11804 | auth | `jwt-role-claim-key: '$.realm_access.roles[?(@ != "other")]'` | same key |
| 11805 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud"` | both keys |
| 11807 | auth | `jwt-aud: postgrest_test_author`, `jwt-role-claim-key: "$.aud[0]"` | both keys |
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

Two cases need an extra option that is **not** expressed anywhere in their
case YAML's `config:` block — it's a fact about the harness's fixture layout,
not about PostgREST config. (Case 1764 also happens to carry an ordinary
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
4. **Schema → `Accept-Profile`.** A case's `schema:` field is a fixture-set
   label (§7), not itself a header. Derive the request header from it:

   ```elixir
   if schema in [nil, "public", "test"] do
     headers
   else
     Map.put_new(headers, "Accept-Profile", schema)
   end
   ```

   In other words: send **no** `Accept-Profile` header when `schema` is
   absent, `"public"`, or `"test"`; otherwise send
   `Accept-Profile: <schema>`, unless the case already set its own
   `Accept-Profile` in `request.headers` (in which case that value wins —
   don't overwrite it).
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
  or `"null"` — literally zero bytes).

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
  area's `--dump-config`/startup-validation cases): the process's exit code
  (an integer, or the literal string `"nonzero"` meaning "any non-zero
  code"), substrings the dumped config output or stderr must contain, and
  whether re-feeding the dumped config and re-dumping it produces
  byte-identical output. These do not apply to HTTP cases.

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

> **Four areas overflow into a 5-digit band** once their primary 50-wide band
> filled: `operators` (10200+), `mutations` (11400+), `auth` (11800+),
> `content_negotiation` (12400+). These sort *lexically* right after
> unrelated 4-digit ids, so `ls cases/ | sort -n` (numeric sort), never a
> plain `ls`, is required to read ids in area order.

| Area | Id band(s) | Cases | `schema:` label(s) used |
|---|---|---:|---|
| url_grammar | 1000–1035 | 36 | `test`, `multi`, `unicode`, `ordering` |
| operators | 1050–1099, 10200–10236 | 87 | `operators` |
| select | 1100–1149 | 50 | `test` |
| filters | 1150–1199 | 50 | `test` |
| ordering | 1200–1232 | 33 | `ordering`, `test`, `mutations` |
| pagination | 1250–1288 | 39 | `pagination` |
| representations | 1300–1327, 1330–1333 | 32 | `representations`, `rpc` |
| mutations | 1350–1399, 11400–11405, 11407–11415 (no 11406) | 65 | `mutations` |
| rpc | 1400–1443 | 44 | `rpc`, `test` |
| auth | 1450–1499, 11800–11818 | 69 | `auth` |
| errors | 1500–1526 | 27 | `test` |
| headers | 1550–1584 | 35 | `headers`, `test` |
| content_negotiation | 1600–1649, 12400–12401 | 52 | `test` |
| openapi | 1650–1688 | 39 | `test`, `openapi_no_comment` |
| config | 1700–1744 | 45 | `config` |
| observability | 1750–1771 | 22 | `observability` |
| domain_representations | 1800–1836 | 37 | `domain_representations`, `test` |

**Total: 762 cases across 17 areas** (36+87+50+50+33+39+32+65+44+69+27+35+52+39+45+22+37 = 762).

Recover a case's area directly from its own file, no index lookup needed:

```sh
grep -h '^feature:' cases/1800_format_single_domain_column.yaml
# feature: domain_representations/read/format_single_column
```

See [`INDEX.md`](INDEX.md) for label caveats (e.g. `unicode` → `تست`,
`multi` → the `v1`/`v2` pair) and per-area sub-feature breakdowns.
