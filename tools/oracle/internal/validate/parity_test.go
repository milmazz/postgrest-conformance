package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestFullCorpusIsHealthy runs the tree check over the real repository:
// every case in cases/ must validate cleanly, exactly as tools/validate.py
// reports for the same tree.
func TestFullCorpusIsHealthy(t *testing.T) {
	res := mustTree(t, repoRoot(t))
	if len(res.Findings) != 0 {
		t.Fatalf("full corpus has findings:\n%s", strings.Join(res.Findings, "\n"))
	}
	if res.CasesChecked != 762 {
		t.Fatalf("checked %d cases, want 762", res.CasesChecked)
	}
}

// requireValidatePy skips unless python3 with pyyaml+jsonschema is
// available (same skip pattern as internal/cases's pyyaml cross-check) and
// tools/validate.py still exists — once the transition period ends and the
// script is deleted, these parity tests skip rather than fail, and can be
// deleted along with it.
func requireValidatePy(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command("python3", "-c", "import yaml, jsonschema").Run(); err != nil {
		t.Skip("pyyaml/jsonschema not available")
	}
	script := filepath.Join(repoRoot(t), "tools", "validate.py")
	if _, err := os.Stat(script); err != nil {
		t.Skip("tools/validate.py removed (transition over)")
	}
	return script
}

// runValidatePy runs tools/validate.py with the given tree as CWD and
// returns the set of flagged cases/*.yaml paths, whether any INDEX.md
// finding was reported, and whether it exited zero.
func runValidatePy(t *testing.T, script, tree string) (flagged map[string]bool, indexFindings, ok bool) {
	t.Helper()
	cmd := exec.Command("python3", script)
	cmd.Dir = tree
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			t.Fatalf("running validate.py: %v\n%s", err, out)
		}
	}
	flagged = map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, found := strings.CutPrefix(line, "cases/"); found {
			if file, _, colon := strings.Cut(rest, ":"); colon {
				flagged["cases/"+file] = true
			}
		}
		if strings.HasPrefix(line, "INDEX.md:") {
			indexFindings = true
		}
	}
	return flagged, indexFindings, err == nil
}

// goVerdicts reduces a Result to the same shape runValidatePy returns.
func goVerdicts(res Result) (flagged map[string]bool, indexFindings, ok bool) {
	flagged = map[string]bool{}
	for _, f := range res.Findings {
		if strings.HasPrefix(f, "cases/") {
			if file, _, colon := strings.Cut(f, ":"); colon {
				flagged[file] = true
			}
		}
		if strings.HasPrefix(f, "INDEX.md:") {
			indexFindings = true
		}
	}
	return flagged, indexFindings, len(res.Findings) == 0
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestParityWithValidatePyOnFullCorpus diffs both validators' verdicts over
// the real 762-case tree: both must pass, flagging nothing.
func TestParityWithValidatePyOnFullCorpus(t *testing.T) {
	script := requireValidatePy(t)
	root := repoRoot(t)

	pyFlagged, pyIndex, pyOK := runValidatePy(t, script, root)
	goFlagged, goIndex, goOK := goVerdicts(mustTree(t, root))

	if !pyOK || pyIndex || len(pyFlagged) != 0 {
		t.Fatalf("validate.py unexpectedly failed on the real tree: flagged=%v index=%v", sortedSet(pyFlagged), pyIndex)
	}
	if !goOK || goIndex || len(goFlagged) != 0 {
		t.Fatalf("Go validator unexpectedly failed on the real tree: flagged=%v index=%v", sortedSet(goFlagged), goIndex)
	}
}

// TestParityWithValidatePyOnBrokenTree builds a tree seeded with
// deliberately-broken fixtures — one per failure mode, plus tricky
// should-pass shapes — and requires both validators to flag exactly the
// same files. This is the fidelity proof for the JSON-Schema layer: any
// verdict difference between Python jsonschema and santhosh-tekuri's
// validator over these shapes fails here.
func TestParityWithValidatePyOnBrokenTree(t *testing.T) {
	script := requireValidatePy(t)
	root := miniTree(t)

	// Broken: schema violations of different kinds.
	write(t, root, "cases/0102_unknown_top_key.yaml",
		strings.Replace(validCase2, "id: 101", "id: 102\nbogus_key: true", 1))
	write(t, root, "cases/0103_missing_required.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 103", 1), "feature: demo/basic\n", "", 1))
	write(t, root, "cases/0104_wrong_id_type.yaml",
		strings.Replace(validCase2, "id: 101", `id: "one-oh-four"`, 1))
	write(t, root, "cases/0105_bad_method_enum.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 105", 1), "method: GET", "method: FETCH", 1))
	write(t, root, "cases/0106_request_shape_anyof.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 106", 1),
			"request:\n  method: GET\n  path: /items?select=name", "request:\n  headers:\n    Accept: application/json", 1))
	write(t, root, "cases/0107_expect_no_status_or_exit.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 107", 1),
			"expect:\n  status: 200", "expect:\n  headers_no_blank: true", 1))
	write(t, root, "cases/0108_status_out_of_range.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 108", 1), "status: 200", "status: 600", 1))
	write(t, root, "cases/0109_source_no_anchor.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 109", 1), "#L20", "", 1))
	// Broken: non-schema machine checks.
	write(t, root, "cases/0110_wrong_pin.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 110", 1), "/v16.0/", "/v12.2/", 1))
	write(t, root, "cases/0111_filename_mismatch.yaml",
		strings.Replace(validCase2, "id: 101", "id: 112", 1))
	write(t, root, "cases/0113_duplicate_of_100.yaml", validCase) // same id: 100 as 0100_demo_basic.yaml
	// Tricky but valid: integral float id (draft-2020-12 "integer" accepts
	// zero-fraction floats in both validators), boundary status 599.
	write(t, root, "cases/0114_integral_float_id.yaml",
		strings.Replace(validCase2, "id: 101", "id: 114.0", 1))
	write(t, root, "cases/0115_boundary_status.yaml",
		strings.Replace(strings.Replace(validCase2, "id: 101", "id: 115", 1), "status: 200", "status: 599", 1))

	// INDEX.md matches the *valid* population exactly: both validators
	// count the 6 unique ids of files that pass the schema (100, 101, 110,
	// 112, 114, 115 — the schema-invalid files are skipped before area
	// bookkeeping), all inside the 100-149 band. So neither side may emit
	// any INDEX finding — index parity here means "clean on both", not
	// "same complaint on both".
	write(t, root, "INDEX.md", `# Index

| Area | Cases | Id band |
|------|-------|---------|
| demo | 6     | 100-149 |

Total: 6 cases
`)

	pyFlagged, pyIndex, pyOK := runValidatePy(t, script, root)
	goFlagged, goIndex, goOK := goVerdicts(mustTree(t, root))

	if pyOK || goOK {
		t.Fatalf("broken tree must fail both validators: pyOK=%v goOK=%v", pyOK, goOK)
	}
	if pyIndex || goIndex {
		t.Errorf("INDEX.md must be clean on both sides (it matches the valid population): python=%v go=%v", pyIndex, goIndex)
	}
	pySet, goSet := sortedSet(pyFlagged), sortedSet(goFlagged)
	if strings.Join(pySet, ",") != strings.Join(goSet, ",") {
		t.Errorf("flagged-file sets differ:\npython: %v\ngo:     %v", pySet, goSet)
	}
	// And the flagged set is exactly the broken files.
	want := []string{
		"cases/0102_unknown_top_key.yaml",
		"cases/0103_missing_required.yaml",
		"cases/0104_wrong_id_type.yaml",
		"cases/0105_bad_method_enum.yaml",
		"cases/0106_request_shape_anyof.yaml",
		"cases/0107_expect_no_status_or_exit.yaml",
		"cases/0108_status_out_of_range.yaml",
		"cases/0109_source_no_anchor.yaml",
		"cases/0110_wrong_pin.yaml",
		"cases/0111_filename_mismatch.yaml",
		"cases/0113_duplicate_of_100.yaml",
	}
	if strings.Join(goSet, ",") != strings.Join(want, ",") {
		t.Errorf("flagged files:\ngot:  %v\nwant: %v", goSet, want)
	}
}

// TestValidatePyAgreesOnHealthyMiniTree runs validate.py over the healthy
// mini tree to prove the fixtures themselves are sound under the Python
// validator too (guards against parity tests passing vacuously because the
// "valid" fixtures were invalid on both sides).
func TestValidatePyAgreesOnHealthyMiniTree(t *testing.T) {
	script := requireValidatePy(t)
	root := miniTree(t)
	flagged, index, ok := runValidatePy(t, script, root)
	if !ok || index || len(flagged) != 0 {
		t.Fatalf("validate.py rejects the healthy mini tree: flagged=%v index=%v", sortedSet(flagged), index)
	}
}
