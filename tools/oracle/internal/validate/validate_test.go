package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from CWD to the directory containing PIN, mirroring the
// helper in internal/cases's tests.
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

const validCase = `id: 100
feature: demo/basic
schema: test
request:
  method: GET
  path: /items
expect:
  status: 200
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/test/spec/Feature/Query/QuerySpec.hs#L10
`

const validCase2 = `id: 101
feature: demo/basic
schema: test
request:
  method: GET
  path: /items?select=name
expect:
  status: 200
source: https://raw.githubusercontent.com/PostgREST/postgrest/v16.0/test/spec/Feature/Query/QuerySpec.hs#L20
`

const validIndex = `# Index

| Area | Cases | Id band |
|------|-------|---------|
| demo | 2     | 100-149 |

Total: 2 cases
`

// validHarness mirrors HARNESS.md's shape rather than INDEX.md's: the area
// table lives under a numbered section, spells its row
// "| Area | Id band(s) | Cases |" (band and count swapped relative to
// INDEX.md), and is preceded by the admonition naming the areas that
// overflowed into a 5-digit band.
//
// The §2 table exists to prove the parse is scoped: its rows are shaped so
// that reading them as area rows would raise a "ghost" finding.
const validHarness = "# Harness\n" + `
## 2. Server configuration

| Key | Value | Notes |
|---|---|---|
| ghost | 200-249 | 7 |

## 7. Areas

> **Zero areas overflow into a 5-digit band** once their primary 50-wide
> band filled: none so far.

| Area | Id band(s) | Cases | ` + "`schema:`" + ` label(s) used |
|---|---|---:|---|
| demo | 100-149 | 2 | ` + "`test`" + ` |

**Total: 2 cases across 1 areas**
`

// miniTree writes a minimal healthy suite tree (real case.schema.json, a
// PIN pinned to v16.0, two demo cases, and matching INDEX.md and HARNESS.md
// area tables) into a temp dir and returns its path. Callers mutate it to
// break specific invariants.
func miniTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	schema, err := os.ReadFile(filepath.Join(repoRoot(t), "case.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "case.schema.json", string(schema))
	write(t, root, "PIN", "postgrest: v16.0\n")
	write(t, root, "INDEX.md", validIndex)
	write(t, root, "HARNESS.md", validHarness)
	write(t, root, "cases/0100_demo_basic.yaml", validCase)
	write(t, root, "cases/0101_demo_select.yaml", validCase2)
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustTree runs Tree and fails the test on infrastructure error.
func mustTree(t *testing.T, root string) Result {
	t.Helper()
	res, err := Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestFullCorpusIsHealthy runs the tree check over the real repository:
// every case in cases/ must validate cleanly.
func TestFullCorpusIsHealthy(t *testing.T) {
	res := mustTree(t, repoRoot(t))
	if len(res.Findings) != 0 {
		t.Fatalf("full corpus has findings:\n%s", strings.Join(res.Findings, "\n"))
	}
	if res.CasesChecked != 801 {
		t.Fatalf("checked %d cases, want 801", res.CasesChecked)
	}
}

// wantFinding asserts exactly one finding containing each of the given
// substrings.
func wantFinding(t *testing.T, res Result, substrs ...string) {
	t.Helper()
	if len(res.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got %d: %v", len(res.Findings), res.Findings)
	}
	for _, s := range substrs {
		if !strings.Contains(res.Findings[0], s) {
			t.Errorf("finding %q does not contain %q", res.Findings[0], s)
		}
	}
}

func TestSchemaViolationIsReported(t *testing.T) {
	root := miniTree(t)
	// additionalProperties: false at the top level makes this a schema error.
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "id: 101", "id: 101\nbogus_key: true", 1))
	res := mustTree(t, root)
	// Like validate.py, a schema-invalid file is skipped for the rest of the
	// per-file checks, so the INDEX counts desync and report too; assert the
	// schema finding itself names the property.
	if !hasFinding(res, "0101_demo_select.yaml", "schema:", "bogus_key") {
		t.Fatalf("schema finding not found: %v", res.Findings)
	}
	if strings.Contains(strings.Join(res.Findings, "\n"), "file://") {
		t.Fatalf("schema finding leaks a file:// URL: %v", res.Findings)
	}
}

func TestMissingRequiredKeyIsSchemaError(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "feature: demo/basic\n", "", 1))
	// Removing the case from an area breaks the INDEX count too, so restore
	// the balance by only asserting on the schema finding's presence.
	res := mustTree(t, root)
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f, "0101_demo_select.yaml") && strings.Contains(f, "schema:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no schema finding for the file missing `feature:`; findings: %v", res.Findings)
	}
}

func TestUnparseableYAMLIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", "id: 101\n  bad indent: [unclosed\n")
	res := mustTree(t, root)
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f, "0101_demo_select.yaml") {
			found = true
			// The finding is prefixed with the tree-relative path; the
			// parser error's own absolute path must not repeat after it.
			if strings.Contains(f, root) {
				t.Errorf("finding repeats the absolute path: %q", f)
			}
		}
	}
	if !found {
		t.Fatalf("no finding for unparseable YAML; findings: %v", res.Findings)
	}
}

func TestDuplicateIDIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", validCase) // same id: 100 as 0100_demo_basic.yaml
	res := mustTree(t, root)
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f, "duplicate id 100") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no duplicate-id finding; findings: %v", res.Findings)
	}
}

func TestFilenamePrefixMismatchIsReported(t *testing.T) {
	root := miniTree(t)
	if err := os.Rename(filepath.Join(root, "cases/0101_demo_select.yaml"), filepath.Join(root, "cases/0999_demo_select.yaml")); err != nil {
		t.Fatal(err)
	}
	res := mustTree(t, root)
	wantFinding(t, res, "0999_demo_select.yaml", "filename prefix != id 101")
}

func TestSourceNotMatchingPinIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "/v16.0/", "/v12.2/", 1))
	res := mustTree(t, root)
	wantFinding(t, res, "0101_demo_select.yaml", "source citation", "v16.0")
}

func TestSourceMissingLineAnchorIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "#L20", "", 1))
	res := mustTree(t, root)
	// The schema's own `pattern` also requires the #L anchor, so this may
	// surface as a schema finding, a citation finding, or both — either way
	// the file must be flagged.
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f, "0101_demo_select.yaml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no finding for missing #L anchor; findings: %v", res.Findings)
	}
}

func TestUnknownExpectKeyIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "  status: 200", "  status: 200\n  body_exactt: {}", 1))
	res := mustTree(t, root)
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f, "0101_demo_select.yaml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no finding for unknown expect key; findings: %v", res.Findings)
	}
}

func TestMissingPINIsHardError(t *testing.T) {
	root := miniTree(t)
	if err := os.Remove(filepath.Join(root, "PIN")); err != nil {
		t.Fatal(err)
	}
	if _, err := Tree(root); err == nil {
		t.Fatal("want a hard error for missing PIN, got nil")
	}
}

// hasFinding reports whether any finding contains all the given substrings.
func hasFinding(res Result, substrs ...string) bool {
	for _, f := range res.Findings {
		ok := true
		for _, s := range substrs {
			if !strings.Contains(f, s) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestIndexMissingAreaRowIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", strings.Replace(validIndex, "| demo | 2     | 100-149 |\n", "", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "no id-band table row for area", "demo") {
		t.Fatalf("missing-area-row finding not found: %v", res.Findings)
	}
}

func TestIndexCountMismatchIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", strings.Replace(validIndex, "| demo | 2 ", "| demo | 3 ", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "claims 3 cases, 2 found on disk") {
		t.Fatalf("count-mismatch finding not found: %v", res.Findings)
	}
}

func TestIndexOutOfBandIDIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/0101_demo_select.yaml", strings.Replace(validCase2, "id: 101", "id: 999", 1))
	if err := os.Rename(filepath.Join(root, "cases/0101_demo_select.yaml"), filepath.Join(root, "cases/0999_demo_select.yaml")); err != nil {
		t.Fatal(err)
	}
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "outside its declared band(s)", "999") {
		t.Fatalf("out-of-band finding not found: %v", res.Findings)
	}
}

func TestIndexGhostAreaIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", strings.Replace(validIndex,
		"| demo | 2     | 100-149 |",
		"| demo | 2     | 100-149 |\n| ghost | 0     | 200-249 |", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "ghost", "no case on disk") {
		t.Fatalf("ghost-area finding not found: %v", res.Findings)
	}
	// The ghost row's 0 also desyncs the summed total from... no: 2+0=2
	// still matches, so the ghost row must be the only finding.
	if len(res.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got: %v", res.Findings)
	}
}

func TestIndexTotalLineMismatchIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", strings.Replace(validIndex, "Total: 2 cases", "Total: 5 cases", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "'Total:' line claims 5", "2 found on disk") {
		t.Fatalf("total-line finding not found: %v", res.Findings)
	}
}

func TestIndexMissingTotalLineIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", strings.Replace(validIndex, "Total: 2 cases\n", "", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "could not find the 'Total: N cases' summary line") {
		t.Fatalf("missing-total-line finding not found: %v", res.Findings)
	}
}

func TestIndexUnparseableTableIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "INDEX.md", "# Index\n\nNo table here.\n\nTotal: 2 cases\n")
	res := mustTree(t, root)
	if !hasFinding(res, "INDEX.md", "0 rows matched") {
		t.Fatalf("unparseable-table finding not found: %v", res.Findings)
	}
}

func TestIndexOverflowBandsAndBoldMarkupAccepted(t *testing.T) {
	root := miniTree(t)
	// A second "overflow" band after '+', bold cells, and a single-id band
	// row — all shapes validate.py's parser accepts.
	write(t, root, "cases/11800_demo_overflow.yaml", strings.Replace(validCase2, "id: 101", "id: 11800", 1))
	write(t, root, "INDEX.md", `# Index

| Area | Cases | Id band |
|------|-------|---------|
| **demo** | **3** | 100-149 + 11800-11849 |

Total: **3 cases**
`)
	write(t, root, "HARNESS.md", harnessWith(overflowDemo))
	res := mustTree(t, root)
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got: %v", res.Findings)
	}
}

// overflowDemo is validHarness's §7 rewritten for a demo area that has
// spilled into a 5-digit band: one overflow area, named by the note.
const overflowDemo = `> **One areas overflow into a 5-digit band** once their primary 50-wide
> band filled: ` + "`demo`" + ` (11800+).

| Area | Id band(s) | Cases | ` + "`schema:`" + ` label(s) used |
|---|---|---:|---|
| demo | 100-149, 11800-11849 | 3 | ` + "`test`" + ` |

**Total: 3 cases across 1 areas**
`

// harnessWith returns validHarness with everything after the "## 7. Areas"
// heading replaced by body, so a test can vary just the area section.
func harnessWith(body string) string {
	const heading = "## 7. Areas\n\n"
	return validHarness[:strings.Index(validHarness, heading)+len(heading)] + body
}

func TestHarnessCountMismatchIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "HARNESS.md", strings.Replace(validHarness, "| demo | 100-149 | 2 |", "| demo | 100-149 | 3 |", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "claims 3 cases, 2 found on disk") {
		t.Fatalf("count-mismatch finding not found: %v", res.Findings)
	}
	// INDEX.md still agrees with disk: the two tables are checked
	// independently, so breaking one must not implicate the other.
	if hasFinding(res, "INDEX.md") {
		t.Fatalf("INDEX.md is unchanged and should be clean: %v", res.Findings)
	}
}

func TestHarnessAreaTableIsCheckedIndependentlyOfIndex(t *testing.T) {
	root := miniTree(t)
	// The 762 -> 801 pass's actual failure mode: INDEX.md refreshed,
	// HARNESS.md left behind. Nothing caught it while only INDEX.md was
	// parsed.
	write(t, root, "cases/0102_demo_third.yaml", strings.Replace(validCase2, "id: 101", "id: 102", 1))
	write(t, root, "INDEX.md", strings.NewReplacer(
		"| demo | 2     | 100-149 |", "| demo | 3     | 100-149 |",
		"Total: 2 cases", "Total: 3 cases",
	).Replace(validIndex))
	res := mustTree(t, root)
	if hasFinding(res, "INDEX.md") {
		t.Fatalf("INDEX.md was updated and should be clean: %v", res.Findings)
	}
	if !hasFinding(res, "HARNESS.md", "claims 2 cases, 3 found on disk") {
		t.Fatalf("stale-HARNESS finding not found: %v", res.Findings)
	}
}

func TestHarnessTotalBreakdownIsChecked(t *testing.T) {
	root := miniTree(t)
	// A breakdown whose addends do not reach its own stated total.
	write(t, root, "HARNESS.md", strings.Replace(validHarness,
		"**Total: 2 cases across 1 areas**",
		"**Total: 2 cases across 1 areas** (1+2 = 2)", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "breakdown adds up to 3, not the 2 it states") {
		t.Fatalf("breakdown-arithmetic finding not found: %v", res.Findings)
	}
}

func TestHarnessTotalBreakdownDisagreeingWithDiskIsReported(t *testing.T) {
	root := miniTree(t)
	// Internally consistent arithmetic, wrong answer for the tree on disk.
	write(t, root, "HARNESS.md", strings.Replace(validHarness,
		"**Total: 2 cases across 1 areas**",
		"**Total: 2 cases across 1 areas** (2+3 = 5)", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "breakdown claims 5 cases, 2 found on disk") {
		t.Fatalf("breakdown-vs-disk finding not found: %v", res.Findings)
	}
}

func TestHarnessAreaCountInTotalLineIsChecked(t *testing.T) {
	root := miniTree(t)
	write(t, root, "HARNESS.md", strings.Replace(validHarness, "across 1 areas", "across 4 areas", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "claims 4 areas, 1 found on disk") {
		t.Fatalf("area-count finding not found: %v", res.Findings)
	}
}

func TestHarnessOverflowNoteCountIsChecked(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/11800_demo_overflow.yaml", strings.Replace(validCase2, "id: 101", "id: 11800", 1))
	write(t, root, "INDEX.md", strings.NewReplacer(
		"| demo | 2     | 100-149 |", "| demo | 3     | 100-149, 11800-11849 |",
		"Total: 2 cases", "Total: 3 cases",
	).Replace(validIndex))
	// The table gained an overflow band; the note still counts zero — the
	// "Four areas overflow" drift, in miniature.
	write(t, root, "HARNESS.md", harnessWith(strings.Replace(overflowDemo,
		"**One areas overflow into a 5-digit band**", "**Zero areas overflow into a 5-digit band**", 1)))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", `overflow note says "zero" areas`, "the table shows 1 (one)") {
		t.Fatalf("overflow-count finding not found: %v", res.Findings)
	}
}

func TestHarnessOverflowNoteMissingAreaIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/11800_demo_overflow.yaml", strings.Replace(validCase2, "id: 101", "id: 11800", 1))
	write(t, root, "INDEX.md", strings.NewReplacer(
		"| demo | 2     | 100-149 |", "| demo | 3     | 100-149, 11800-11849 |",
		"Total: 2 cases", "Total: 3 cases",
	).Replace(validIndex))
	write(t, root, "HARNESS.md", harnessWith(strings.Replace(overflowDemo,
		"`demo` (11800+)", "`other` (11800+)", 1)))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", `does not name area "demo"`, "5-digit band 11800+") {
		t.Fatalf("unnamed-overflow-area finding not found: %v", res.Findings)
	}
	if !hasFinding(res, "HARNESS.md", `names area "other"`, "declares no 5-digit band") {
		t.Fatalf("phantom-overflow-area finding not found: %v", res.Findings)
	}
}

func TestHarnessOverflowNoteWrongBandIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "cases/11800_demo_overflow.yaml", strings.Replace(validCase2, "id: 101", "id: 11800", 1))
	write(t, root, "INDEX.md", strings.NewReplacer(
		"| demo | 2     | 100-149 |", "| demo | 3     | 100-149, 11800-11849 |",
		"Total: 2 cases", "Total: 3 cases",
	).Replace(validIndex))
	write(t, root, "HARNESS.md", harnessWith(strings.Replace(overflowDemo,
		"`demo` (11800+)", "`demo` (12400+)", 1)))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", `gives area "demo" the band 12400+`, "declares 11800+") {
		t.Fatalf("wrong-band finding not found: %v", res.Findings)
	}
}

func TestHarnessMissingOverflowNoteIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "HARNESS.md", strings.Replace(validHarness,
		"> **Zero areas overflow into a 5-digit band** once their primary 50-wide\n> band filled: none so far.\n", "", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "could not find the", "areas overflow into a 5-digit band") {
		t.Fatalf("missing-note finding not found: %v", res.Findings)
	}
}

func TestHarnessMissingAreaSectionIsReported(t *testing.T) {
	root := miniTree(t)
	write(t, root, "HARNESS.md", strings.Replace(validHarness, "## 7. Areas", "## 7. Regions", 1))
	res := mustTree(t, root)
	if !hasFinding(res, "HARNESS.md", "could not find the area-table section") {
		t.Fatalf("missing-section finding not found: %v", res.Findings)
	}
	// The section is the whole check; a missing one must not also cascade
	// into per-area or total findings.
	if len(res.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got: %v", res.Findings)
	}
}

func TestHarnessBandParentheticalIsNotReadAsBound(t *testing.T) {
	root := miniTree(t)
	// HARNESS.md annotates mutations' band "11400-11405, 11407-11415 (no
	// 11406)". The parenthetical's digits are commentary on the range, not
	// bounds: reading them as bounds drops the range entirely and reports
	// every id in it as out-of-band.
	write(t, root, "cases/11407_demo_gap.yaml", strings.Replace(validCase2, "id: 101", "id: 11407", 1))
	write(t, root, "INDEX.md", strings.NewReplacer(
		"| demo | 2     | 100-149 |", "| demo | 3     | 100-149, 11407-11415 |",
		"Total: 2 cases", "Total: 3 cases",
	).Replace(validIndex))
	write(t, root, "HARNESS.md", harnessWith(`> **One areas overflow into a 5-digit band** once their primary 50-wide
> band filled: `+"`demo`"+` (11407+).

| Area | Id band(s) | Cases | `+"`schema:`"+` label(s) used |
|---|---|---:|---|
| demo | 100-149, 11407-11415 (no 11406) | 3 | `+"`test`"+` |

**Total: 3 cases across 1 areas**
`))
	res := mustTree(t, root)
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got: %v", res.Findings)
	}
}

func TestMissingHarnessIsHardError(t *testing.T) {
	root := miniTree(t)
	if err := os.Remove(filepath.Join(root, "HARNESS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Tree(root); err == nil {
		t.Fatal("want a hard error for missing HARNESS.md, got nil")
	}
}

func TestMissingIndexIsHardError(t *testing.T) {
	root := miniTree(t)
	if err := os.Remove(filepath.Join(root, "INDEX.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Tree(root); err == nil {
		t.Fatal("want a hard error for missing INDEX.md, got nil")
	}
}

func TestHealthyTreeHasNoFindings(t *testing.T) {
	res := mustTree(t, miniTree(t))
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got: %v", res.Findings)
	}
	if res.CasesChecked != 2 {
		t.Fatalf("want 2 cases checked, got %d", res.CasesChecked)
	}
}
