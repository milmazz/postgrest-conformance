# Task 16 triage: first full 762-case oracle run

> **Provenance:** this is the triage of the runner's first full 762-case run
> (751/762). Commit `a5f4670`'s message references this document at its
> pre-commit workspace path
> (`.superpowers/sdd/2026-08-21-postgrest-oracle/task-16-triage.md`); this
> committed copy, under `docs/superpowers/notes/`, supersedes that path.

Date: 2026-08-22
Binary: PostgREST v16.0 (commit `ac464c368153851fd7746cf761b2ee11d7200e62`, per `PIN`)
DB: `postgrest_conf_oracle` on `oracle-pg` (PG17 + PostGIS 3.5 + pg_safeupdate, port 6432), rebuilt fresh via `db-teardown && db-setup` before the first run.

## Final result

```
auth 69/69
config 45/45
content_negotiation 52/52
domain_representations 37/37
errors 27/27
filters 48/50
headers 34/35
mutations 61/65
observability 22/22
openapi 39/39
operators 87/87
ordering 31/33
pagination 39/39
representations 32/32
rpc 44/44
select 48/50
url_grammar 36/36
TOTAL 751/762
```

Full JSON: `task-16-report.json` (copy of `tools/oracle/report.json` from the final run). 11 case failures remain, all suite findings (group (b)) — none are runner bugs or environment issues. 30 `route.CrossCheckHarness` findings also remain (a separate, non-failing routing/documentation cross-check — see the dedicated section below).

## Timeline

1. **First full run** (fresh fixture DB): **720/762**, 42 failures.
2. Triaged all 42 failures by hand (case YAML inspection + manual `curl` against hand-booted instances built from the exact same `PGRST_*` config the runner uses, including one probe with `PGRST_DB_TX_END=commit` to make a write's target table directly observable).
3. Found and fixed **3 runner bugs** in `tools/oracle` (see "Runner fixes" below), committing each separately, re-running the affected slice after the first two, then a **full re-run** after the third (its blast radius — every auto-injected write case — couldn't be bounded to a slice).
4. **Second full run**: **751/762**, 11 failures — all confirmed, by hand, to be suite defects (group (b)), not runner bugs.

## Runner fixes (group (a))

Fixes 1 and 2 landed together in one commit (`3baafff`, both touch
`internal/assert/assert.go`); fix 3 landed separately (`c61c011`, touches
`internal/httpexec/exec.go`) since its blast radius required a full re-run
rather than a bounded slice. Commit messages have full detail and evidence.

### Fix 1 — `body_exact`/`body_json` empty-string sentinel

`internal/assert/assert.go`'s `checkBodyExact` only treated a YAML `null` expected value as "expect a literally empty body". 27 case files instead write `body_exact: ""` for the same intent (e.g. `representations/post/return-minimal`), which the runner tried to JSON-decode against the (correctly empty) actual body, always failing with `"body_exact: body is not valid JSON: EOF (body: )"`.

Confirmed against bier's actual reference assertion code (`test/support/conformance_assertions.ex`, fetched live from `github.com/milmazz/bier`):

```elixir
defp check(key, expected, resp)
     when key in ["body_exact", "body_json"] and expected in [nil, ""] do
  assert resp.body == "", "#{key} expected empty body, got: #{inspect(resp.body)}"
end
```

`expected in [nil, ""]` — the reference implementation this suite formalizes treats `""` identically to `nil`. Fixed `checkBodyExact` to do the same; added two regression tests.

Cases fixed (27, all now pass): 1277, 1284, 1302, 1303, 1304, 1305, 1306, 1308, 1309, 1311, 1313, 1314, 1315, 1317, 1318, 1319, 1321, 1322, 1324, 1332, 1333, 1409, 1425, 1681, 1823, 1824, 1828.

### Fix 2 — `headers_present`/`headers_absent` must not synthesize Content-Length

`foldedHeader`'s Content-Length synthesis (from `HTTPResponse.ContentLength`, added for *value* comparisons like `headers: {Content-Length: "5"}`) was also feeding `checkHeadersPresent`/`checkHeadersAbsent`. Go's `http.Response.ContentLength` is `0` (not `-1`/"unknown") for any bodyless response — including a genuine 204 that carries **no** literal `Content-Length` header on the wire — so `headers_absent: [Content-Length]` false-positived on every such response.

Verified with `curl -v` against a manually booted instance (case 1311's `PATCH /items?id=eq.2`, `Accept-Profile: representations`): the wire response has no `Content-Length` header at all, yet the runner reported `headers_absent[Content-Length]: present with value "0"`.

Added `literalHeader`, a presence-only lookup with no synthesis, used by `checkHeadersPresent`/`checkHeadersAbsent`; `checkHeaders`/`checkHeadersMatch` keep the synthesizing `foldedHeader` unchanged (that behavior is real and tested — see `TestCheckHTTPContentLengthSynthesizedFromField`). Added two regression tests.

Cases fixed (5, all now pass): 1362, 1365, 1369, 1568, 11400.

### Fix 3 — write methods must use `Content-Profile`, not `Accept-Profile`

The most consequential fix. `httpexec.BuildSpec` always injected a case's `schema:` label as `Accept-Profile`, regardless of HTTP method. Per PostgREST's own docs (`docs/references/api/schemas.rst`, fetched live): GET/HEAD select the schema with `Accept-Profile`; **POST, PATCH, PUT and DELETE — including a POST to `/rpc/<fn>` — select it with `Content-Profile`**. Sending `Accept-Profile` on a write is not an error: PostgREST silently ignores it and resolves the request against the *default* schema instead.

This went undetected across most of the suite because area schemas are largely auto-updatable *views* mirroring the same underlying `test.*` tables (`fixtures/06_area_schemas.sql`), so a write silently misrouted to `test` usually lands on the identical row anyway (same physical table). It surfaced unambiguously on case 1755 (`POST /rpc/ret_point_overloaded`, `schema: observability`): that function exists only in the `observability` schema, so the wrongly-scoped write 404'd outright with `Could not find the function test.ret_point_overloaded(x, y)`.

Confirmed definitively with a decisive probe: booted an instance with `PGRST_DB_TX_END=commit` (so a write's target becomes directly observable afterward — the shared/normal instances roll every transaction back) and sent `POST /loc_test` with `Accept-Profile: headers`. The row landed in `test.loc_test`, **not** `headers.loc_test` (a genuinely separate, non-mirrored table, confirmed via `pg_class`/`\d`) — while the same request with `Content-Profile: headers` correctly landed in `headers.loc_test`. Probe rows deleted afterward; no fixture data was altered.

The suite's own case 1011 already states the intended behavior directly in its `notes:` field: *"Write methods use Content-Profile (not Accept-Profile) for schema selection."*

Fixed by making the injected header method-aware (`profileHeaderName`); added four regression tests (one per write method, one per read method, one for put-new precedence).

**Impact of this fix, measured by the second full run vs. the first:**
- `representations` 13/32 → 32/32 (the body_exact fix already covered 19 of these; the remaining gains elsewhere are this fix)
- `mutations` 59/65 → 61/65 (also fixed case 1366, `mutations/update/columns-param`: `mutations.articles` turned out to be a genuinely separate, independently-seeded table from `test.articles` — not a mirror view like `mutations.tasks`/`users`/`users_tasks` — so the wrong-schema write was landing on `test.articles`'s pre-existing `owner='diogo'` row instead of `mutations.articles`'s own `owner` default)
- `headers` — case 1573 now correctly targets `headers.loc_test` (does not change its remaining failure, see below)
- **Newly surfaced 3 findings** (1360, 1368, 1373 — see group (b) below): now-correct `Content-Profile: mutations` routing changed a not-found error's schema qualification from `test.<name>` to `mutations.<name>`, which is what real PostgREST actually does — these 3 cases' own recorded `expect:` values are a stale artifact from their upstream single-schema citation, not updated for this suite's own schema-label convention.

Because this fix's correctness affects every auto-injected write case in the suite, a full 762-case re-run was done afterward rather than a bounded slice.

## Findings (group (b)) — 11 remaining failures, 3 root causes

### Finding 1 — area-mirror schema duplication causes false PGRST201 ambiguous-embed errors (7 cases)

**Cases:** 1117 (`select/embed/many-to-many`), 1125 (`select/embed/ambiguous-multiple-choices`), 1198, 1199 (`filters/embed/null_filtering`), 1213 (`ordering/embed/two_levels`), 1222 (`ordering/embed/by_nested_related`), 11415 (`mutations/update/resource-embedding-m2m-with-parent`).

**Observed vs. expected:** each case requests an embed (e.g. `GET /tasks?select=id,users(id)`) against the default `test` schema and expects either a resolved embed (1117, 1198, 1199, 1213, 1222, 11415) or a specific 3-way ambiguity (1125). Real PostgREST instead returns `PGRST201` reporting the *same* `users_tasks`-derived relationship duplicated **8 times** (1125: the `sites`/`big_projects` relationship set inflated similarly, from 3 expected candidates to many more).

**Root-cause hypothesis:** `fixtures/06_area_schemas.sql` mirrors the entire `test` schema into 7 other schemas as plain views (confirmed via `pg_class`: `tasks`/`users`/`users_tasks` exist as `relkind='v'` views in `config`, `domain_representations`, `mutations`, `operators`, `ordering`, `pagination`, `representations`, plus the real `relkind='r'` tables in `test` — 8 schemas total, matching the 8 duplicate entries exactly). PostgREST's schema cache resolves an *unqualified* embed target name (`users`) by scanning relationships across every schema in `db-schemas` for a same-named source/target pair, not just the request's active schema — with 8 schemas each independently offering a same-shaped `tasks`↔`users` relationship (because they're all simple mirrors of the identical base tables), PostgREST reports all 8 as ambiguous candidates. Reproduced identically with a manual `curl` against a hand-booted instance using the exact `bulk` config (see fix 3's commit for the boot recipe) — this is not affected by any of the three runner fixes above (these are all `schema: test`, no profile header involved at all).

This is a real conflict between HARNESS.md §2.1's stated `db-schemas` list (needed so every area's `Accept-Profile` resolves to *something*) and PostgREST's actual embed-ambiguity resolution, which is not scoped to the active profile the way table/column resolution is. Root cause lives in `fixtures/06_area_schemas.sql`'s mirror strategy, which is fixtures/-owned, delta-channel work per `CONTRIBUTING.md` — not something this task's mandate permits touching.

**Source anchors:** QuerySpec.hs#L679 (1117), EmbedDisambiguationSpec.hs#L14 (1125), RelatedQueriesSpec.hs#L200/#L209 (1198/1199), QuerySpec.hs#L1164 (1213), RelatedQueriesSpec.hs#L80 (1222), UpdateSpec.hs#L539 (11415).

### Finding 2 — stale `test.`-qualified not-found-error citation, not updated for the mutations profile (3 cases)

**Cases:** 1360 (`mutations/columns-param/table-not-found-precedence`), 1368 (`mutations/update/error/table-not-found`), 1373 (`mutations/delete/error/table-not-found`).

**Observed vs. expected:** all three request a deliberately-nonexistent table (`garlic`, `fake`, `foozle`) under `schema: mutations`. Real PostgREST correctly reports `Could not find the table 'mutations.<name>' in the schema cache` (now that `Content-Profile: mutations` is correctly sent per fix 3). Each case's recorded `expect.body_exact` instead says `'test.<name>'`.

**Root-cause hypothesis:** these three cases were ported from PostgREST's own upstream single-schema test suite (`InsertSpec.hs#L477`, `UpdateSpec.hs#L15`, `DeleteSpec.hs#L113`), where the only exposed schema is literally named `test`, so the upstream error message naturally says `test.<name>`. When adapted into this suite's `mutations` area (assigning `schema: mutations` to route the request through the mutations profile, per this suite's own general convention of exercising the same behavior through different area labels), the citation's error-message string was carried over verbatim instead of being updated to the schema label the case itself now runs under. This is a citation/expectation staleness bug in the three case files, exposed — not created — by fix 3: before that fix, the runner's own bug (always sending `Accept-Profile`, silently ignored on a write) coincidentally routed these same three requests to the default `test` schema, so the stale `test.`-qualified expectation happened to match by accident. It no longer does, correctly.

**Source anchors:** InsertSpec.hs#L477 (1360), UpdateSpec.hs#L15 (1368), DeleteSpec.hs#L113 (1373).

### Finding 3 — blank `X-Request-Id` trace-header echo conflicts with a no-blank-headers assertion (1 case)

**Case:** 1573 (`headers/guc/no-blank-headers`).

**Observed vs. expected:** `POST /loc_test` (now correctly routed to `headers.loc_test` via `Content-Profile: headers`, fix 3) asserts `headers_no_blank: true` — no response header may have a blank value. Real PostgREST returns a literal `X-Request-Id: ` (present, empty value), failing the assertion.

**Root-cause hypothesis:** HARNESS.md §2.1 configures `server_trace_header: "X-Request-Id"` as a **fixed value on the shared bulk/auth instance**, used across essentially every case in the suite. When a client doesn't send a matching `X-Request-Id` request header (as this case's request does not), PostgREST still echoes the configured trace-header name with an empty value on every response. Confirmed with `curl -v`: `< X-Request-Id: ` on the wire, with nothing after the colon. This is real, reproducible PostgREST behavior, not a runner bug — the case's `headers_no_blank: true` assertion is simply incompatible with running on an instance that has `server-trace-header` configured but received no matching request header. (Contrast with cases 1760–1763, which specifically test the trace-header feature and are careful to always send a matching `X-Request-Id` request header and their own dedicated `config:` block.) The other 3 cases that exercise `headers_no_blank` implicitly via other assertions were not affected because they don't run on the trace-header-configured shared instance without also sending the header, or don't assert `headers_no_blank` at all.

**Source anchor:** RpcSpec.hs#L1103.

## `route.CrossCheckHarness` output — HARNESS §2.3 table is an incomplete inventory (30 ids, not a failure)

This is not a case failure — `CrossCheckHarness` runs regardless of pass/fail and flags any case the runner's own routing (`route.Route`, derived independently from each case's own `config:` block) sends to a dedicated variant instance but that HARNESS.md §2.3's hand-curated 33-id table doesn't list. All 30 were inspected by hand; every one carries an explicit `config:` block whose translated value genuinely differs from its base instance's default (or PostgREST's own default), so the runner's routing is correct in every case — HARNESS §2.3's table is simply missing these 30 entries, not describing something wrong.

Grouped by the differing config key:

- **`db-aggregates-enabled: true`** (8): 1129, 1130, 1131, 1132, 1133, 1147, 1148, 1149 — the `operators`-area aggregate-function cases, none of which HARNESS §2.3 lists at all (the table has no aggregate-related row).
- **`url-use-legacy-target-names: false`** (1): 1140 — a second case sharing the exact overlay HARNESS §2.3 already documents for case 1139, just not cross-referenced.
- **Hardcoded safeupdate ID switch, not the `config:`-translate path** (3): 1387, 1388, 1389 — these get their variant instance via a hardcoded `case c.ID ==` branch in `route.Route` (HARNESS.md documents the safeupdate mechanism elsewhere, but §2.3's table is specifically the `config:`-block-driven variant list, and doesn't cross-reference this separate mechanism).
- **`db-pre-request: test.switch_role`** (3): 1475, 1476, 1477.
- **`jwt-secret` + `jwt-secret-is-base64: true`** (1): 1466 (a binary/base64-encoded HS256 secret, distinct from the two asymmetric-JWK variants HARNESS §2.3 does list at 1467/1495).
- **`jwt-aud: youraudience`, no-JWT / absent / array / null-ignored variants** (4): 1494, 11815, 11816, 11817 — same overlay as the 6 audience-mismatch cases HARNESS §2.3 already lists (1468–1473), but these 4 exercise different edge cases (no token, absent claim, single-match array, null aud) not individually cross-referenced.
- **`jwt-cache-max-entries: 86400` + `server-timing-enabled: false`** (1): 1497.
- **`db-anon-role: null`** (1): 1492 — same overlay as case 1491 (which HARNESS §2.3 does list), exercised with a *valid* token instead of none; shares 1491's instance/GroupKey, so no additional instance boot is involved, just an uncross-referenced case id.
- **`db-max-rows: 2`** (2): 1700, 1701 — a global `db-max-rows` override (distinct from the `db_max_rows_by_schema` cap for the `config` schema that HARNESS §2.1 already documents).
- **`log-level: warn|info|crit`** (3): 1765, 1766, 1767.
- **`jwt-role-claim-key`, various** (3): 11801, 11806, 11808 — additional role-claim-key edge cases alongside the ones HARNESS §2.3 does list (11800, 11802–11805, 11807, 11818).

Total: 8+1+3+3+1+4+1+1+2+3+3 = 30, matching the runner's output exactly.

## Environment issues (group (c))

None. The fixture DB (`postgrest_conf_oracle`) built cleanly on first `db-teardown && db-setup`; the `oracle-pg` container (PG17 + PostGIS 3.5, `pg_safeupdate` compiled from source per the `Makefile`) required no changes. Every instance boot succeeded across both full runs.

## What was NOT touched

Per the task's hard rule, `cases/`, `spec/`, `fixtures/`, `HARNESS.md`, the repo-root `README.md`, and `CONTRIBUTING.md` were read only, never modified. All three fixes above landed exclusively in `tools/oracle/internal/assert/` and `tools/oracle/internal/httpexec/`, each verified against `go build`/`go vet`/`go test ./...` before committing.
