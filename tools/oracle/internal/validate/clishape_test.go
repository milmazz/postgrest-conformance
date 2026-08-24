package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI-set claims (how many `request.kind: cli` cases there are, which
// ids they are, and how the `request.flag` values distribute) are restated
// in prose across four documents. None sits in an area table, so
// checkAreaTables cannot see them — which is how five went stale in a
// single pass while `oracle validate` stayed green (issue #21). These tests
// pin each site.
//
// The fixture is its own tree rather than miniTree's: it needs CLI cases
// and the four claim documents, and keeping it separate leaves the area
// table tests reading the fixture they were written against.

const cliHTTPCase = `id: 100
feature: demo/basic
schema: test
request:
  method: GET
  path: /items
expect:
  status: 200
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/test/spec/Feature/Query/QuerySpec.hs#L10
`

// 103 is HTTP and sits between the CLI ids on purpose, so the fixture's CLI
// set (102 plus 104-105) is non-contiguous the way the real one is.
const cliHTTPCase2 = `id: 103
feature: demo/basic
schema: test
request:
  method: GET
  path: /items?select=name
expect:
  status: 200
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/test/spec/Feature/Query/QuerySpec.hs#L20
`

const cliDumpConfigCase = `id: 102
feature: demo/cli/dump
schema: test
request:
  kind: cli
  flag: "--dump-config"
expect:
  exit_code: 0
  dump_contains:
    - 'server-port = 3000'
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/src/library/PostgREST/Config.hs#L161
`

const cliReadyCase = `id: 104
feature: demo/cli/ready
schema: test
request:
  kind: cli
  flag: "--ready"
expect:
  exit_code: nonzero
  stderr_contains: "ERROR: Admin server is not running."
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/src/library/PostgREST/Client.hs#L94
`

const cliExampleCase = `id: 105
feature: demo/cli/example
schema: test
request:
  kind: cli
  flag: "--example"
expect:
  exit_code: 0
  dump_contains:
    - 'db-uri = "postgresql://"'
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/src/library/PostgREST/Config.hs#L677
`

const cliIndex = `# Index

| Area | Cases | Id band |
|------|-------|---------|
| demo | 5     | 100-149 |

Total: 5 cases

## Case file shapes

Most cases are HTTP request/response (**2**). The **demo** area additionally
uses a **CLI** shape (` + "`request.kind: cli`" + `) asserting on
` + "`expect.exit_code`" + `
rather than an HTTP status — **3** cases, ids
**102 plus 104-105**. Note the CLI ids are *not* one contiguous run:
**103 is HTTP**, so ` + "`102-105`" + ` is the band, not the CLI set.
` + "`request.flag`" + ` carries three flag
values — ` + "`\"--dump-config\"`" + ` (**1** cases), ` + "`\"--ready\"`" + ` (**1**: 104, added
2026-08-24) and ` + "`\"--example\"`" + ` (**1**: 105).
`

const cliHarness = `# Harness

## 4. Assertion semantics

  The CLI set is **not** all ` + "`--dump-config`" + `. ` + "`request.flag`" + ` takes three
  values — ` + "`--dump-config`" + ` (1 cases), ` + "`--ready`" + ` (1) and ` + "`--example`" + ` (1) —
  plus nothing else.

## 7. Areas

> **Zero areas overflow into a 5-digit band** once their primary 50-wide
> band filled: none so far.

| Area | Id band(s) | Cases | ` + "`schema:`" + ` label(s) used |
|---|---|---:|---|
| demo | 100-149 | 5 | ` + "`test`" + ` |

**Total: 5 cases across 1 areas**
`

// The two COVERAGE.md sites spell the id set differently on purpose: the
// mapping row uses "+" with a dated parenthetical, the scope bullet uses
// "plus". The trailing dated snapshot restates an OLD set and must not be
// read as a live claim.
const cliCoverage = `# Coverage

| Docs page | Covering case ids | Notes |
|---|---|---|
| ` + "`cli`" + ` (CLI) | 102 + 104, **105 (new 2026-08-24)** (all 3 ` + "`request.kind: cli`" + ` cases) | flags. |

- **` + "`cli`" + ` — COVERED.** Neither half survives contact with the tree: the CLI set is now
  **102 plus 104-105 (3 cases)**, and no spec case carries ` + "`pending`" + `.

*(Re-verified a sixth time at the 668-case state: the CLI set is
**102 + 104 = 2**; this dated snapshot is history and must not be flagged.)*
`

const cliSpecReadme = `# Spec

- **CLI** (demo CLI behavior, 3 cases — all of them in ` + "`demo`" + `,
  ids **102 plus 104-105**; the band is *not* contiguous, because
  103 is HTTP): ` + "`request.kind: cli`" + ` with
  ` + "`request.flag`" + ` one of ` + "`\"--dump-config\"`" + ` (1 cases), ` + "`\"--ready\"`" + ` (1:
  104) or ` + "`\"--example\"`" + ` (1: 105).
`

// cliTree writes a healthy tree whose CLI-set claims all agree with disk.
func cliTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	schema, err := os.ReadFile(filepath.Join(repoRoot(t), "case.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "case.schema.json", string(schema))
	write(t, root, "PIN", "postgrest: v16.0\n")
	write(t, root, "INDEX.md", cliIndex)
	write(t, root, "HARNESS.md", cliHarness)
	write(t, root, "COVERAGE.md", cliCoverage)
	write(t, root, "spec/README.md", cliSpecReadme)
	write(t, root, "cases/0100_demo_basic.yaml", cliHTTPCase)
	write(t, root, "cases/0103_demo_select.yaml", cliHTTPCase2)
	write(t, root, "cases/0102_demo_cli_dump.yaml", cliDumpConfigCase)
	write(t, root, "cases/0104_demo_cli_ready.yaml", cliReadyCase)
	write(t, root, "cases/0105_demo_cli_example.yaml", cliExampleCase)
	return root
}

func TestCLIHealthyTreeHasNoFindings(t *testing.T) {
	res := mustTree(t, cliTree(t))
	if len(res.Findings) != 0 {
		t.Fatalf("healthy CLI tree has findings:\n%s", strings.Join(res.Findings, "\n"))
	}
}

// The dated per-pass snapshot restates an older CLI set on purpose.
func TestDatedCLISnapshotIsNotFlagged(t *testing.T) {
	res := mustTree(t, cliTree(t))
	for _, f := range res.Findings {
		if strings.Contains(f, "668-case state") || strings.Contains(f, "= 2") {
			t.Fatalf("dated snapshot flagged as a live claim: %s", f)
		}
	}
}

func TestCLICountMismatchIsReported(t *testing.T) {
	root := cliTree(t)
	write(t, root, "INDEX.md", strings.Replace(cliIndex,
		"— **3** cases, ids", "— **4** cases, ids", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "INDEX.md", "says 4 CLI cases", "the tree has 3")
}

func TestCLIIdListMismatchIsReported(t *testing.T) {
	root := cliTree(t)
	write(t, root, "spec/README.md", strings.Replace(cliSpecReadme,
		"ids **102 plus 104-105**", "ids **102 plus 104**", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "spec/README.md", "omits CLI cases on disk: 105")
}

func TestCLIIdListNamingANonCLICaseIsReported(t *testing.T) {
	root := cliTree(t)
	// 103 is an HTTP case, not a CLI one.
	write(t, root, "spec/README.md", strings.Replace(cliSpecReadme,
		"ids **102 plus 104-105**", "ids **102 plus 103-105**", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "spec/README.md", "names ids that are not CLI cases: 103")
}

func TestCLIFlagHistogramMismatchIsReported(t *testing.T) {
	root := cliTree(t)
	write(t, root, "HARNESS.md", strings.Replace(cliHarness,
		"`--ready` (1) and", "`--ready` (2) and", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "HARNESS.md", "says 2 --ready cases", "the tree has 1")
}

// A claim reworded past its anchor must fail loudly rather than silently
// stop being checked: a guard that quietly stops guarding leaves the
// document looking verified when it is not.
func TestCLIClaimSiteThatNoLongerMatchesIsReported(t *testing.T) {
	root := cliTree(t)
	write(t, root, "COVERAGE.md", strings.Replace(cliCoverage,
		"the CLI set is now\n  **102 plus 104-105 (3 cases)**",
		"the CLI set has been reworded into prose stating nothing", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "COVERAGE.md", "could not find", "scope-decision bullet")
}

func TestCLIMappingRowIdCellIsChecked(t *testing.T) {
	root := cliTree(t)
	write(t, root, "COVERAGE.md", strings.Replace(cliCoverage,
		"| 102 + 104, **105 (new 2026-08-24)**",
		"| 102 + 104, **106 (new 2026-08-24)**", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "COVERAGE.md", "mapping row", "105", "106")
}

// A tree with no CLI cases has no CLI set to claim; the check is a no-op
// there rather than demanding claim sites the documents need not carry.
func TestTreeWithNoCLICasesSkipsTheCheck(t *testing.T) {
	res := mustTree(t, miniTree(t))
	for _, f := range res.Findings {
		if strings.Contains(f, "CLI") {
			t.Fatalf("CLI finding on a tree with no CLI cases: %s", f)
		}
	}
}
