package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestSummaryAllPass(t *testing.T) {
	var buf bytes.Buffer
	ok := Summary(&buf, []CaseResult{
		{ID: 1, Area: "filters", Pass: true},
	}, nil)
	if !ok {
		t.Fatal("must report all-pass")
	}
	out := buf.String()
	if strings.Contains(out, "CONTRIBUTING.md") {
		t.Fatalf("all-pass summary must not include the routing epilogue:\n%s", out)
	}
	if !strings.Contains(out, "TOTAL 1/1") {
		t.Fatalf("summary missing TOTAL line:\n%s", out)
	}
}

func TestWriteJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	results := []CaseResult{
		{ID: 1, Area: "filters", Pass: true, Source: "https://s1"},
		{ID: 2, Area: "filters", Pass: false, Failures: []string{"boom"}, Source: "https://s2"},
	}
	findings := []string{"variant mismatch: 1475"}
	if err := WriteJSON(path, results, findings); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded struct {
		Results  []CaseResult `json:"results"`
		Findings []string     `json:"findings"`
		Total    int          `json:"total"`
		Passed   int          `json:"passed"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, data)
	}
	if decoded.Total != 2 || decoded.Passed != 1 {
		t.Fatalf("got total=%d passed=%d, want total=2 passed=1", decoded.Total, decoded.Passed)
	}
	if len(decoded.Results) != 2 || len(decoded.Findings) != 1 {
		t.Fatalf("got %d results, %d findings, want 2 and 1", len(decoded.Results), len(decoded.Findings))
	}
}

func TestWriteJSONEmptyProducesArraysNotNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := WriteJSON(path, nil, nil); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "null") {
		t.Fatalf("empty results/findings must marshal as [], not null:\n%s", out)
	}
	if !strings.Contains(out, `"results": []`) || !strings.Contains(out, `"findings": []`) {
		t.Fatalf("expected empty array fields:\n%s", out)
	}
}
