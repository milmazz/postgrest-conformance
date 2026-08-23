// Package report renders the runner's per-case results into the two forms
// consumers need: a human-readable summary (per-area pass/fail table,
// failing-case detail, harness findings, and a routing epilogue when any
// case failed) and a machine-readable JSON dump for downstream tooling.
//
// Every failure this package reports is, by construction, a suite defect —
// this runner tests cases/ against real PostgREST, never the other way
// around. Summary's routing epilogue exists so nobody mistakes a red run
// for license to hand-edit cases/ into passing.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

// CaseResult is one case's outcome, as recorded by the runner.
type CaseResult struct {
	ID        int      `json:"id"`
	Feature   string   `json:"feature"`
	Area      string   `json:"area"`
	Placement string   `json:"placement"` // GroupKey or "cli"
	Pass      bool     `json:"pass"`
	Failures  []string `json:"failures,omitempty"`
	Source    string   `json:"source"`
}

// routingEpilogue is appended verbatim to the summary whenever any case
// failed, so a red run always points at CONTRIBUTING.md rather than at
// cases/ itself.
const routingEpilogue = `Failures are suite defects by definition (this runner tests the suite, not
PostgREST). Route them through CONTRIBUTING.md: re-verify the citation via
the bier-spec-audit workflow, or open delta-channel fixture work. Never
hand-edit cases/ to make this runner pass.`

// areaTotals accumulates the passed/total counts for one area.
type areaTotals struct {
	passed int
	total  int
}

// Summary writes a human-readable report of results and findings to w: a
// per-area passed/total table (areas sorted), then each failing case's
// detail, then each harness finding, then the TOTAL line. It appends the
// routing epilogue whenever any case failed. It reports whether every case
// passed.
func Summary(w io.Writer, results []CaseResult, findings []string) (allPass bool) {
	areas := make(map[string]*areaTotals)
	var areaOrder []string
	passed, total := 0, 0
	for _, r := range results {
		a, ok := areas[r.Area]
		if !ok {
			a = &areaTotals{}
			areas[r.Area] = a
			areaOrder = append(areaOrder, r.Area)
		}
		a.total++
		total++
		if r.Pass {
			a.passed++
			passed++
		}
	}
	sort.Strings(areaOrder)

	for _, area := range areaOrder {
		a := areas[area]
		fmt.Fprintf(w, "%s %d/%d\n", area, a.passed, a.total)
	}

	for _, r := range results {
		if r.Pass {
			continue
		}
		fmt.Fprintf(w, "case %d (%s) [%s]\n", r.ID, r.Feature, r.Placement)
		for _, f := range r.Failures {
			fmt.Fprintf(w, "  %s\n", f)
		}
		fmt.Fprintf(w, "  source: %s\n", r.Source)
	}

	for _, f := range findings {
		fmt.Fprintf(w, "HARNESS finding: %s\n", f)
	}

	fmt.Fprintf(w, "TOTAL %d/%d\n", passed, total)

	allPass = passed == total
	if !allPass {
		fmt.Fprintln(w, routingEpilogue)
	}
	return allPass
}

// jsonReport is the shape WriteJSON serializes.
type jsonReport struct {
	Results  []CaseResult `json:"results"`
	Findings []string     `json:"findings"`
	Total    int          `json:"total"`
	Passed   int          `json:"passed"`
}

// WriteJSON writes results and findings to path as indented JSON:
// {"results": [...], "findings": [...], "total": n, "passed": m}. A nil
// results or findings slice is written as [], never null.
func WriteJSON(path string, results []CaseResult, findings []string) error {
	if results == nil {
		results = []CaseResult{}
	}
	if findings == nil {
		findings = []string{}
	}
	passed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		}
	}
	data, err := json.MarshalIndent(jsonReport{
		Results:  results,
		Findings: findings,
		Total:    len(results),
		Passed:   passed,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
