# CI phase 2 — the PostgREST oracle

**Date:** 2026-08-21
**Status:** approved design, pre-implementation
**Predecessor:** the extraction design's deferred "Phase 2 (credibility centerpiece)"
paragraph (milmazz/bier, `docs/superpowers/specs/2026-08-18-postgrest-conformance-extraction-design.md`).

## Goal

An internal CI job that boots **real PostgREST v16.0** (the exact version in
`PIN`, commit `ac464c368153851fd7746cf761b2ee11d7200e62`) against the suite's
own fixture database and executes **all 762 cases** against it via a runner in
`tools/oracle/`, so the README can truthfully claim every case is
machine-verified against PostgREST itself.

Target is **762/762**. The suite records what PostgREST does, so *any* failure
against the real thing is a suite defect — including the 3 `expect.status_text`
cases (1508/1510/1511, which bier's harness cannot check but this runner can)
and case 1771 (`Server: ^postgrest/…` — a bier divergence, not a PostgREST
one). The runner has **no skip/pending mechanism**: per CONTRIBUTING.md, skip
lists belong to consumers, and this runner is not granted one.

## Non-goals

- The runner is **internal tooling** (`tools/`), not a supported consumer API.
  The repo's data-only promise stands; README/HARNESS.md keep their consumer
  contract unchanged (apart from the eventual "machine-verified" claim).
- The runner **never edits cases**. Failures become findings routed through
  CONTRIBUTING.md's channels (`bier-spec-audit` re-verification, delta-channel
  fixture work) with human review.
- No rewrite of `tools/validate.py` or `tools/regen_area_schemas.exs`. Both
  stay as-is; Go rewrites are possible *separate* follow-up projects (the
  generator's byte-diffed output contract and validate.py's jsonschema
  fidelity each carry their own migration risk, and neither blocks the oracle).

## Decisions already made (with rationale)

| Decision | Choice | Why |
|---|---|---|
| Runner language | **Go** (new module under `tools/oracle/`) | Owner wants Go experience; every hard requirement is met: `http.Response.Status` preserves the wire reason phrase (the 3 `status_text` cases), `url.URL.Opaque` sends raw request targets, `Transport.DisableCompression` keeps `Content-Length` intact, stdlib `crypto/hmac` covers HS256 (the only `sign_with` in the suite). Greenfield tool ⇒ no migration risk. |
| PostgREST distribution | **Official release binaries**, checksum-pinned | `postgrest-v16.0-linux-static-x86-64.tar.xz` in CI, `postgrest-v16.0-macos-aarch64.tar.xz` locally — same runner code both places. The dozens of short-lived variant boots and 38 CLI invocations are plain subprocesses. (Verified: the GitHub release ships both; the `postgrest/postgrest:v16.0` Docker image also exists as a fallback, `Cmd: ["/bin/postgrest"]`, no entrypoint.) Docker is used only for the CI database. |
| Database | **PostgreSQL 17 only** (`postgis/postgis:17-3.5` in CI) | Matches the suite's development target; the existing `fixtures` CI job already covers loading on 15/16/17. PostgREST v16.0 supports PG ≥ 14 (verified in `Config/PgVersion.hs`), so no conflict. Widen later if wanted. |
| CI landing | **Land only when green** | Iterate locally against a scratch DB until 762/762 (or until remaining failures are reviewed findings); the workflow enters CI already blocking. The README claim follows the first green run, not the other way around. |
| Instance model | **HARNESS-faithful shared instances + documented deltas** (approach A) | The runner is HARNESS.md's first real consumer; configuring per §2.1/§2.2 and deviating only where PostgREST lacks the feature makes every forced deviation a finding about the contract's portability. Upstream's own config layout (`baseCfg` + inline overrides, verified in `test/spec/SpecHelper.hs`) is the diagnostic fallback: when a case fails under A, "does it pass under upstream's config?" separates HARNESS defects from case defects. |

## Verified facts this design rests on

All verified against the pinned tag's sources / live registries during design
(2026-08-21):

1. `postgrest/postgrest:v16.0` exists on Docker Hub (linux/amd64 + arm64);
   GitHub release `v16.0` ships static linux x86-64, linux aarch64, macOS
   x86-64/aarch64 tarballs; git tag `v16.0` = the PIN commit.
2. PostgREST v16.0: minimum PG 14, CI-tested through PG 19.
3. `db-tx-end=rollback` (no `-allow-override`): a client `Prefer: tx=commit`
   is **recognized but dropped** — never honored, never echoed in
   `Preference-Applied` (`ApiRequest/Preferences.hs`, `RollbackSpec.forced`).
   Cases 1004/1021 (the only two sending `tx=`) assert nothing about
   persistence or `Preference-Applied`, so **running every HTTP instance under
   `rollback` makes the whole run order-independent**.
4. Config: every key has a `PGRST_*` env equivalent; `db-schemas` is
   comma-split + whitespace-stripped with **no quoting**, so
   `SPECIAL "@/\#~_-` and `تست` pass through env values verbatim (schema names
   containing a comma are inexpressible — none exist here).
5. PostgREST has **no schema aliases, no per-schema `db-max-rows`, no
   safe-update table list** — confirming five HARNESS §2.1 keys as bier-only:
   `db_schema_aliases`, `db_profile_default`, `db_profile_schemas`,
   `db_max_rows_by_schema`, `db_safe_update_tables`. Handled runner-side (§
   "Instance model" and "Routing" below).
6. `--dump-config` needs no database (in-DB config read fails soft); admin
   server exposes `/ready` (503 until schema cache loaded) — the readiness
   probe; `postgrest --ready` also exists.
7. Custom `status_text` reason phrases go on the wire via
   `HTTP.mkStatus` (`Error.hs`) — HTTP/1.1 only, so the runner speaks plain
   HTTP/1.1 (Go's default for `http://`).
8. Upstream's spec suite runs one `baseCfg` (db-schemas `["test"]`, anon role,
   test secret, pre-request `test.switch_role`, `rollback-allow-override`)
   with per-module inline overrides — the same shape as the cases' `config:`
   blocks.
9. Case inventory (mechanically derived): 762 cases, 15 distinct `schema:`
   labels; 116 cases carry `config:` (78 HTTP across 38 distinct combos + 38
   CLI); exactly 2 cases send `Prefer: tx=`; exactly 3 use `status_text`;
   exactly 1 uses `dump_reparse_stable` (1726) and 1 `headers_no_blank`
   (1573); `headers_absent_in_value` and `body_json` appear in HARNESS.md but
   in **zero** cases; every case is a single request (no setup/teardown except
   `config.preconditions_sql` on CLI cases 1724/1725/1744).

## Architecture

```
tools/oracle/                  Go module (go.mod lives here; repo root stays clean)
  cmd/oracle/main.go           CLI entry: run all / --cases / --areas / --report
  internal/cases/              YAML case loader + expect-key registry
  internal/route/              instance routing (labels, config-satisfaction)
  internal/instance/           PostgREST process manager (config gen, boot, /ready, teardown)
  internal/httpexec/           request builder (raw paths, profile injection, JWT, bodies)
  internal/cliexec/            CLI-case executor (--dump-config / --example / file arg)
  internal/assert/             all HARNESS §4 assertion semantics
  internal/report/             per-case results, JSON + human summary
  internal/db/                 scratch-DB bootstrap (fixture chain via psql), teardown
  bin.sha256                   pinned SHA-256 sums for the four release tarballs
```

Everything below the CLI is a library so a later `validate.py`-in-Go (if ever)
can reuse the case loader and assertions — but that reuse is not designed for
now.

### PostgREST binary acquisition

A `fetch` subcommand (or make target) downloads the release tarball matching
`PIN`'s version for the host platform from
`github.com/PostgREST/postgrest/releases/download/v16.0/…`, verifies it
against `tools/oracle/bin.sha256` (sums recorded for linux-static-x86-64,
linux-static-aarch64, macos-x86-64, macos-aarch64), and unpacks to a cache
dir. `POSTGREST_BIN` env overrides for a pre-installed binary. The version is
read from `PIN`, not hard-coded, so a future re-pin fails loudly on a missing
checksum instead of silently testing the wrong version.

### Instance model

All HTTP instances run **`db-tx-end=rollback`** (HARNESS §2.1; fact 3 makes
the run order-independent) and get an admin server on a second port for
`/ready` polling. Config is delivered as a generated config file per instance
(env only for the CLI cases, which test env handling themselves). Ports are
allocated dynamically from the ephemeral range.

**Long-lived (booted once per run):**

| Instance | Config | Serves |
|---|---|---|
| `bulk` | HARNESS §2.1 translated to PostgREST keys, minus the five bier-only keys. `db-schemas` **verbatim** from §2.1 (test first, incl. `openapi`, `v1`, `v2`, `SPECIAL "@/\#~_-`, `تست`), `db-extra-search-path=public`, `db-plan-enabled=true`, `db-max-rows` unset, CORS origins + `server-timing-enabled=true` + `server-trace-header=X-Request-Id`, `log-level=error`, no anon role / secret / pre-request. | every case not routed elsewhere |
| `auth` | bulk + `db-anon-role=postgrest_test_anonymous`, `jwt-secret=reallyreallyreallyreallyverysafe`, `db-pre-request=auth.switch_role` (§2.2) | `schema: auth`/`openapi` cases and path-`/` cases |
| `multi` | bulk base but `db-schemas = v1,v2,SPECIAL "@/\#~_-` **exactly** (case 1560 asserts that exposed-schemas hint verbatim; upstream `MultipleSchemaSpec` shape) | the 14 `schema: multi` cases + headers-area profile cases 1557, 1558, 1559, 1560, 1574, 1583 |
| `unicode` | bulk base but `db-schemas = تست` exactly (upstream `UnicodeSpec` shape) | cases 1003, 1004, 1021 |

**Short-lived variants (booted on demand, one at a time, shared across cases
with identical config):** any HTTP case whose translated `config:` block is
not already satisfied by its routed instance's effective config gets a variant
built as *base-instance config ⊕ translated case config*, generalizing HARNESS
§2.3 instead of hard-coding its table:

- **Translation** per §2.4: kebab-case key → PostgREST key unchanged (they
  *are* PostgREST's names); scalar `db-schemas` stays a one-element list;
  `null` clears the key from the config file entirely (1474, 1491, 1492,
  1493); JWK sentinels `asymmetric_jwk_public_key` /
  `asymmetric_jwks_public_key` expand to §2.6's literal JWK / JWK-Set values.
- **Satisfaction** = every translated key/value equals the routed instance's
  effective value. So e.g. the 12 observability cases declaring
  `server-timing-enabled: true` run on `bulk`; the 8 `db-aggregates-enabled`
  select cases, the jwt-aud family, `client-error-verbosity: minimal`, the
  log-level family etc. become variants. Variants are keyed by
  `(base, canonical-config)` so the 11 `jwt-aud: youraudience` cases share one
  boot.
- **Hard-coded extras** per §2.5: 1654 → `db-schemas = openapi_no_comment`;
  1764 → clear `jwt-secret` (its own `log-level: error` block still merges
  normally).
- **safeupdate variant** for cases 1387–1389: bulk config + the `safeupdate`
  extension loaded on the serving connections (first choice:
  `db-uri` with `options=-csession_preload_libraries=safeupdate`; fallback: a
  `db-pre-request` that executes `LOAD 'safeupdate'`, upstream's own
  `test.load_safeupdate` pattern). bier emulated this with its
  `db_safe_update_tables` option; real PostgREST needs the real extension —
  the `.so` must be installed in the database server (see "Database" below).

**Diagnostic cross-check:** the computed variant set is compared against
HARNESS §2.3's table at run start. The inventory already suggests they differ
(§2.3 lists ~33 cases; 78 HTTP cases carry config blocks — e.g. 1475–1477's
`db-pre-request: test.switch_role` appears in no §2.3 row). Every mismatch is
reported as a HARNESS.md finding, not silently absorbed.

### Routing

1. `request.kind: cli` → CLI executor (below).
2. Case id ∈ {1387, 1388, 1389} → safeupdate variant.
3. `schema: multi`, or id ∈ {1557, 1558, 1559, 1560, 1574, 1583} → `multi`.
4. `schema: unicode` → `unicode`.
5. Else HARNESS §2.2's rule: `schema:` ∈ {`auth`, `openapi`} or path is `/` →
   `auth` base; otherwise `bulk` base. (`openapi_no_comment` = case 1654,
   auth-based per §2.5.)
6. Then the config-satisfaction check decides shared instance vs variant.

**Label → wire resolution** (replacing bier's server-side glue, which real
PostgREST lacks):

- `test` / `public` / absent → no `Accept-Profile` (HARNESS §3 step 4).
- `multi`, `unicode` → **no** `Accept-Profile` injected; the instance's
  `db-schemas` ordering supplies the default profile (`v1` / `تست`), exactly
  as upstream's specs ran. Explicit profile headers in `request.headers` pass
  through untouched. This deviates from a literal reading of §3 step 4
  (bier resolves these labels via a hard-coded `@profile_aliases` allowlist in
  *production* code — COVERAGE.md open finding #1); the deviation is recorded
  as a HARNESS portability finding.
- Every other label → `Accept-Profile: <label>` with put-new semantics: an
  explicit `Accept-Profile` in `request.headers` wins; an explicit
  `Content-Profile` does **not** suppress injection (INDEX.md label caveats —
  cases 1011/1012/1559 depend on this asymmetry).

### Request execution (Go specifics are requirements, not suggestions)

- **Raw paths:** `request.path` goes on the wire byte-preserving via
  `url.URL{Opaque: …}`; the runner percent-encodes only bytes the transport
  cannot carry (per HARNESS §3 step 2) and never re-encodes existing `%XX`,
  `+`, or reserved delimiters.
- **HTTP/1.1, no compression:** plain `http.Transport` with
  `DisableCompression: true`, no TLS, no `Accept-Encoding` sent, keep-alives
  fine. Reason phrase read from `http.Response.Status` (strip the leading
  `<code> `).
- **Headers:** sent verbatim (Go canonicalizes names' case only — HTTP-legal);
  UTF-8 header values (`Accept-Profile: تست`) are written as raw bytes.
- **Bodies:** `body_raw` verbatim; `body_json` always JSON-encoded; `body`
  JSON-encoded unless already a string. No key ⇒ no body.
- **JWT:** `request.jwt` with `sign_with: hs256_test_secret` → stdlib
  HS256 over the payload **as-is** (no claim validation/coercion — cases
  deliberately carry invalid claim types); skipped when the case sets its own
  `Authorization` header.
- **JSON numbers:** all decoding uses `json.Decoder.UseNumber()`; deep-equal
  compares `json.Number` values numerically (so YAML's `2` matches JSON's
  `2` and precision is never lost through float64).

### Assertions

Implements every HARNESS §4 key; an unrecognized `expect:` key **fails the
case loudly**. Notables:

- `headers`: named-header-only, case-insensitive names, repeated headers
  comma-folded — except `Set-Cookie`, folded with `"\n"` (case 1568).
- `body_exact`: decoded deep-equal; expected `null`/absent ⇒ the body must be
  **zero bytes**.
- `body_raw`: byte-for-byte. `body_contains`: raw substring(s).
- `body_jsonpath`: `equals` / `present`|`exists` / `absent` predicates via a
  Go JSONPath library (chosen after enumerating the path syntax the 48 cases
  actually use; `ohler55/ojg` or RFC-9535 `theory/jsonpath` are the
  candidates).
- `status_text`: exact reason-phrase match (1508/1510/1511) — the assertion
  bier cannot make.
- `headers_no_blank` (1573), `headers_present`, `headers_absent`,
  `headers_match` per §4. `headers_absent_in_value` and `body_json` are
  implemented (HARNESS defines them) but currently exercised by zero cases.

### CLI cases (38, all `config` area)

In scope — they invoke the **same pinned binary** as subprocesses:

- `--dump-config` (36), `--example` (1727), or a positional config-file arg
  (1719's is a deliberately nonexistent path).
- Config from `config.env` (exact `PGRST_*` map), `config.file` (temp file),
  or both (1720: env wins).
- Assertions: `exit_code` (integer, or literal string `"nonzero"` = any ≠ 0),
  `dump_contains`, `stderr_contains`, `dump_reparse_stable` (1726: dump →
  re-feed as config file → re-dump → byte-identical).
- **1724/1725/1744** additionally run `config.preconditions_sql` against the
  scratch DB (superuser), then invoke with a db-uri authenticating as
  `db_config_authenticator`. The preconditions create that role without a
  password, so the runner sets one itself post-preconditions (environment
  glue, same spirit as §2.5's `variant_extra_opts`) and **drops the role** in
  cleanup — roles are cluster-wide and the local cluster is shared.
- 35 of the 38 touch no database at all.

### Database

**Local:** a scratch database `postgrest_conf_oracle` on the local PG
(never `bier_test`), built by the fixture chain exactly per fixtures/README
(`01_roles.sql` against the maintenance DB; `CREATE DATABASE … TEMPLATE
template0 LC_COLLATE 'C'`; `02`–`07` with `PGTZ=UTC`, `ON_ERROR_STOP`),
dropped after the run (plus the `db_config_authenticator` role). The
`postgrest_test_*` roles from `01_roles.sql` are idempotent and shared with
bier's own use — left in place.

**CI:** `postgis/postgis:17-3.5` container (same pin as the existing
`freshness` job) with **pg-safeupdate installed into it** — expected via the
PGDG apt package (`postgresql-17-pg-safeupdate`); if the package doesn't
exist, compile from source inside the container (it is a single C file). This
is the design's main unverified dependency — **verify first during
implementation** (milestone 0 risk item).

### Reporting & failure routing

- Per-case: pass/fail, and on failure the full expected-vs-actual diff
  (status line, offending headers, body diff) plus the case's `source:` URL.
- Outputs: human summary (per-area counts) + machine JSON (one record per
  case); CI uploads the JSON as an artifact; any failure ⇒ non-zero exit.
- The report's failure epilogue states the repo rule: failures are suite
  defects; route via CONTRIBUTING.md (`bier-spec-audit` for suspect
  expectations, delta-channel for fixture gaps) with human review. The runner
  never writes to `cases/`, `spec/`, or `fixtures/`.

### CI workflow (added last, only when locally green)

`.github/workflows/oracle.yml`, separate from `validate.yml`:

1. Start the PG 17 + PostGIS container, install pg-safeupdate, load the
   fixture chain (same psql pattern as the `fixtures` job).
2. `setup-go`, build `tools/oracle`, fetch + checksum-verify the PostgREST
   linux-static-x86-64 tarball.
3. Run the oracle: CLI cases, then HTTP cases (long-lived instances booted
   once; variants sequential). Upload the JSON report artifact.

Pushing this workflow (and the subsequent README "machine-verified" claim) to
main happens **only with explicit owner confirmation at that step**, per the
project's standing constraint.

### Testing the runner itself

The runner is internal tooling whose ultimate test *is* the 762-case run, but
its subtle pieces get real Go unit tests (TDD during implementation):
assertion semantics (Set-Cookie fold, zero-byte body, `json.Number`
deep-equal), config translation + satisfaction (including null-clears and JWK
sentinels), routing (label table, §2.2 rule, §2.3 cross-check), JWT minting
against the known-answer token in HARNESS §3.1, and the CLI `"nonzero"` exit
contract. One additional guard: a **YAML cross-check** — every case file's
parse under `yaml.v3` is compared against a `tools/validate.py`-style pyyaml
parse (one-time script) to catch YAML 1.1-vs-1.2 scalar divergences before
they masquerade as PostgREST failures.

## Expected findings (known before the first run)

These are the places the design already knows HARNESS.md or the suite may be
non-portable; the first run adjudicates each, and each lands as a reviewed
finding, not a silent workaround:

1. §2.1's five bier-only config keys (fact 5) — HARNESS presents them in the
   shared-instance option table without flagging that no PostgREST equivalent
   exists.
2. §2.3's variant table vs the computed variant set (e.g. 1475–1477's
   `db-pre-request: test.switch_role`; whether `test.switch_role` even exists
   in the fixture chain).
3. `bulk`'s §2.1 `db-schemas` includes `openapi`, a schema INDEX.md says does
   not exist on disk; also whether an `observability` schema mirror exists.
4. The `multi` label's resolution living in bier production code
   (COVERAGE.md open finding #1) — the oracle's instance-based resolution is
   the portable statement of what those cases actually need.
5. Error hints/bodies that enumerate exposed schemas could differ under
   `bulk`'s wide schema list vs upstream's `["test"]`.
6. `db-tx-end=rollback` (HARNESS) vs upstream's `rollback-allow-override` —
   fact 3 predicts no observable difference across all 762; the run confirms.

## Milestones

0. **Risk retirement (spike-grade):** pg-safeupdate availability for the
   PG 17 container; binary tarball checksums recorded; YAML cross-check run.
1. Go module scaffold; case loader + expect-key registry; loader round-trips
   all 762 files.
2. CLI executor — the 35 no-DB CLI cases green locally against the pinned
   macOS binary.
3. Scratch-DB bootstrap + instance manager — `bulk`/`auth` boot, `/ready`,
   teardown; first HTTP areas green.
4. Full routing: `multi`/`unicode` instances, variants, JWK sentinels,
   hard-coded extras, safeupdate variant, db-config CLI cases.
5. Full local run; triage every failure into pass-after-fix (via reviewed
   suite changes through CONTRIBUTING channels) or documented finding.
6. `oracle.yml` + README claim — pushed only with explicit confirmation,
   after 762/762 locally.

## Open questions deferred to implementation

- Exact JSONPath library choice (after enumerating the 48 cases' path
  syntax).
- Whether the safeupdate variant uses `session_preload_libraries` in `db-uri`
  or a `LOAD`-ing pre-request function (whichever proves reliable against the
  container).
- Port-allocation details and whether any variant batching beyond
  config-keyed sharing is worth it (sequential is the baseline; 762/762
  correctness beats runtime).
