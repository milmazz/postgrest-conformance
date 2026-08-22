# PostgREST Oracle Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go runner in `tools/oracle/` that executes all 762 conformance cases against real PostgREST v16.0 (pinned release binaries) and a PG 17 fixture database, targeting 762/762, plus (gated, last) a CI workflow.

**Architecture:** Long-lived PostgREST instances (`bulk`, `auth`, `multi`, `unicode`) configured per HARNESS.md §2.1/§2.2 via `PGRST_*` env maps, short-lived variant instances for cases whose `config:` block a shared instance can't satisfy, a subprocess executor for the 38 CLI cases, and an assertion engine implementing HARNESS.md §4 exactly. All HTTP instances run `db-tx-end=rollback`, making the run order-independent.

**Tech Stack:** Go ≥ 1.23, sole dependency `gopkg.in/yaml.v3`. External tools at runtime: `psql`, `tar` (xz-capable), `docker` (for the local/CI database).

**Spec:** `docs/superpowers/specs/2026-08-21-postgrest-oracle-ci-design.md` — read it first; every design rationale lives there.

## Global Constraints

- Go module path: `github.com/milmazz/postgrest-conformance/tools/oracle`; `go.mod` lives in `tools/oracle/`; only dependency `gopkg.in/yaml.v3`.
- The runner NEVER writes to `cases/`, `spec/`, `fixtures/`, `HARNESS.md`, or `README.md`. Failures are reported, not fixed.
- Never touch the `bier_test` database. Scratch database name: `postgrest_conf_oracle`. The preferred local DB is the Docker container from `make db-up` (port 6432); the shared cluster is a fallback only.
- PostgREST version comes from `PIN` (currently `postgrest: v16.0`); binaries are verified against `tools/oracle/bin.sha256` before use — a missing checksum is a hard error, never a warning.
- All HTTP instances: `PGRST_DB_TX_END=rollback`, `PGRST_SERVER_HOST=127.0.0.1`, `PGRST_DB_CONFIG=false`.
- `gofmt` clean; `go vet ./...` clean. Tests that need a database and/or binary are guarded by env vars (`ORACLE_TEST_DB_URI`, `ORACLE_TEST_BIN`) and skip when unset — plain `go test ./...` must always pass without external services.
- Commits: conventional messages ending with the two trailers used throughout this repo:

  ```
  Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_017oDh9G278GxhJVuZsvoAVb
  ```
- Pushing to origin, tagging, or adding/altering `.github/workflows/` happens ONLY in Task 17, only with the owner's explicit confirmation at that step.

## Reference values used across tasks

**Shared base configs (`PGRST_*` maps, exclusive of db-uri/ports, which `internal/instance` injects):**

`bulk` (HARNESS §2.1 minus the five bier-only keys):

| Env var | Value |
|---|---|
| `PGRST_DB_SCHEMAS` | `test,operators,ordering,pagination,representations,mutations,rpc,headers,config,openapi,domain_representations,observability,auth,v1,v2,SPECIAL "@/\#~_-,تست` |
| `PGRST_DB_EXTRA_SEARCH_PATH` | `public` |
| `PGRST_DB_POOL` | `10` |
| `PGRST_DB_TX_END` | `rollback` |
| `PGRST_DB_PLAN_ENABLED` | `true` |
| `PGRST_DB_CONFIG` | `false` |
| `PGRST_SERVER_CORS_ALLOWED_ORIGINS` | `http://example.com, http://example2.com` |
| `PGRST_SERVER_TIMING_ENABLED` | `true` |
| `PGRST_SERVER_TRACE_HEADER` | `X-Request-Id` |
| `PGRST_LOG_LEVEL` | `error` |
| `PGRST_SERVER_HOST` | `127.0.0.1` |

`auth` = `bulk` plus: `PGRST_DB_ANON_ROLE=postgrest_test_anonymous`, `PGRST_JWT_SECRET=reallyreallyreallyreallyverysafe`, `PGRST_DB_PRE_REQUEST=auth.switch_role`.

`multi` = `bulk` with `PGRST_DB_SCHEMAS` replaced by `v1,v2,SPECIAL "@/\#~_-`.

`unicode` = `bulk` with `PGRST_DB_SCHEMAS` replaced by `تست`.

**PostgREST defaults for satisfaction checks** (effective value when a key is absent from the base map; from case 1705's dump + v16.0 config docs):

```
PGRST_DB_AGGREGATES_ENABLED=false   PGRST_URL_USE_LEGACY_TARGET_NAMES=true
PGRST_JWT_ROLE_CLAIM_KEY=$.role     PGRST_JWT_AUD=            PGRST_JWT_SECRET=
PGRST_JWT_SECRET_IS_BASE64=false    PGRST_JWT_CACHE_MAX_ENTRIES=10
PGRST_DB_PRE_REQUEST=               PGRST_DB_ANON_ROLE=
PGRST_CLIENT_ERROR_VERBOSITY=verbose  PGRST_OPENAPI_MODE=follow-privileges
PGRST_OPENAPI_SECURITY_ACTIVE=false PGRST_DB_ROOT_SPEC=       PGRST_DB_MAX_ROWS=
```

**HARNESS §2.3 variant-case ids** (for the cross-check diagnostic — 33 ids):
1139, 1467, 1468, 1469, 1470, 1471, 1472, 1473, 1491, 1493, 1495, 1498, 1499, 1517, 1518, 1522, 1654, 1677, 1678, 1680, 1682, 1703, 1742, 1758, 1763, 1764, 11800, 11802, 11803, 11804, 11805, 11807, 11818.

**Asymmetric JWK (HARNESS §2.6), embedded as Go constant `asymJWK`:**

```
{"alg":"RS256","e":"AQAB","key_ops":["verify"],"kty":"RSA","n":"0etQ2Tg187jb04MWfpuogYGV75IFrQQBxQaGH75eq_FpbkyoLcEpRUEWSbECP2eeFya2yZ9vIO5ScD-lPmovePk4Aa4SzZ8jdjhmAbNykleRPCxMg0481kz6PQhnHRUv3nF5WP479CnObJKqTVdEagVL66oxnX9VhZG9IZA7k0Th5PfKQwrKGyUeTGczpOjaPqbxlunP73j9AfnAt4XCS8epa-n3WGz1j-wfpr_ys57Aq-zBCfqP67UYzNpeI1AoXsJhD9xSDOzvJgFRvc3vm2wjAW4LEMwi48rCplamOpZToIHEPIaPzpveYQwDnB1HFTR1ove9bpKJsHmi-e2uzQ","use":"sig"}
```

`asymmetric_jwks_public_key` sentinel expands to `{"keys":[` + asymJWK + `]}`.

---

### Task 1: Module scaffold

**Files:**
- Create: `tools/oracle/go.mod`, `tools/oracle/doc.go`, `tools/oracle/cmd/oracle/main.go`, `tools/oracle/.gitignore`, `tools/oracle/Makefile`

**Interfaces:**
- Produces: the module `github.com/milmazz/postgrest-conformance/tools/oracle`; `cmd/oracle` subcommand dispatch — later tasks register `fetch`, `db-setup`, `db-teardown`, `run`.

- [ ] **Step 1: Create the module and files**

`tools/oracle/go.mod`:

```
module github.com/milmazz/postgrest-conformance/tools/oracle

go 1.23
```

`tools/oracle/doc.go`:

```go
// Package oracle is the suite's internal conformance runner: it executes
// every case in cases/ against real PostgREST (the version pinned in PIN)
// and reports divergences.
//
// "Oracle" is the software-testing term: a test oracle is the authoritative
// mechanism that decides what the correct output is — here, real PostgREST
// itself. It is not a reference to Oracle Database.
//
// This is internal tooling, not a supported consumer API. It never modifies
// cases/, spec/, or fixtures/; failures are findings routed through
// CONTRIBUTING.md.
package oracle
```

`tools/oracle/cmd/oracle/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

// dispatch maps subcommand name to implementation; later tasks add entries
// (fetch, db-setup, db-teardown, run). Each takes the remaining args.
var dispatch = map[string]func(args []string) error{}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	fn, ok := dispatch[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "oracle:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: oracle <fetch|db-setup|db-teardown|run> [flags]`)
}
```

`tools/oracle/.gitignore`:

```
/.cache/
/report.json
```

`tools/oracle/Makefile`:

```make
.PHONY: test build
build:
	go build ./...
test:
	go test ./...
```

- [ ] **Step 2: Verify it builds and vets**

Run: `cd tools/oracle && go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add tools/oracle
git commit -m "feat(oracle): scaffold Go module for the conformance runner"
```

---

### Task 2: Pinned binary fetch (`internal/fetchbin`) + `bin.sha256`

**Files:**
- Create: `tools/oracle/internal/fetchbin/fetchbin.go`, `tools/oracle/internal/fetchbin/fetchbin_test.go`, `tools/oracle/bin.sha256`
- Modify: `tools/oracle/cmd/oracle/main.go` (register `fetch`), `tools/oracle/Makefile` (add `fetch` target)

**Interfaces:**
- Produces: `fetchbin.Version(pinPath string) (string, error)` — parses `PIN`'s `postgrest: v16.0` line, returns `"v16.0"`. `fetchbin.Fetch(repoRoot string) (binPath string, err error)` — downloads the release tarball for `runtime.GOOS/GOARCH`, verifies SHA-256 against `tools/oracle/bin.sha256`, unpacks via `tar` into `tools/oracle/.cache/postgrest-<version>/`, returns the absolute binary path. Idempotent (cache hit skips download but still re-verifies the cached tarball).

- [ ] **Step 1: Record the official checksums**

Download all four assets once and write `tools/oracle/bin.sha256` (format: `<sha256>  <asset-filename>` per line, like `shasum -a 256` output):

```bash
cd /tmp && for a in linux-static-x86-64 linux-static-aarch64 macos-x86-64 macos-aarch64; do
  curl -fsSLO "https://github.com/PostgREST/postgrest/releases/download/v16.0/postgrest-v16.0-$a.tar.xz"
done
shasum -a 256 postgrest-v16.0-*.tar.xz > ~/Dev/elixir-lang/postgrest-conformance/tools/oracle/bin.sha256
cat ~/Dev/elixir-lang/postgrest-conformance/tools/oracle/bin.sha256
```

Expected: four lines. (If the release also publishes a checksums file, cross-verify against it and note that in the commit message.)

- [ ] **Step 2: Write the failing tests**

`fetchbin_test.go`:

```go
package fetchbin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionParsesPIN(t *testing.T) {
	dir := t.TempDir()
	pin := filepath.Join(dir, "PIN")
	os.WriteFile(pin, []byte("postgrest: v16.0\ncommit: ac464c368153851fd7746cf761b2ee11d7200e62\n"), 0o644)
	v, err := Version(pin)
	if err != nil || v != "v16.0" {
		t.Fatalf("got %q, %v; want v16.0", v, err)
	}
}

func TestAssetNameForPlatform(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "postgrest-v16.0-linux-static-x86-64.tar.xz"},
		{"linux", "arm64", "postgrest-v16.0-linux-static-aarch64.tar.xz"},
		{"darwin", "amd64", "postgrest-v16.0-macos-x86-64.tar.xz"},
		{"darwin", "arm64", "postgrest-v16.0-macos-aarch64.tar.xz"},
	} {
		got, err := assetName("v16.0", tc.goos, tc.goarch)
		if err != nil || got != tc.want {
			t.Fatalf("%s/%s: got %q, %v", tc.goos, tc.goarch, got, err)
		}
	}
	if _, err := assetName("v16.0", "plan9", "386"); err == nil {
		t.Fatal("want error for unsupported platform")
	}
}

func TestVerifyChecksumRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.tar.xz")
	os.WriteFile(f, []byte("bytes"), 0o644)
	sums := filepath.Join(dir, "bin.sha256")
	os.WriteFile(sums, []byte("deadbeef  a.tar.xz\n"), 0o644)
	if err := verifyChecksum(f, sums); err == nil {
		t.Fatal("want checksum mismatch error")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/fetchbin/`
Expected: FAIL — `Version`, `assetName`, `verifyChecksum` undefined.

- [ ] **Step 4: Implement**

`fetchbin.go` — implement:

```go
func Version(pinPath string) (string, error)        // scan lines for "postgrest: "; error if absent
func assetName(version, goos, goarch string) (string, error)
func verifyChecksum(tarPath, sumsFile string) error // sha256 of file must equal the entry matching filepath.Base(tarPath); missing entry = error
func Fetch(repoRoot string) (string, error)
```

`Fetch`: version from `<repoRoot>/PIN`; cache dir `<repoRoot>/tools/oracle/.cache/`; download `https://github.com/PostgREST/postgrest/releases/download/<version>/<asset>` with `net/http` (follow redirects, 5-min timeout) to `<cache>/<asset>` if absent; `verifyChecksum`; unpack with `exec.Command("tar", "-xJf", asset, "-C", destDir)` (create dest first); find the `postgrest` executable under dest (walk; it may sit at the top level or in a `bin/` subdir); `os.Chmod(0o755)`; return its path.

Register in `main.go`: `case "fetch":` → resolve repo root (walk up from CWD until a `PIN` file is found), call `Fetch`, print the binary path. Add `fetch:` Makefile target: `go run ./cmd/oracle fetch`.

- [ ] **Step 5: Run unit tests, then a real fetch**

Run: `cd tools/oracle && go test ./internal/fetchbin/ && go run ./cmd/oracle fetch`
Expected: tests PASS; fetch prints a path; then verify the binary runs:
`"$(go run ./cmd/oracle fetch)" --example | head -3` — expected: config template lines including `db-uri`.

- [ ] **Step 6: Commit**

```bash
git add tools/oracle
git commit -m "feat(oracle): pinned PostgREST release-binary fetch with checksum verification"
```

---

### Task 3: JSON value model (`internal/jsonval`): UseNumber decoding + numeric deep-equal

**Files:**
- Create: `tools/oracle/internal/jsonval/jsonval.go`, `tools/oracle/internal/jsonval/jsonval_test.go`

**Interfaces:**
- Produces: `jsonval.DecodeJSON(b []byte) (any, error)` — decodes with `json.Decoder.UseNumber()`, rejects trailing data. `jsonval.DeepEqual(a, b any) bool` — deep equality where any mix of `json.Number`/`int`/`int64`/`uint64`/`float64` compares numerically via `big.Rat` (so YAML's `2` equals JSON's `2.0`, and 19-digit ints never round-trip through float64); maps require identical key sets; slices identical length/order.

- [ ] **Step 1: Write the failing tests**

```go
package jsonval

import "testing"

func TestDecodeUsesNumber(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"big": 9999999999999999999, "f": 1.5}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if _, ok := m["big"].(interface{ String() string }); !ok {
		t.Fatalf("big decoded as %T, want json.Number", m["big"])
	}
}

func TestDeepEqualNumbers(t *testing.T) {
	a, _ := DecodeJSON([]byte(`[{"id": 2}, 2.0, 9999999999999999999, "2"]`))
	// YAML-decoded shape: ints and float64s
	b := []any{map[string]any{"id": 2}, float64(2), mustNum(t, "9999999999999999999"), "2"}
	if !DeepEqual(a, b) {
		t.Fatal("want equal")
	}
	if DeepEqual(float64(2), "2") {
		t.Fatal("number must not equal string")
	}
	if DeepEqual(map[string]any{"a": 1}, map[string]any{"a": 1, "b": 2}) {
		t.Fatal("extra key must not be equal")
	}
	if DeepEqual([]any{1, 2}, []any{2, 1}) {
		t.Fatal("order matters")
	}
	if !DeepEqual(nil, nil) || DeepEqual(nil, "x") {
		t.Fatal("nil handling")
	}
}
```

with helper `func mustNum(t *testing.T, s string) any { v, err := DecodeJSON([]byte(s)); if err != nil { t.Fatal(err) }; return v }`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/jsonval/`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Implement**

```go
package jsonval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

func DecodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}

func numRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(n.String())
		return r, ok
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case uint64:
		return new(big.Rat).SetUint64(n), true
	case float64:
		r, ok := new(big.Rat).SetString(fmt.Sprintf("%v", n))
		return r, ok
	}
	return nil, false
}

func DeepEqual(a, b any) bool {
	if ra, ok := numRat(a); ok {
		rb, ok2 := numRat(b)
		return ok2 && ra.Cmp(rb) == 0
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case string:
		bs, ok := b.(string)
		return ok && av == bs
	case bool:
		bb, ok := b.(bool)
		return ok && av == bb
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bvv, ok := bv[k]
			if !ok || !DeepEqual(v, bvv) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !DeepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return false
}
```

(`float64` via `fmt.Sprintf("%v")` deliberately uses Go's shortest-representation formatting, matching how the YAML value was written; do NOT use `SetFloat64`, which would introduce binary-representation dust like 0.1 ≠ 1/10.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/jsonval/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/jsonval
git commit -m "feat(oracle): numeric-aware JSON decoding and deep equality"
```

---

### Task 4: Mini JSONPath evaluator (`internal/jsonval/jsonpath.go`)

**Files:**
- Create: `tools/oracle/internal/jsonval/jsonpath.go`, `tools/oracle/internal/jsonval/jsonpath_test.go`

**Interfaces:**
- Produces: `jsonval.EvalPath(doc any, path string) (val any, found bool, err error)`. Grammar (the complete syntax used by all 115 paths in the corpus — verified during design): `$` root, then any sequence of `.ident` (ident = run of chars excluding `.` and `[`), `['key']` (single-quoted string key, no escapes needed in corpus), `[123]` (non-negative integer index). Anything else → `err`. Missing key/index or type mismatch → `found=false, err=nil`.

- [ ] **Step 1: Write the failing tests**

```go
package jsonval

import "testing"

func doc(t *testing.T) any {
	v, err := DecodeJSON([]byte(`{
	  "info": {"title": "T"},
	  "paths": {"/child_entities": {"get": {"parameters": [{"$ref": "#/x"}], "responses": {"200": {"description": "OK"}}}}},
	  "security": [{"JWT": []}],
	  "arr": [{"Plan": "p"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEvalPath(t *testing.T) {
	d := doc(t)
	for _, tc := range []struct {
		path  string
		found bool
		want  any
	}{
		{"$.info.title", true, "T"},
		{"$.paths['/child_entities'].get.parameters[0]['$ref']", true, "#/x"},
		{"$.paths['/child_entities'].get.responses['200'].description", true, "OK"},
		{"$.security[0].JWT", true, []any{}},
		{"$.missing", false, nil},
		{"$.info.title.deeper", false, nil},
		{"$.security[5]", false, nil},
	} {
		v, found, err := EvalPath(d, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if found != tc.found {
			t.Fatalf("%s: found=%v want %v", tc.path, found, tc.found)
		}
		if found && !DeepEqual(v, tc.want) {
			t.Fatalf("%s: got %v", tc.path, v)
		}
	}
}

func TestEvalPathRootIndex(t *testing.T) {
	v, _ := DecodeJSON([]byte(`[{"Plan": "p"}]`))
	got, found, err := EvalPath(v, "$[0].Plan")
	if err != nil || !found || got != "p" {
		t.Fatalf("got %v %v %v", got, found, err)
	}
}

func TestEvalPathRejectsUnsupported(t *testing.T) {
	for _, p := range []string{"info.title", "$..x", "$.a[*]", "$.a[?(@.b)]", "$['a\\'b']"} {
		if _, _, err := EvalPath(nil, p); err == nil {
			t.Fatalf("%s: want syntax error", p)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/jsonval/ -run Eval`
Expected: FAIL — `EvalPath` undefined.

- [ ] **Step 3: Implement**

```go
type pathStep struct {
	key     string
	idx     int
	isIndex bool
}

func parsePath(p string) ([]pathStep, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("jsonpath %q: must start with $", p)
	}
	rest := p[1:]
	var steps []pathStep
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			if strings.HasPrefix(rest, "..") {
				return nil, fmt.Errorf("jsonpath %q: recursive descent unsupported", p)
			}
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end == -1 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("jsonpath %q: empty member name", p)
			}
			steps = append(steps, pathStep{key: rest[:end]})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end == -1 {
				return nil, fmt.Errorf("jsonpath %q: unclosed bracket", p)
			}
			inner := rest[1:end]
			if strings.HasPrefix(inner, "'") && strings.HasSuffix(inner, "'") && len(inner) >= 2 {
				key := inner[1 : len(inner)-1]
				if strings.ContainsAny(key, `'\`) {
					return nil, fmt.Errorf("jsonpath %q: quoted-key escapes unsupported", p)
				}
				steps = append(steps, pathStep{key: key})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("jsonpath %q: unsupported selector [%s]", p, inner)
				}
				steps = append(steps, pathStep{idx: n, isIndex: true})
			}
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("jsonpath %q: unexpected %q", p, rest[0])
		}
	}
	return steps, nil
}

func EvalPath(docv any, path string) (any, bool, error) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, false, err
	}
	cur := docv
	for _, s := range steps {
		if s.isIndex {
			arr, ok := cur.([]any)
			if !ok || s.idx >= len(arr) {
				return nil, false, nil
			}
			cur = arr[s.idx]
		} else {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false, nil
			}
			v, ok := m[s.key]
			if !ok {
				return nil, false, nil
			}
			cur = v
		}
	}
	return cur, true, nil
}
```

(Note: `$[0]` parses because the loop starts at `[`. A quoted key containing `'` never occurs in the corpus; rejecting it loudly is deliberate.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/jsonval/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/jsonval
git commit -m "feat(oracle): mini JSONPath evaluator covering the corpus grammar"
```

---

### Task 5: Case loader (`internal/cases`) + YAML cross-check

**Files:**
- Create: `tools/oracle/internal/cases/cases.go`, `tools/oracle/internal/cases/cases_test.go`, `tools/oracle/internal/cases/xcheck_test.go`

**Interfaces:**
- Consumes: `jsonval.DeepEqual`, `jsonval.DecodeJSON`.
- Produces:

```go
package cases

type JWT struct {
	SignWith string
	Payload  map[string]any
}

type Request struct {
	Kind    string // "" (HTTP) or "cli"
	Method  string
	Path    string
	Headers map[string]string
	JWT     *JWT
	Body        any
	HasBody     bool
	BodyJSON    any
	HasBodyJSON bool
	BodyRaw     string
	HasBodyRaw  bool
	Flag string // CLI only
}

type Config struct {
	Present          bool
	Keys             map[string]any    // HTTP config keys (kebab-case); may be empty
	Env              map[string]string // CLI
	File             map[string]any    // CLI
	PreconditionsSQL []string          // CLI
}

type Case struct {
	ID      int
	Feature string
	Area    string // first '/'-segment of Feature
	Schema  string
	Request Request
	Config  Config
	Expect  map[string]any
	Notes   string
	Source  string
	Path    string // source file path
}

func Load(path string) (*Case, error)
func LoadAll(dir string) ([]*Case, error) // sorted by ID; duplicate IDs = error
```

Loading validates: `expect` keys ∈ {status, status_text, headers, headers_present, headers_absent, headers_match, headers_absent_in_value, headers_no_blank, body_exact, body_json, body_contains, body_raw, body_jsonpath, exit_code, dump_contains, stderr_contains, dump_reparse_stable} — any other key is a load error (HARNESS §4: fail loudly). Body-field presence is tracked via the `Has*` booleans so `body_exact: null` (assert-empty-body) is distinguishable from key-absent; same for request bodies.

- [ ] **Step 1: Write the failing tests**

```go
package cases

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCase(t *testing.T, dir, name, content string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadHTTPCase(t *testing.T) {
	p := writeCase(t, t.TempDir(), "1_x.yaml", `
id: 1
feature: filters/read/eq
request:
  method: GET
  path: /items?id=eq.1
  headers: { Accept: application/json }
  jwt: { sign_with: hs256_test_secret, payload: { role: r, exp: "not-a-number" } }
schema: test
config: { db-max-rows: 2 }
expect:
  status: 200
  body_exact: [{ id: 1 }]
notes: n
source: https://example.com
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != 1 || c.Area != "filters" || c.Request.Method != "GET" ||
		c.Request.JWT == nil || c.Request.JWT.Payload["exp"] != "not-a-number" ||
		!c.Config.Present || c.Config.Keys["db-max-rows"] != 2 ||
		c.Request.HasBody || c.Request.HasBodyRaw {
		t.Fatalf("bad parse: %+v", c)
	}
}

func TestLoadBodyPresence(t *testing.T) {
	p := writeCase(t, t.TempDir(), "2_x.yaml", `
id: 2
feature: a/b
request: { method: POST, path: /x, body_raw: "" }
schema: test
expect: { status: 200, body_exact: null }
notes: n
source: s
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Request.HasBodyRaw || c.Request.BodyRaw != "" {
		t.Fatal("empty body_raw must register as present")
	}
	if v, ok := c.Expect["body_exact"]; !ok || v != nil {
		t.Fatal("body_exact: null must be present with nil value")
	}
}

func TestLoadRejectsUnknownExpectKey(t *testing.T) {
	p := writeCase(t, t.TempDir(), "3_x.yaml", `
id: 3
feature: a/b
request: { method: GET, path: /x }
schema: test
expect: { status: 200, body_sorta: x }
notes: n
source: s
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want unknown expect key error")
	}
}

func TestLoadAllRealCorpus(t *testing.T) {
	root := repoRoot(t) // helper: walk up from CWD until PIN exists
	cs, err := LoadAll(filepath.Join(root, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 762 {
		t.Fatalf("got %d cases, want 762", len(cs))
	}
	cli := 0
	for _, c := range cs {
		if c.Request.Kind == "cli" {
			cli++
		}
	}
	if cli != 38 {
		t.Fatalf("got %d cli cases, want 38", cli)
	}
}
```

Include this helper in the test file:

```go
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "PIN")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("PIN not found above CWD")
		}
		dir = parent
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/cases/`
Expected: FAIL — package doesn't exist / functions undefined.

- [ ] **Step 3: Implement the loader**

Add the dependency first: `cd tools/oracle && go get gopkg.in/yaml.v3@v3.0.1`.

Implementation notes (write real code, not these notes):

- Unmarshal the file into `map[string]any` via `yaml.Unmarshal` (yaml.v3 gives `map[string]any` for mappings when the target is `any`).
- Extract fields manually; presence checks use the two-value map lookup (`v, ok := m["body_raw"]`).
- `request.headers` values may parse as non-strings (YAML `Range: "0-0"` stays a string because it's quoted, but a bare `X: 100` would be an int) — convert scalars to their YAML-literal string with a `scalarString(v any) string` helper (`fmt.Sprintf("%v", v)` for int/bool; strings as-is; anything else = error).
- `config`: if the key is absent → `Config{Present: false}`. If present: pull out reserved keys `env` (map[string]string via `scalarString` on values), `file` (map[string]any), `preconditions_sql` ([]string); everything else goes into `Keys`.
- Validate `expect` keys against the allowed set; validate `request.kind` ∈ {"", "cli"}; require `id`, `feature`, `request`, `schema`, `expect`, `source` (mirror of case.schema.json's `required`).
- `LoadAll`: `filepath.Glob(dir + "/*.yaml")`, load each, sort by ID, error on duplicates.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/cases/`
Expected: PASS (including the real-corpus count of 762/38).

- [ ] **Step 5: Write the YAML cross-check test**

`xcheck_test.go` — guards against yaml.v3 vs pyyaml scalar divergence. It shells out to `python3` (skip if unavailable or pyyaml missing) to convert every case file to JSON, then compares against the Go parse using `jsonval.DeepEqual`:

```go
package cases

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/jsonval"
)

const pyDump = `
import sys, yaml, json
for f in sys.argv[1:]:
    print(json.dumps({"file": f, "doc": yaml.safe_load(open(f))}, ensure_ascii=False))
`

func TestYAMLCrossCheckAgainstPyYAML(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command("python3", "-c", "import yaml").Run(); err != nil {
		t.Skip("pyyaml not available")
	}
	root := repoRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, "cases", "*.yaml"))
	args := append([]string{"-c", pyDump}, files...)
	out, err := exec.Command("python3", args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytesReader(out)) // bytes.NewReader
	dec.UseNumber()
	checked := 0
	for dec.More() {
		var row struct {
			File string          `json:"file"`
			Doc  json.RawMessage `json:"doc"`
		}
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		pyDoc, err := jsonval.DecodeJSON(row.Doc)
		if err != nil {
			t.Fatal(err)
		}
		goDoc, err := rawYAML(row.File) // exported-for-test: yaml.Unmarshal into any
		if err != nil {
			t.Fatalf("%s: %v", row.File, err)
		}
		if !jsonval.DeepEqual(normalizeYAML(goDoc), pyDoc) {
			t.Errorf("%s: yaml.v3 parse differs from pyyaml", row.File)
		}
		checked++
	}
	if checked != 762 {
		t.Fatalf("cross-checked %d files, want 762", checked)
	}
}
```

Implement in `cases.go`: `rawYAML(path string) (any, error)` and `normalizeYAML(v any) any` (recursively convert `map[string]any` keys — yaml.v3 may produce `map[string]any` directly for string keys, but normalize `[]any`/int types so `DeepEqual`'s numeric handling applies; strings pass through). Any real mismatch found here is a **finding** to bring to the owner, not something to paper over in the loader.

- [ ] **Step 6: Run the cross-check**

Run: `cd tools/oracle && go test ./internal/cases/ -run CrossCheck -v`
Expected: PASS with 762 files checked (or documented skips; if it fails, STOP and report which files/values diverge).

- [ ] **Step 7: Commit**

```bash
git add tools/oracle
git commit -m "feat(oracle): case loader with expect-key validation and pyyaml cross-check"
```

---

### Task 6: Assertion engine (`internal/assert`)

**Files:**
- Create: `tools/oracle/internal/assert/assert.go`, `tools/oracle/internal/assert/assert_test.go`

**Interfaces:**
- Consumes: `cases.Case`, `jsonval.{DecodeJSON,DeepEqual,EvalPath}`.
- Produces:

```go
package assert

type HTTPResponse struct {
	StatusCode    int
	Reason        string
	Header        http.Header // canonical-key map, all values
	Body          []byte
	ContentLength int64 // -1 if unknown
}

type CLIResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Redump    []byte // second dump for dump_reparse_stable
	RedumpRan bool
}

func CheckHTTP(c *cases.Case, r *HTTPResponse) []string // nil/empty = pass; each string one failure
func CheckCLI(c *cases.Case, r *CLIResult) []string
```

- [ ] **Step 1: Write the failing tests**

Table-driven tests covering each assertion key's pass AND fail direction. The essential ones (write all of these):

```go
func resp(status int, reason string, hdr map[string][]string, body string) *HTTPResponse {
	h := http.Header{}
	for k, vs := range hdr {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return &HTTPResponse{StatusCode: status, Reason: reason, Header: h,
		Body: []byte(body), ContentLength: int64(len(body))}
}

// mkCase builds a Case with only Expect set, from a YAML fragment.
func mkCase(t *testing.T, expectYAML string) *cases.Case {
	t.Helper()
	var expect map[string]any
	if err := yaml.Unmarshal([]byte(expectYAML), &expect); err != nil {
		t.Fatal(err)
	}
	return &cases.Case{ID: 999, Expect: expect}
}
```

- `status`: 200 vs 200 passes; 200 vs 404 fails with a message containing both.
- `status_text`: `"My Custom Status"` vs Reason match/mismatch.
- `headers`: `{Content-Type: application/json; charset=utf-8}` — case-insensitive name (`content-type` in response map via `http.Header` canonicalization), exact value; an int-valued expected header (`Content-Length: 2`) compares as `"2"`.
- `headers` fold: response with two `X-Multi` values `a`,`b` → expected `"a, b"` passes; two `Set-Cookie` values → expected joined with `"\n"` passes and `", "` join fails.
- `Content-Length` synthesis: response whose Header map lacks Content-Length but `ContentLength: 5` → `headers: {Content-Length: "5"}` passes.
- `headers_present` / `headers_absent` / `headers_match` (regex `^postgrest/.+`) / `headers_absent_in_value` (substr present ⇒ fail; missing header treated as "") / `headers_no_blank` (a header with value `"   "` fails).
- `body_exact`: `[{id: 1}]` vs body `[{"id": 1}]` passes; vs `[{"id": 2}]` fails; `body_exact: null` vs empty body passes, vs `null` body (4 bytes) fails; **key order and whitespace insensitivity**: expected `{a: 1, b: 2}` vs body `{"b":2, "a":1}` passes. `body_json` behaves identically.
- `body_contains`: single string and list forms.
- `body_raw`: byte-exact.
- `body_jsonpath`: one entry each for `equals`, `present: true`, `exists: true`, `absent: true`, plus a failing `equals`.
- `CheckCLI`: exit 0 vs `exit_code: 0`; exit 3 vs `exit_code: nonzero` passes, exit 0 vs `nonzero` fails; `dump_contains` / `stderr_contains` substring checks; `dump_reparse_stable: true` with `Redump == Stdout` passes, differing fails, `RedumpRan == false` fails.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/assert/`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Implement**

Key implementation rules (from HARNESS §4 — the spec's Assertions section is the contract):

```go
func foldedHeader(h http.Header, name string, contentLength int64) (string, bool) {
	vs := h.Values(name) // case-insensitive via canonical key
	if len(vs) == 0 {
		if strings.EqualFold(name, "Content-Length") && contentLength >= 0 {
			return strconv.FormatInt(contentLength, 10), true
		}
		return "", false
	}
	sep := ", "
	if strings.EqualFold(name, "Set-Cookie") {
		sep = "\n"
	}
	return strings.Join(vs, sep), true
}

func expectedScalar(v any) string { // YAML header values may be int/bool
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}
```

- Iterate `c.Expect` keys with a `switch`; the loader already rejected unknown keys, but keep a `default: failures = append(failures, "unhandled expect key "+k)` guard anyway.
- `body_exact` / `body_json`: if expected is `nil` → `len(r.Body) == 0` required. Else `jsonval.DecodeJSON(r.Body)` (decode error = failure with body excerpt) then `jsonval.DeepEqual(decoded, expected)`; on mismatch include both re-marshalled compactly (truncate at ~2KB).
- `headers_match`: `regexp.Compile` each pattern (compile error = failure), match against folded value; missing header = failure.
- `body_jsonpath`: entries are `[]any` of `map[string]any` with `path` + exactly one predicate; treat `present` and `exists` identically.
- Failure strings must be self-contained: `fmt.Sprintf("status: got 404, want 200")`, `fmt.Sprintf("headers[%s]: got %q, want %q", name, got, want)` etc.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/assert/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/assert
git commit -m "feat(oracle): HARNESS §4 assertion engine with fold and byte-profile rules"
```

---

### Task 7: JWT minting (`internal/httpexec/jwt.go`)

**Files:**
- Create: `tools/oracle/internal/httpexec/jwt.go`, `tools/oracle/internal/httpexec/jwt_test.go`

**Interfaces:**
- Produces: `httpexec.MintHS256(payload map[string]any, secret string) (string, error)`; `httpexec.HS256TestSecret = "reallyreallyreallyreallyverysafe"` (HARNESS §3.2's only `sign_with` secret).

- [ ] **Step 1: Write the failing tests**

```go
package httpexec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/jsonval"
)

// Known-answer from HARNESS §3.1 (case 1452's literal token): re-sign the
// exact signing input and compare signatures — validates the HMAC/base64url
// mechanics without depending on JSON key order.
func TestHS256KnownAnswer(t *testing.T) {
	signingInput := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoicG9zdGdyZXN0X3Rlc3RfYXV0aG9yIiwiaWQiOiJqZG9lIn0"
	wantSig := "B-lReuGNDwAlU1GOC476MlO0vAt9JNoHIlxg2vwMaO0"
	mac := hmac.New(sha256.New, []byte(HS256TestSecret))
	mac.Write([]byte(signingInput))
	if got := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)); got != wantSig {
		t.Fatalf("got %s", got)
	}
}

func TestMintHS256Structure(t *testing.T) {
	tok, err := MintHS256(map[string]any{"role": "r", "exp": "not-a-number"}, HS256TestSecret)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 segments, got %d", len(parts))
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	hv, _ := jsonval.DecodeJSON(hdr)
	if !jsonval.DeepEqual(hv, map[string]any{"alg": "HS256", "typ": "JWT"}) {
		t.Fatalf("header: %s", hdr)
	}
	pl, _ := base64.RawURLEncoding.DecodeString(parts[1])
	pv, _ := jsonval.DecodeJSON(pl)
	// payload signed as-is: the invalid exp string must survive untouched
	if !jsonval.DeepEqual(pv, map[string]any{"role": "r", "exp": "not-a-number"}) {
		t.Fatalf("payload: %s", pl)
	}
	mac := hmac.New(sha256.New, []byte(HS256TestSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("signature does not verify")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/httpexec/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
package httpexec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

const HS256TestSecret = "reallyreallyreallyreallyverysafe"

func MintHS256(payload map[string]any, secret string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // claim values may contain URLs; keep bytes literal
	if err := enc.Encode(payload); err != nil {
		return "", err
	}
	pl := bytes.TrimRight(buf.Bytes(), "\n")
	b64 := base64.RawURLEncoding
	signing := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64.EncodeToString(pl)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	return signing + "." + b64.EncodeToString(mac.Sum(nil)), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/httpexec/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/httpexec
git commit -m "feat(oracle): HS256 JWT minting, sign-payload-as-is"
```

---

### Task 8: Raw path encoder (`internal/httpexec/pathenc.go`)

**Files:**
- Create: `tools/oracle/internal/httpexec/pathenc.go`, `tools/oracle/internal/httpexec/pathenc_test.go`

**Interfaces:**
- Produces: `httpexec.EncodeRawPath(p string) string` — HARNESS §3 step 2: percent-encode ONLY what HTTP/1.1 cannot carry (space and control bytes, `" < > \ ^ ` + backtick + ` { } |`, bytes ≥ 0x80, and a `%` not followed by two hex digits); leave existing `%XX`, `+`, and all reserved delimiters untouched.

- [ ] **Step 1: Write the failing tests**

```go
func TestEncodeRawPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/items?id=eq.1", "/items?id=eq.1"},                                     // untouched
		{"/x?a=in.(    )", "/x?a=in.(%20%20%20%20)"},                             // spaces (case 10204)
		{"/entities?arr=eq.{1,2,3}&select=id", "/entities?arr=eq.%7B1,2,3%7D&select=id"}, // braces (10212)
		{"/simple_pk?k=match.^xy", "/simple_pk?k=match.%5Exy"},                   // caret (1063)
		{"/a?b=like(any).{%plan%,%brain%}", "/a?b=like(any).%7B%25plan%25,%25brain%25%7D"}, // bare % (1086)
		{"/%D9%85%D9%88%D8%A7%D8%B1%D8%AF", "/%D9%85%D9%88%D8%A7%D8%B1%D8%AF"},   // existing escapes kept (1003)
		{"/x?q=a+b", "/x?q=a+b"},                                                 // + untouched
		{`/x?name=eq."q"`, "/x?name=eq.%22q%22"},                                 // double quotes
		{"/تست", "/%D8%AA%D8%B3%D8%AA"},                                          // raw UTF-8 bytes
		{"/x%2F", "/x%2F"},                                                       // valid escape at end of string
		{"/x%2", "/x%252"},                                                       // truncated escape at end -> encode %
	} {
		if got := EncodeRawPath(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/oracle && go test ./internal/httpexec/ -run EncodeRawPath`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func EncodeRawPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '%':
			if i+2 < len(p) && isHex(p[i+1]) && isHex(p[i+2]) {
				b.WriteByte(c)
			} else {
				b.WriteString("%25")
			}
		case c <= ' ' || c >= 0x7f || strings.IndexByte("\"<>\\^`{}|", c) >= 0:
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
```

(The `i+2 < len(p)` bound is correct for an escape ending exactly at the string end: for `"/x%2F"`, `i=2`, `i+2=4 < 5` — the two `%2F`/`%2`-at-end test cases in Step 1 prove both directions.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/httpexec/`
Expected: PASS (add the `%2F`-at-end test case from the note above).

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/httpexec
git commit -m "feat(oracle): minimal transport encoding for raw request paths"
```

---

### Task 9: Request builder + sender (`internal/httpexec/exec.go`)

**Files:**
- Create: `tools/oracle/internal/httpexec/exec.go`, `tools/oracle/internal/httpexec/exec_test.go`

**Interfaces:**
- Consumes: `cases.Case`, `MintHS256`, `EncodeRawPath`, `assert.HTTPResponse`.
- Produces:

```go
// BuildSpec resolves headers/body/JWT per HARNESS §3. injectProfile is the
// Accept-Profile value to add with put-new semantics ("" = none).
type Spec struct {
	Method, Path string
	Headers      map[string]string
	Body         []byte
	HasBody      bool
}
func BuildSpec(c *cases.Case, injectProfile string) (*Spec, error)

// Do sends the spec to 127.0.0.1:port over plain HTTP/1.1.
func Do(port int, s *Spec) (*assert.HTTPResponse, error)
```

- [ ] **Step 1: Write the failing tests**

`BuildSpec` unit tests:

```go
func TestBuildSpecProfileInjection(t *testing.T) {
	c := &cases.Case{Request: cases.Request{Method: "GET", Path: "/x",
		Headers: map[string]string{"Accept": "application/json"}}}
	s, _ := BuildSpec(c, "operators")
	if s.Headers["Accept-Profile"] != "operators" {
		t.Fatal("must inject Accept-Profile")
	}
	// put-new: explicit Accept-Profile wins
	c.Request.Headers = map[string]string{"Accept-Profile": "v2"}
	s, _ = BuildSpec(c, "headers")
	if s.Headers["Accept-Profile"] != "v2" {
		t.Fatal("explicit Accept-Profile must win")
	}
	// Content-Profile does NOT suppress injection (INDEX.md caveat)
	c.Request.Headers = map[string]string{"Content-Profile": "v2"}
	s, _ = BuildSpec(c, "headers")
	if s.Headers["Accept-Profile"] != "headers" {
		t.Fatal("Content-Profile must not suppress injection")
	}
}

func TestBuildSpecJWT(t *testing.T) {
	c := &cases.Case{Request: cases.Request{Method: "GET", Path: "/x",
		JWT: &cases.JWT{SignWith: "hs256_test_secret", Payload: map[string]any{"role": "r"}}}}
	s, err := BuildSpec(c, "")
	if err != nil || !strings.HasPrefix(s.Headers["Authorization"], "Bearer ey") {
		t.Fatalf("want minted bearer, got %q (%v)", s.Headers["Authorization"], err)
	}
	// explicit Authorization wins over jwt block
	c.Request.Headers = map[string]string{"Authorization": "Bearer literal"}
	s, _ = BuildSpec(c, "")
	if s.Headers["Authorization"] != "Bearer literal" {
		t.Fatal("explicit Authorization must win")
	}
	// unknown sign_with is an error
	c.Request.Headers = nil
	c.Request.JWT.SignWith = "nope"
	if _, err := BuildSpec(c, ""); err == nil {
		t.Fatal("want unknown sign_with error")
	}
}

func TestBuildSpecBodies(t *testing.T) {
	mk := func(r cases.Request) *Spec {
		s, err := BuildSpec(&cases.Case{Request: r}, "")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := mk(cases.Request{BodyRaw: "a,b\n1,2", HasBodyRaw: true}); string(s.Body) != "a,b\n1,2" {
		t.Fatal("body_raw verbatim")
	}
	if s := mk(cases.Request{BodyJSON: "str", HasBodyJSON: true}); string(s.Body) != `"str"` {
		t.Fatal("body_json always JSON-encodes")
	}
	if s := mk(cases.Request{Body: "str", HasBody: true}); string(s.Body) != "str" {
		t.Fatal("body: string sent as-is")
	}
	if s := mk(cases.Request{Body: map[string]any{"a": 1}, HasBody: true}); string(s.Body) != `{"a":1}` {
		t.Fatal("body: non-string JSON-encoded")
	}
	if s := mk(cases.Request{}); s.HasBody {
		t.Fatal("no body key = no body")
	}
}
```

Wire-level test with a raw TCP server (verifies raw target bytes, custom reason phrase, repeated headers, no auto-added Accept-Encoding):

```go
func TestDoRawWire(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gotLine := make(chan string, 1)
	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, _ := br.ReadString('\n')
		gotLine <- strings.TrimRight(line, "\r\n")
		for { // drain headers
			h, _ := br.ReadString('\n')
			if h == "\r\n" || h == "\n" || h == "" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 419 My Custom Status\r\n" +
			"Set-Cookie: a=1\r\n" +
			"Set-Cookie: b=2; Expires=Wed, 21 Oct 2015 07:28:00 GMT\r\n" +
			"Content-Length: 2\r\n\r\nhi"))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r, err := Do(port, &Spec{Method: "GET", Path: "/x?a=in.( )", Headers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if line := <-gotLine; line != "GET /x?a=in.(%20) HTTP/1.1" {
		t.Fatalf("request line: %q", line)
	}
	if r.StatusCode != 419 || r.Reason != "My Custom Status" {
		t.Fatalf("status: %d %q", r.StatusCode, r.Reason)
	}
	if got := r.Header.Values("Set-Cookie"); len(got) != 2 {
		t.Fatalf("set-cookie values: %v", got)
	}
	if string(r.Body) != "hi" || r.ContentLength != 2 {
		t.Fatalf("body/CL: %q %d", r.Body, r.ContentLength)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/httpexec/`
Expected: FAIL — `Spec`, `BuildSpec`, `Do` undefined.

- [ ] **Step 3: Implement**

```go
var sharedTransport = &http.Transport{
	DisableCompression: true, // preserve wire Content-Length; never send Accept-Encoding
	Proxy:              nil,
	MaxIdleConnsPerHost: 4,
}
var client = &http.Client{
	Transport: sharedTransport,
	Timeout:   30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // never follow; cases assert the 3xx itself
	},
}

func BuildSpec(c *cases.Case, injectProfile string) (*Spec, error) {
	h := map[string]string{}
	for k, v := range c.Request.Headers {
		h[k] = v
	}
	hasHeader := func(name string) bool {
		for k := range h {
			if strings.EqualFold(k, name) {
				return true
			}
		}
		return false
	}
	if injectProfile != "" && !hasHeader("Accept-Profile") {
		h["Accept-Profile"] = injectProfile
	}
	if c.Request.JWT != nil && !hasHeader("Authorization") {
		if c.Request.JWT.SignWith != "hs256_test_secret" {
			return nil, fmt.Errorf("case %d: unknown sign_with %q", c.ID, c.Request.JWT.SignWith)
		}
		tok, err := MintHS256(c.Request.JWT.Payload, HS256TestSecret)
		if err != nil {
			return nil, err
		}
		h["Authorization"] = "Bearer " + tok
	}
	s := &Spec{Method: c.Request.Method, Path: c.Request.Path, Headers: h}
	switch {
	case c.Request.HasBodyRaw:
		s.Body, s.HasBody = []byte(c.Request.BodyRaw), true
	case c.Request.HasBodyJSON:
		b, err := marshalNoHTMLEscape(c.Request.BodyJSON)
		if err != nil {
			return nil, err
		}
		s.Body, s.HasBody = b, true
	case c.Request.HasBody:
		if str, ok := c.Request.Body.(string); ok {
			s.Body, s.HasBody = []byte(str), true
		} else {
			b, err := marshalNoHTMLEscape(c.Request.Body)
			if err != nil {
				return nil, err
			}
			s.Body, s.HasBody = b, true
		}
	}
	return s, nil
}

func Do(port int, s *Spec) (*assert.HTTPResponse, error) {
	u := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), Opaque: EncodeRawPath(s.Path)}
	var body io.Reader
	if s.HasBody {
		body = bytes.NewReader(s.Body)
	}
	req, err := http.NewRequest(s.Method, "http://placeholder/", body)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.Host = u.Host
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode))
	reason = strings.TrimPrefix(reason, " ")
	return &assert.HTTPResponse{
		StatusCode: resp.StatusCode, Reason: reason,
		Header: resp.Header, Body: b, ContentLength: resp.ContentLength,
	}, nil
}
```

`marshalNoHTMLEscape` is the same encoder shape as in `MintHS256` (factor it out into a shared helper in this package).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/httpexec/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/httpexec
git commit -m "feat(oracle): HTTP request builder and raw-wire sender"
```

---

### Task 10: Routing (`internal/route`)

**Files:**
- Create: `tools/oracle/internal/route/route.go`, `tools/oracle/internal/route/route_test.go`

**Interfaces:**
- Consumes: `cases.Case`.
- Produces:

```go
package route

// Val is a translated config value: either a concrete env value or "clear
// this key" (YAML null — the variant omits the env var).
type Val struct {
	Clear bool
	V     string
}

type Placement struct {
	Kind          string // "http" | "cli"
	Base          string // "bulk" | "auth" | "multi" | "unicode" (http only)
	Overlay       map[string]Val // PGRST_* overrides; empty = shared instance
	SafeUpdate    bool           // 1387-1389: db-uri needs safeupdate preloaded
	InjectProfile string
	GroupKey      string // Base when shared; else Base+canonical(Overlay)(+"+safeupdate")
}

func BaseConfigs() map[string]map[string]string // "bulk"/"auth"/"multi"/"unicode" -> PGRST_* map (the Reference-values tables)
func Route(c *cases.Case) (*Placement, error)
func CrossCheckHarness(all map[int]*Placement) []string // findings: variant-set vs HARNESS §2.3's 33 ids
```

- [ ] **Step 1: Write the failing tests**

```go
func httpCase(id int, schema, path string, cfg map[string]any) *cases.Case {
	c := &cases.Case{ID: id, Schema: schema,
		Request: cases.Request{Method: "GET", Path: path}}
	if cfg != nil {
		c.Config = cases.Config{Present: true, Keys: cfg}
	}
	return c
}

func TestRouteBases(t *testing.T) {
	for _, tc := range []struct {
		c        *cases.Case
		base     string
		profile  string
		variant  bool
	}{
		{httpCase(1, "test", "/items", nil), "bulk", "", false},
		{httpCase(2, "operators", "/items", nil), "bulk", "operators", false},
		{httpCase(3, "auth", "/items", nil), "auth", "auth", false},
		{httpCase(4, "test", "/", nil), "auth", "", false},          // root path -> auth
		{httpCase(1005, "multi", "/parents", nil), "multi", "", false},
		{httpCase(1558, "headers", "/x", nil), "multi", "", false},  // headers-profile exception
		{httpCase(1003, "unicode", "/x", nil), "unicode", "", false},
		{httpCase(1771, "observability", "/", nil), "auth", "observability", false},
	} {
		p, err := Route(tc.c)
		if err != nil {
			t.Fatal(err)
		}
		if p.Base != tc.base || p.InjectProfile != tc.profile || (len(p.Overlay) > 0) != tc.variant {
			t.Fatalf("case %d: got %+v", tc.c.ID, p)
		}
	}
}

func TestRouteSatisfaction(t *testing.T) {
	// server-timing-enabled true == bulk's effective value -> shared
	p, _ := Route(httpCase(1750, "observability", "/x", map[string]any{"server-timing-enabled": true}))
	if len(p.Overlay) != 0 {
		t.Fatal("1750 must be satisfied by bulk")
	}
	// db-max-rows 2 != unset default -> variant
	p, _ = Route(httpCase(1700, "config", "/items", map[string]any{"db-max-rows": 2}))
	if v := p.Overlay["PGRST_DB_MAX_ROWS"]; v.V != "2" || p.GroupKey == "bulk" {
		t.Fatalf("1700: %+v", p)
	}
	// jwt-aud null on auth base: effective is unset -> satisfied (case 1474)
	p, _ = Route(httpCase(1474, "auth", "/x", map[string]any{"jwt-aud": nil}))
	if len(p.Overlay) != 0 {
		t.Fatal("1474 must be satisfied (jwt-aud already unset)")
	}
	// db-anon-role null on auth base: effective is set -> variant with Clear
	p, _ = Route(httpCase(1491, "auth", "/x", map[string]any{"db-anon-role": nil}))
	if v := p.Overlay["PGRST_DB_ANON_ROLE"]; !v.Clear {
		t.Fatalf("1491: %+v", p)
	}
	// sentinel expansion
	p, _ = Route(httpCase(1467, "auth", "/x", map[string]any{"jwt-secret": "asymmetric_jwk_public_key"}))
	if v := p.Overlay["PGRST_JWT_SECRET"]; !strings.Contains(v.V, `"kty":"RSA"`) {
		t.Fatalf("1467: %+v", v)
	}
	// identical configs share a GroupKey
	a, _ := Route(httpCase(1468, "auth", "/x", map[string]any{"jwt-aud": "youraudience"}))
	b, _ := Route(httpCase(1469, "auth", "/x", map[string]any{"jwt-aud": "youraudience"}))
	if a.GroupKey != b.GroupKey {
		t.Fatal("same config must share GroupKey")
	}
}

func TestRouteSpecials(t *testing.T) {
	p, _ := Route(httpCase(1387, "mutations", "/safe_update_items", nil))
	if !p.SafeUpdate || p.Base != "bulk" || p.InjectProfile != "mutations" {
		t.Fatalf("1387: %+v", p)
	}
	p, _ = Route(httpCase(1654, "openapi_no_comment", "/", nil))
	if v := p.Overlay["PGRST_DB_SCHEMAS"]; v.V != "openapi_no_comment" || p.Base != "auth" {
		t.Fatalf("1654: %+v", p)
	}
	p, _ = Route(httpCase(1764, "observability", "/", map[string]any{"log-level": "error"}))
	if v, ok := p.Overlay["PGRST_JWT_SECRET"]; !ok || !v.Clear {
		t.Fatalf("1764 must clear jwt-secret: %+v", p)
	}
	cli := &cases.Case{ID: 1705, Schema: "config", Request: cases.Request{Kind: "cli", Flag: "--dump-config"}}
	p, _ = Route(cli)
	if p.Kind != "cli" {
		t.Fatal("cli case must route to cli")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd tools/oracle && go test ./internal/route/`
Expected: FAIL.

- [ ] **Step 3: Implement**

Implementation content, in order:

1. `BaseConfigs()` returning the four maps from **Reference values** verbatim (write the `db-schemas` strings exactly, including `SPECIAL "@/\#~_-` — in Go source: `"test,operators,ordering,pagination,representations,mutations,rpc,headers,config,openapi,domain_representations,observability,auth,v1,v2,SPECIAL \"@/\\#~_-,تست"`).
2. `pgDefaults` map (from **Reference values**).
3. `envKey(kebab string) string` → `"PGRST_" + strings.ToUpper(strings.ReplaceAll(kebab, "-", "_"))`.
4. `translate(cfg map[string]any) (map[string]Val, error)`:
   - `nil` → `Val{Clear: true}`; `bool` → `"true"/"false"`; int/float via `fmt.Sprintf("%v")`; string → sentinel expansion (`asymmetric_jwk_public_key` → `asymJWK` const; `asymmetric_jwks_public_key` → `{"keys":[…]}`), else as-is.
5. `Route`:
   - `Kind == "cli"` → `&Placement{Kind: "cli"}`.
   - id ∈ {1387, 1388, 1389} → bulk + `SafeUpdate: true`, `GroupKey: "bulk+safeupdate"`, profile `mutations`.
   - `Schema == "multi"` or id ∈ {1557, 1558, 1559, 1560, 1574, 1583} → base `multi`, no profile injection.
   - `Schema == "unicode"` → base `unicode`, no injection.
   - else base `auth` if `Schema ∈ {"auth", "openapi", "openapi_no_comment"}` or `Request.Path` is `/` or starts with `/?`; else `bulk`.
   - InjectProfile: `""` if `Schema ∈ {"", "public", "test", "multi", "unicode"}` else `Schema`.
   - Overlay: for each translated key, keep it only if NOT satisfied: `eff := base[k]`, falling back to `pgDefaults[k]`, falling back to `""`; satisfied means (`Clear` && `eff == ""`) or (!`Clear` && `eff == v.V`).
   - Extras: id 1654 → overlay `PGRST_DB_SCHEMAS = "openapi_no_comment"`; id 1764 → overlay `PGRST_JWT_SECRET = Clear` (merged after the case's own config).
   - GroupKey: base name if overlay empty && !SafeUpdate; else base + "|" + sorted `k=v`/`k=∅` pairs joined with `;` (+ `"+safeupdate"`).
6. `CrossCheckHarness`: harness table ids as a package-level `[]int` (from **Reference values**); produce one finding line per id that is variant-in-oracle-but-not-in-§2.3 and per id §2.3-lists-but-oracle-shares.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd tools/oracle && go test ./internal/route/`
Expected: PASS.

- [ ] **Step 5: Add the corpus-wide routing smoke test**

Append to `route_test.go` (uses the real corpus):

```go
// repoRoot walks up from CWD until a PIN file is found (same helper shape
// as in internal/cases tests; each test package carries its own copy).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "PIN")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("PIN not found above CWD")
		}
		dir = parent
	}
}

func TestRouteWholeCorpus(t *testing.T) {
	root := repoRoot(t)
	cs, err := cases.LoadAll(filepath.Join(root, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[int]*Placement{}
	groups := map[string]int{}
	for _, c := range cs {
		p, err := Route(c)
		if err != nil {
			t.Fatalf("case %d: %v", c.ID, err)
		}
		all[c.ID] = p
		if p.Kind == "http" {
			groups[p.GroupKey]++
		}
	}
	t.Logf("distinct http groups: %d", len(groups))
	for _, f := range CrossCheckHarness(all) {
		t.Logf("HARNESS finding: %s", f)
	}
}
```

Run with `-v` and eyeball: expect the 4 shared groups plus roughly 20–35 variant groups, and a non-empty findings list (§2.3 is already known to disagree — the findings are logged, not failed).

- [ ] **Step 6: Commit**

```bash
git add tools/oracle/internal/route
git commit -m "feat(oracle): case routing with config-satisfaction and HARNESS 2.3 cross-check"
```

---

### Task 11: Database bootstrap (`internal/db`) + Docker make targets

**Files:**
- Create: `tools/oracle/internal/db/db.go`, `tools/oracle/internal/db/db_test.go`
- Modify: `tools/oracle/Makefile`, `tools/oracle/cmd/oracle/main.go` (register `db-setup`, `db-teardown`)

**Interfaces:**
- Produces:

```go
package db

type PGEnv struct{ Host, Port, User, Password string } // FromEnv() reads PGHOST/PGPORT/PGUSER/PGPASSWORD with defaults localhost/6432/postgres/postgres

func FromEnv() PGEnv
func (p PGEnv) URI(dbname string) string // postgresql://user:pass@host:port/dbname (url.QueryEscape the password)
func (p PGEnv) Psql(dbname string, extraEnv []string, args ...string) error // exec psql -v ON_ERROR_STOP=1 -q with PG* env
func Setup(p PGEnv, dbname, fixturesDir string) error
func Teardown(p PGEnv, dbname string) error // DROP DATABASE IF EXISTS + DROP ROLE IF EXISTS db_config_authenticator
```

- [ ] **Step 1: Add the Docker targets to the Makefile**

```make
DB_CONTAINER ?= oracle-pg
DB_PORT ?= 6432

.PHONY: db-up db-down
db-up:
	docker run -d --name $(DB_CONTAINER) -p $(DB_PORT):5432 \
	  -e POSTGRES_PASSWORD=postgres postgis/postgis:17-3.5
	@until docker exec $(DB_CONTAINER) pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	docker exec $(DB_CONTAINER) bash -c \
	  'apt-get update -qq && apt-get install -y -qq postgresql-17-pg-safeupdate'
db-down:
	docker rm -f $(DB_CONTAINER)
```

- [ ] **Step 2: Verify the safeupdate install (milestone-0 risk item)**

Run: `cd tools/oracle && make db-up`
Expected: container up; the apt install succeeds (the postgis image is Debian with the PGDG repo configured). Then verify the library actually loads:

```bash
PGPASSWORD=postgres psql -h localhost -p 6432 -U postgres -c "LOAD 'safeupdate'; SET safeupdate.enabled = on; SELECT 1"
```

Expected: `1`. **If the package does not exist**, STOP: fall back to compiling in-container (`apt-get install -y gcc make postgresql-server-dev-17 && git clone https://github.com/eradman/pg-safeupdate && make -C pg-safeupdate install` — pin the clone to the latest release tag), verify the same `LOAD`, and record which path worked in the commit message and as a note for Task 17's workflow.

- [ ] **Step 3: Write the failing integration test**

```go
package db

import (
	"os"
	"os/exec"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "PIN")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("PIN not found above CWD")
		}
		dir = parent
	}
}

func TestSetupLoadsFixtureChain(t *testing.T) {
	if os.Getenv("ORACLE_TEST_DB") == "" {
		t.Skip("set ORACLE_TEST_DB=1 with `make db-up` running")
	}
	p := FromEnv()
	root := repoRoot(t)
	const name = "postgrest_conf_oracle_test"
	defer Teardown(p, name)
	if err := Setup(p, name, root+"/fixtures"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("psql", p.URI(name), "-tAc",
		`SELECT count(*) FROM pg_namespace WHERE nspname IN ('test','mutations','تست','v1','v2')`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "5\n" {
		t.Fatalf("schemas missing: %q", out)
	}
}
```

- [ ] **Step 4: Run to verify it fails**

Run: `cd tools/oracle && ORACLE_TEST_DB=1 go test ./internal/db/`
Expected: FAIL — undefined functions. (Plain `go test ./internal/db/` must SKIP.)

- [ ] **Step 5: Implement**

`Setup`, per fixtures/README exactly:

1. `Psql("postgres", nil, "-f", fixturesDir+"/01_roles.sql")`
2. `Psql("postgres", nil, "-c", "DROP DATABASE IF EXISTS "+dbname)` then
   `-c "CREATE DATABASE <dbname> TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"`
3. For each of `02_base.sql 03_supplement.sql 04_postgis.sql 05_corrections.sql 06_area_schemas.sql 07_analyze.sql` (glob `0[2-7]_*.sql`, sorted): `Psql(dbname, []string{"PGTZ=UTC"}, "-f", file)`.

`Psql` builds `exec.Command("psql", append([]string{"-v", "ON_ERROR_STOP=1", "-q"}, args...)...)` with env `PGHOST/PGPORT/PGUSER/PGPASSWORD` from the struct plus `extraEnv`; on failure return an error containing stderr.

Register `db-setup` / `db-teardown` subcommands in `main.go` (db name flag `-db`, default `postgrest_conf_oracle`; fixtures dir = `<repoRoot>/fixtures`).

- [ ] **Step 6: Run to verify it passes**

Run: `cd tools/oracle && ORACLE_TEST_DB=1 go test ./internal/db/ && go test ./internal/db/`
Expected: first PASS, second SKIP.

- [ ] **Step 7: Commit**

```bash
git add tools/oracle
git commit -m "feat(oracle): fixture-chain DB bootstrap and dockerized PG17+PostGIS+safeupdate"
```

---

### Task 12: Instance manager (`internal/instance`)

**Files:**
- Create: `tools/oracle/internal/instance/instance.go`, `tools/oracle/internal/instance/instance_test.go`

**Interfaces:**
- Consumes: `route.Val`, `db.PGEnv`.
- Produces:

```go
package instance

type Instance struct {
	Port      int
	AdminPort int
	// unexported: cmd, stderr ring buffer
}

// Start boots PostgREST: base config (PGRST_* map) + overlay applied
// (Clear deletes the key), db-uri (with safeupdate options when asked),
// dynamic ports, admin /ready polling (30s timeout).
func Start(bin string, base map[string]string, overlay map[string]route.Val,
	dbURI string, safeUpdate bool) (*Instance, error)
func (i *Instance) Stop()
```

- [ ] **Step 1: Write the failing unit test for env assembly**

Export a pure helper for testing: `BuildEnv(base map[string]string, overlay map[string]route.Val, dbURI string, safeUpdate bool, port, adminPort int) []string`.

```go
func TestBuildEnv(t *testing.T) {
	base := map[string]string{"PGRST_LOG_LEVEL": "error", "PGRST_JWT_SECRET": "s"}
	overlay := map[string]route.Val{
		"PGRST_JWT_SECRET":  {Clear: true},
		"PGRST_DB_MAX_ROWS": {V: "2"},
	}
	env := BuildEnv(base, overlay, "postgresql://u:p@h:6432/d", false, 3001, 3002)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if _, ok := got["PGRST_JWT_SECRET"]; ok {
		t.Fatal("cleared key must be absent")
	}
	if got["PGRST_DB_MAX_ROWS"] != "2" || got["PGRST_SERVER_PORT"] != "3001" ||
		got["PGRST_ADMIN_SERVER_PORT"] != "3002" || got["PGRST_DB_URI"] != "postgresql://u:p@h:6432/d" {
		t.Fatalf("env: %v", got)
	}
	env = BuildEnv(base, nil, "postgresql://u:p@h:6432/d", true, 1, 2)
	for _, kv := range env {
		if strings.HasPrefix(kv, "PGRST_DB_URI=") &&
			!strings.HasSuffix(kv, "?options=-csession_preload_libraries%3Dsafeupdate") {
			t.Fatalf("safeupdate uri: %s", kv)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd tools/oracle && go test ./internal/instance/`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `BuildEnv`: copy base; apply overlay (Clear → delete); add `PGRST_DB_URI` (append the safeupdate query — `?options=…` or `&options=…` if the URI already has a `?`), `PGRST_SERVER_PORT`, `PGRST_ADMIN_SERVER_PORT`; render as `k=v` slice plus `PATH` from the parent env (nothing else inherited — deliberately no ambient `PGRST_*`/`PG*` leakage).
- `Start`: pick two free ports (`net.Listen("tcp", "127.0.0.1:0")`, read, close); `exec.Command(bin)` with `cmd.Env = BuildEnv(...)`, stderr to a bounded buffer (keep last 64KB); start; poll `http://127.0.0.1:<admin>/ready` every 100ms until 200 or 30s (on timeout: Stop and return error including the stderr tail — this is the message an implementer sees when e.g. a schema in `db-schemas` breaks boot).
- `Stop`: `cmd.Process.Signal(syscall.SIGTERM)`, wait up to 5s, then `Kill`.

- [ ] **Step 4: Add the guarded boot test**

```go
func TestStartAgainstRealDB(t *testing.T) {
	bin, uri := os.Getenv("ORACLE_TEST_BIN"), os.Getenv("ORACLE_TEST_DB_URI")
	if bin == "" || uri == "" {
		t.Skip("set ORACLE_TEST_BIN and ORACLE_TEST_DB_URI (a loaded fixture DB)")
	}
	inst, err := Start(bin, map[string]string{
		"PGRST_DB_SCHEMAS": "test", "PGRST_DB_TX_END": "rollback",
		"PGRST_SERVER_HOST": "127.0.0.1", "PGRST_DB_CONFIG": "false",
		"PGRST_LOG_LEVEL": "error",
	}, nil, uri, false)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Stop()
	r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", inst.Port))
	if err != nil || r.StatusCode >= 500 {
		t.Fatalf("root request: %v %v", r, err)
	}
}
```

- [ ] **Step 5: Run all instance tests**

Run (with `make db-up` active and a DB loaded via `go run ./cmd/oracle db-setup`):

```bash
cd tools/oracle
export ORACLE_TEST_BIN="$(go run ./cmd/oracle fetch)"
export ORACLE_TEST_DB_URI="postgresql://postgres:postgres@localhost:6432/postgrest_conf_oracle"
go test ./internal/instance/ -v
```

Expected: PASS (BuildEnv + real boot).

- [ ] **Step 6: Commit**

```bash
git add tools/oracle/internal/instance
git commit -m "feat(oracle): PostgREST instance manager with overlay env and /ready polling"
```

---

### Task 13: CLI executor (`internal/cliexec`)

**Files:**
- Create: `tools/oracle/internal/cliexec/cliexec.go`, `tools/oracle/internal/cliexec/cliexec_test.go`

**Interfaces:**
- Consumes: `cases.Case`, `db.PGEnv`, `assert.CLIResult`.
- Produces:

```go
package cliexec

// Run executes one kind:cli case against the pinned binary. pg/dbname are
// used only by db-config cases (preconditions_sql present); pass pg == nil
// to force an error if such a case appears without a DB.
func Run(c *cases.Case, bin string, pg *db.PGEnv, dbname string) (*assert.CLIResult, error)

func RenderConfigFile(m map[string]any) string // exported for tests
```

- [ ] **Step 1: Write the failing tests**

```go
func TestRenderConfigFile(t *testing.T) {
	got := RenderConfigFile(map[string]any{
		"db-max-rows":         100,
		"log-level":           "warn",
		"jwt-secret-is-base64": `"true"`, // case 1741: literal quotes in the value
		"db-channel-enabled":  true,
	})
	want := "db-channel-enabled = true\n" +
		"db-max-rows = 100\n" +
		"jwt-secret-is-base64 = \"\\\"true\\\"\"\n" +
		"log-level = \"warn\"\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderConfigFileEscapesBackslash(t *testing.T) {
	got := RenderConfigFile(map[string]any{"db-schemas": `SPECIAL "@/\#~_-`})
	want := "db-schemas = \"SPECIAL \\\"@/\\\\#~_-\"\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
```

Rendering rules: keys sorted; `string` → double-quoted with `\` → `\\` and `"` → `\"`; `bool`/int/float → bare literal; `nil` never occurs in file maps (error if it does).

Guarded end-to-end tests against the real binary (no DB):

```go
func TestDumpConfigDefaults(t *testing.T) { // shape of case 1705
	bin := os.Getenv("ORACLE_TEST_BIN")
	if bin == "" {
		t.Skip("set ORACLE_TEST_BIN")
	}
	c := &cases.Case{ID: 1705, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 || !strings.Contains(string(r.Stdout), `db-schemas = "public"`) {
		t.Fatalf("exit %d stdout %.200s", r.ExitCode, r.Stdout)
	}
}

func TestFatalEnvConfig(t *testing.T) { // shape of case 1713
	bin := os.Getenv("ORACLE_TEST_BIN")
	if bin == "" {
		t.Skip("set ORACLE_TEST_BIN")
	}
	c := &cases.Case{ID: 1713, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true, Env: map[string]string{"PGRST_DB_TX_END": "random"}}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || !strings.Contains(string(r.Stderr), "Invalid transaction termination") {
		t.Fatalf("exit %d stderr %.200s", r.ExitCode, r.Stderr)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd tools/oracle && go test ./internal/cliexec/`
Expected: FAIL (render tests), SKIP (binary tests).

- [ ] **Step 3: Implement**

`Run` logic, in order:

1. **argv**: start empty. If `c.Config.File` non-empty → `RenderConfigFile`, write to a temp file, append its path. Then the flag: if `strings.HasPrefix(c.Request.Flag, "--")` append as flag; else append as positional path (case 1719's nonexistent file). (1719 has no `file` map, so its argv is just the bogus path.)
2. **env**: `[]string{"PATH=" + os.Getenv("PATH")}` + each `k=v` from `c.Config.Env` verbatim. NO inherited `PGRST_*`/`PG*`.
3. **db-config glue** (`len(c.Config.PreconditionsSQL) > 0`): require `pg != nil` (else error). Via superuser psql against `dbname`: `DROP ROLE IF EXISTS db_config_authenticator`, then each precondition statement (`psql -c`), then `ALTER ROLE db_config_authenticator PASSWORD 'oracle_cli'`. Add to env: `PGHOST`, `PGPORT`, `PGDATABASE=<dbname>`, `PGPASSWORD=oracle_cli` (PostgREST's default `db-uri = "postgresql://"` resolves via libpq env vars; the case's own env supplies `PGUSER`). The run loop's teardown (Task 15) and `db.Teardown` both drop the role.
4. **exec**: `exec.CommandContext` with 30s timeout; capture stdout/stderr separately; `ExitCode` from `ProcessState` (timeout = error, not an exit code).
5. **dump_reparse_stable** (`c.Expect["dump_reparse_stable"] == true`): write `Stdout` to a temp file; run `bin <tmpfile> --dump-config` with env `PATH` only; store in `Redump`, set `RedumpRan`.

- [ ] **Step 4: Run to verify they pass**

Run: `cd tools/oracle && go test ./internal/cliexec/ && ORACLE_TEST_BIN="$(go run ./cmd/oracle fetch)" go test ./internal/cliexec/ -v`
Expected: render tests PASS everywhere; binary tests PASS in the second run.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/cliexec
git commit -m "feat(oracle): CLI case executor with config-file rendering and db-config glue"
```

---

### Task 14: Report (`internal/report`)

**Files:**
- Create: `tools/oracle/internal/report/report.go`, `tools/oracle/internal/report/report_test.go`

**Interfaces:**
- Produces:

```go
package report

type CaseResult struct {
	ID        int      `json:"id"`
	Feature   string   `json:"feature"`
	Area      string   `json:"area"`
	Placement string   `json:"placement"` // GroupKey or "cli"
	Pass      bool     `json:"pass"`
	Failures  []string `json:"failures,omitempty"`
	Source    string   `json:"source"`
}

func WriteJSON(path string, results []CaseResult, findings []string) error
func Summary(w io.Writer, results []CaseResult, findings []string) (allPass bool)
```

- [ ] **Step 1: Write the failing test**

```go
func TestSummary(t *testing.T) {
	var buf bytes.Buffer
	ok := Summary(&buf, []CaseResult{
		{ID: 1, Area: "filters", Pass: true},
		{ID: 2, Area: "filters", Pass: false, Failures: []string{"status: got 500, want 200"}, Source: "https://s"},
	}, []string{"variant mismatch: 1475"})
	if ok {
		t.Fatal("must report failure")
	}
	out := buf.String()
	for _, want := range []string{"case 2", "status: got 500, want 200", "https://s",
		"TOTAL 1/2", "CONTRIBUTING.md", "HARNESS finding: variant mismatch: 1475"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd tools/oracle && go test ./internal/report/`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `Summary`: per-area `passed/total` table (areas sorted); then each failing case: `case <id> (<feature>) [<placement>]`, indented failure lines, `source: <url>`; then `HARNESS finding: <line>` per finding; final line `TOTAL <passed>/<total>`. If any failure, append the routing epilogue verbatim:

  ```
  Failures are suite defects by definition (this runner tests the suite, not
  PostgREST). Route them through CONTRIBUTING.md: re-verify the citation via
  the bier-spec-audit workflow, or open delta-channel fixture work. Never
  hand-edit cases/ to make this runner pass.
  ```
- `WriteJSON`: `{"results": [...], "findings": [...], "total": n, "passed": m}` with `json.MarshalIndent`.

- [ ] **Step 4: Run to verify it passes**

Run: `cd tools/oracle && go test ./internal/report/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/oracle/internal/report
git commit -m "feat(oracle): run report with per-area summary and finding routing epilogue"
```

---

### Task 15: `run` orchestration (`cmd/oracle`)

**Files:**
- Modify: `tools/oracle/cmd/oracle/main.go`
- Create: `tools/oracle/cmd/oracle/run.go`
- Modify: `tools/oracle/Makefile` (add `run` target)

**Interfaces:**
- Consumes: everything above.
- Produces: `oracle run [-cases 1700,1701] [-areas auth,config] [-bin PATH] [-db postgrest_conf_oracle] [-report report.json] [-skip-cli] [-skip-http]` — exit 0 iff every selected case passes.

- [ ] **Step 1: Implement the orchestration**

`run.go`, structure:

```go
func cmdRun(args []string) error {
	// flag parsing (flag.NewFlagSet), repoRoot discovery, db.FromEnv()
	// bin: -bin flag, else fetchbin.Fetch(root)
	all, err := cases.LoadAll(root + "/cases")
	// filter by -cases / -areas
	placements := map[int]*route.Placement{}
	for _, c := range all { placements[c.ID], err = route.Route(c) }
	findings := route.CrossCheckHarness(placements)

	var results []report.CaseResult

	// 1) CLI cases, id order
	for _, c := range selected { if c.Request.Kind != "cli" { continue }
		r, err := cliexec.Run(c, bin, &pg, dbname) // err => result with Failures=[err]
		results = append(results, toResult(c, "cli", assert.CheckCLI(c, r)))
	}

	// 2) HTTP cases grouped by GroupKey: shared bases first (bulk, auth,
	//    multi, unicode), then variant groups ordered by smallest case id.
	//    Instances boot lazily per group and stop when the group ends
	//    (shared bases stay up for the whole run, stopped at the end).
	uri := pg.URI(dbname)
	bases := route.BaseConfigs()
	for _, g := range groupsInOrder(placements, selected) {
		inst, err := instance.Start(bin, bases[g.Base], g.Overlay, uri, g.SafeUpdate)
		if err != nil { /* every case in the group fails with the boot error */ }
		for _, c := range g.cases { // id order
			spec, err := httpexec.BuildSpec(c, placements[c.ID].InjectProfile)
			resp, err := httpexec.Do(inst.Port, spec)
			results = append(results, toResult(c, g.key, assert.CheckHTTP(c, resp)))
		}
		// stop variant instances now; keep shared bases until the end
	}

	// 3) cleanup: stop remaining instances; drop db_config_authenticator
	//    (best-effort psql, ignore error when no DB was touched)
	report.WriteJSON(reportPath, results, findings)
	if !report.Summary(os.Stdout, results, findings) {
		return fmt.Errorf("%d case(s) failed", nFailed)
	}
	return nil
}
```

Concrete requirements the sketch implies (implement all):
- A transport/executor error (connection refused, timeout, boot failure) is a case failure with the error text as the failure line — never a runner crash.
- Interrupt (SIGINT) triggers instance shutdown before exit (use `signal.NotifyContext` and defer stops).
- `groupsInOrder`: bulk, auth, multi, unicode first (only those with selected cases), then variant groups sorted by their minimum case id.
- Makefile `run:` target: `go run ./cmd/oracle run -report report.json`.

- [ ] **Step 2: Smoke-test on a narrow slice**

With `make db-up` active and DB loaded (`go run ./cmd/oracle db-setup`):

```bash
cd tools/oracle
go run ./cmd/oracle run -cases 1705,1713,1727            # CLI only, no server
go run ./cmd/oracle run -areas filters -report /dev/null # one bulk area
go run ./cmd/oracle run -cases 1003,1005,1560,1700       # unicode, multi, variant
```

Expected: each command prints a summary; CLI slice should pass outright; HTTP slices may already surface real findings — that is signal, not breakage. Verify instances shut down (no stray `postgrest` processes: `pgrep -fl postgrest`).

- [ ] **Step 3: Full-suite dry run**

Run: `go run ./cmd/oracle run -report report.json ; echo "exit: $?"`
Expected: completes without runner crashes; report written; failures (if any) listed per case. Runtime expectation: minutes, not hours.

- [ ] **Step 4: Commit**

```bash
git add tools/oracle
git commit -m "feat(oracle): run orchestration — grouped instances, full-suite execution"
```

---

### Task 16: Full local run to 762/762 — triage checkpoint

**Files:**
- Create: `tools/oracle/README.md` (usage: make targets, env vars, flags, the test-oracle naming note, and the failure-routing rule)

This task is a **human checkpoint, not a code task**. Its deliverable is a verified full run plus a triage list.

- [ ] **Step 1: Execute the full suite**

```bash
cd tools/oracle && make db-up && go run ./cmd/oracle db-setup && make run
```

- [ ] **Step 2: Write `tools/oracle/README.md`**

Cover: what the runner is (two paragraphs incl. the test-oracle naming note from `doc.go`), prerequisites (Go, docker, psql), the exact command sequence above, env vars (`PGHOST/PGPORT/PGUSER/PGPASSWORD`, `ORACLE_TEST_*` test guards), flags, and — verbatim — the rule that failures are suite defects routed via CONTRIBUTING.md and that this runner has no skip mechanism.

- [ ] **Step 3: Triage every failure — STOP for review**

For each failing case, classify into: (a) runner bug — fix in `tools/oracle` and re-run; (b) suite defect (expectation, fixture, or HARNESS.md contract gap) — collect into a findings list with the case id, observed vs expected, and the suspected root cause; (c) environment issue (DB image, extension, locale) — fix environment, re-run.

Present the findings list (b) plus the `CrossCheckHarness` output to the owner. **Do not modify `cases/`, `spec/`, `fixtures/`, or `HARNESS.md`** — those changes go through CONTRIBUTING.md's reviewed channels as separate work items decided by the owner. Iterate (a)/(c) until the only remaining failures are agreed, in-flight (b) items — the target state is 762/762 after the (b) items land upstream through review.

- [ ] **Step 4: Commit (runner fixes + README only)**

```bash
git add tools/oracle
git commit -m "docs(oracle): runner README; fixes from first full-suite run"
```

---

### Task 17: CI workflow + README claim — GATED

**Files:**
- Create: `.github/workflows/oracle.yml`
- Modify: `README.md` (machine-verified claim)

**PRECONDITION:** Task 16 reached 762/762 locally. **This task requires the owner's explicit confirmation before creating the workflow file and again before any push to main.** Do not proceed on inference.

- [ ] **Step 1: Ask the owner for confirmation to add the workflow**

- [ ] **Step 2: Write `.github/workflows/oracle.yml`**

```yaml
name: oracle
on: [push, pull_request]
jobs:
  oracle:
    runs-on: ubuntu-latest
    env:
      PGHOST: localhost
      PGPORT: "6432"
      PGUSER: postgres
      PGPASSWORD: postgres
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.23", cache-dependency-path: tools/oracle/go.sum }
      - name: Start PG 17 + PostGIS + pg-safeupdate
        run: make -C tools/oracle db-up
      - name: Load fixture chain
        run: cd tools/oracle && go run ./cmd/oracle db-setup
      - name: Fetch pinned PostgREST binary
        run: cd tools/oracle && go run ./cmd/oracle fetch
      - name: Run all 762 cases against PostgREST
        run: cd tools/oracle && go run ./cmd/oracle run -report report.json
      - name: Upload report
        if: always()
        uses: actions/upload-artifact@v4
        with: { name: oracle-report, path: tools/oracle/report.json }
```

(If Task 11 Step 2's fallback path was needed for pg-safeupdate, the `db-up` target already encodes it — the workflow needs no change.)

- [ ] **Step 3: Update `README.md`**

Add one sentence to the intro (adjust to taste at review): after the "derived from PostgREST v16.0" sentence, add: `Every case is machine-verified against real PostgREST v16.0 in CI (the internal runner under tools/oracle/ executes all 762 cases against the pinned release binary on every push).`

- [ ] **Step 4: Verify locally, then ask for confirmation to commit and push**

Run the full local suite once more (`make run` — expect 762/762), show the owner the workflow + README diff, and only after explicit approval:

```bash
git add .github/workflows/oracle.yml README.md
git commit -m "ci: oracle job — run all 762 cases against real PostgREST v16.0"
# push ONLY with explicit owner approval
```

- [ ] **Step 5: Watch the first CI run**

After the approved push: `gh run watch` (or `gh run list --workflow=oracle`); confirm green. If red for environment reasons (runner-image drift), fix and repeat Step 4's confirmation for the follow-up push.
