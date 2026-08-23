package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/route"
)

// httpTestCase returns a minimal case for tests that only care about id
// (and, optionally, area).
func httpTestCase(id int, area string) *cases.Case {
	return &cases.Case{ID: id, Area: area}
}

// httpPlacement returns a minimal "http" placement routed to base with the
// given group key.
func httpPlacement(base, groupKey string) *route.Placement {
	return &route.Placement{Kind: "http", Base: base, GroupKey: groupKey}
}

func groupKeys(groups []*httpGroup) []string {
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.key)
	}
	return keys
}

func caseIDs(cs []*cases.Case) []int {
	var ids []int
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestGroupsInOrder(t *testing.T) {
	t.Run("shared bases first in sharedGroupOrder order regardless of input order", func(t *testing.T) {
		// Global ascending id order with groups interleaved — mirrors the
		// real invariant that `selected` is always id-sorted (cases.LoadAll
		// sorts, filterCases preserves order), with different groups'
		// cases appearing in whatever order their ids happen to fall.
		selected := []*cases.Case{
			httpTestCase(1, "unicode-area"),
			httpTestCase(2, "auth-area"),
			httpTestCase(3, "test-area"),
			httpTestCase(4, "auth-area"),
			httpTestCase(5, "test-area"),
			httpTestCase(6, "multi-area"),
			httpTestCase(7, "unicode-area"),
			httpTestCase(8, "test-area"),
		}
		placements := map[int]*route.Placement{
			1: httpPlacement("unicode", "unicode"),
			2: httpPlacement("auth", "auth"),
			3: httpPlacement("test", "test"),
			4: httpPlacement("auth", "auth"),
			5: httpPlacement("test", "test"),
			6: httpPlacement("multi", "multi"),
			7: httpPlacement("unicode", "unicode"),
			8: httpPlacement("test", "test"),
		}

		got := groupsInOrder(placements, selected)

		wantKeys := []string{"test", "auth", "multi", "unicode"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}

		// cases within each group keep ascending id order
		wantIDs := map[string][]int{
			"test":    {3, 5, 8},
			"auth":    {2, 4},
			"multi":   {6},
			"unicode": {1, 7},
		}
		for _, g := range got {
			if ids := caseIDs(g.cases); !reflect.DeepEqual(ids, wantIDs[g.key]) {
				t.Errorf("group %q: case ids = %v, want %v", g.key, ids, wantIDs[g.key])
			}
		}
	})

	t.Run("shared area bases sort alphabetically between test and auth", func(t *testing.T) {
		selected := []*cases.Case{
			httpTestCase(1, "rpc-area"),
			httpTestCase(2, "config-area"),
			httpTestCase(3, "test-area"),
			httpTestCase(4, "operators-area"),
		}
		placements := map[int]*route.Placement{
			1: httpPlacement("rpc", "rpc"),
			2: httpPlacement("config", "config"),
			3: httpPlacement("test", "test"),
			4: httpPlacement("operators", "operators"),
		}

		got := groupsInOrder(placements, selected)

		// "test" always leads; the remaining area bases present come out
		// alphabetically (config, operators, rpc), not input order.
		wantKeys := []string{"test", "config", "operators", "rpc"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}
	})

	t.Run("variant groups sorted by min case id, after shared groups", func(t *testing.T) {
		selected := []*cases.Case{
			httpTestCase(100, "v1"),
			httpTestCase(10, "v2"),
			httpTestCase(50, "v3"),
			httpTestCase(1, "test-area"),
		}
		placements := map[int]*route.Placement{
			100: httpPlacement("test", "test|A=1"),
			10:  httpPlacement("auth", "auth|B=2"),
			50:  httpPlacement("mutations", "mutations+safeupdate"),
			1:   httpPlacement("test", "test"),
		}

		got := groupsInOrder(placements, selected)

		// "test" is the only shared group present, so it comes first;
		// the three variant groups follow, ordered by their minimum id
		// (10, 50, 100) rather than by input order or key.
		wantKeys := []string{"test", "auth|B=2", "mutations+safeupdate", "test|A=1"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}
	})

	t.Run("missing shared bases order correctly", func(t *testing.T) {
		selected := []*cases.Case{
			httpTestCase(1, "unicode-area"),
			httpTestCase(2, "auth-area"),
		}
		placements := map[int]*route.Placement{
			1: httpPlacement("unicode", "unicode"),
			2: httpPlacement("auth", "auth"),
		}

		got := groupsInOrder(placements, selected)

		// Every other shared base is absent from the selection entirely;
		// auth/unicode must still come out in their relative
		// sharedGroupOrder positions, not input order (unicode's case
		// appears first here).
		wantKeys := []string{"auth", "unicode"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}
	})

	t.Run("non-http placements are excluded", func(t *testing.T) {
		selected := []*cases.Case{httpTestCase(1, "x"), httpTestCase(2, "y")}
		placements := map[int]*route.Placement{
			1: {Kind: "cli"},
			2: httpPlacement("test", "test"),
		}

		got := groupsInOrder(placements, selected)

		if len(got) != 1 || got[0].key != "test" || !reflect.DeepEqual(caseIDs(got[0].cases), []int{2}) {
			t.Fatalf("got %+v, want a single test group containing only case 2", got)
		}
	})
}

func TestIsSharedGroupKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"test", true},
		{"config", true},
		{"domain_representations", true},
		{"headers", true},
		{"mutations", true},
		{"observability", true},
		{"operators", true},
		{"ordering", true},
		{"pagination", true},
		{"representations", true},
		{"rpc", true},
		{"auth", true},
		{"multi", true},
		{"unicode", true},
		{"mutations+safeupdate", false},
		{"config|PGRST_DB_MAX_ROWS=2", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := isSharedGroupKey(tc.key); got != tc.want {
			t.Errorf("isSharedGroupKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestSharedGroupRank(t *testing.T) {
	tests := []struct {
		key  string
		want int
	}{
		{"test", 0},
		{"config", 1},
		{"domain_representations", 2},
		{"headers", 3},
		{"mutations", 4},
		{"observability", 5},
		{"operators", 6},
		{"ordering", 7},
		{"pagination", 8},
		{"representations", 9},
		{"rpc", 10},
		{"auth", 11},
		{"multi", 12},
		{"unicode", 13},
		{"mutations+safeupdate", len(sharedGroupOrder)},
		{"anything-else", len(sharedGroupOrder)},
	}
	for _, tc := range tests {
		if got := sharedGroupRank(tc.key); got != tc.want {
			t.Errorf("sharedGroupRank(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestMinCaseID(t *testing.T) {
	g := &httpGroup{cases: []*cases.Case{{ID: 5}, {ID: 2}, {ID: 9}, {ID: 2}}}
	if got := minCaseID(g); got != 2 {
		t.Errorf("minCaseID = %d, want 2", got)
	}

	single := &httpGroup{cases: []*cases.Case{{ID: 42}}}
	if got := minCaseID(single); got != 42 {
		t.Errorf("minCaseID(single) = %d, want 42", got)
	}
}

func TestWithConnUserAnon(t *testing.T) {
	for _, baseName := range []string{"test", "operators", "mutations", "multi", "unicode"} {
		t.Run(baseName, func(t *testing.T) {
			orig := map[string]string{"PGRST_DB_SCHEMAS": "test"}
			origSnapshot := map[string]string{"PGRST_DB_SCHEMAS": "test"}

			got := withConnUserAnon(orig, baseName, "postgres")

			if got["PGRST_DB_ANON_ROLE"] != "postgres" {
				t.Errorf("PGRST_DB_ANON_ROLE = %q, want %q", got["PGRST_DB_ANON_ROLE"], "postgres")
			}
			if got["PGRST_DB_SCHEMAS"] != "test" {
				t.Errorf("PGRST_DB_SCHEMAS not preserved: %q", got["PGRST_DB_SCHEMAS"])
			}
			if !reflect.DeepEqual(orig, origSnapshot) {
				t.Fatalf("original base map mutated: got %+v, want %+v", orig, origSnapshot)
			}

			// The returned map must be a distinct copy, not an alias of
			// orig — mutating it must not affect the original, since the
			// same base map object is shared by every group routed to the
			// same base (including variants layered on top of it).
			got["PGRST_DB_SCHEMAS"] = "mutated"
			if orig["PGRST_DB_SCHEMAS"] != "test" {
				t.Errorf("returned map aliases the original: mutating it changed orig to %q", orig["PGRST_DB_SCHEMAS"])
			}
		})
	}

	t.Run("auth", func(t *testing.T) {
		orig := map[string]string{"PGRST_DB_SCHEMAS": "test"}
		got := withConnUserAnon(orig, "auth", "postgres")

		if _, ok := got["PGRST_DB_ANON_ROLE"]; ok {
			t.Errorf("auth base must not get PGRST_DB_ANON_ROLE injected, got %+v", got)
		}
		if !reflect.DeepEqual(got, orig) {
			t.Errorf("auth base map should be unchanged: got %+v, want %+v", got, orig)
		}
	})
}

func TestParseIDSet(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[int]bool
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"blank", "   ", nil, false},
		{"single", "5", map[int]bool{5: true}, false},
		{"multiple", "1,2,3", map[int]bool{1: true, 2: true, 3: true}, false},
		{"whitespace and trailing comma", " 1 , 2, ", map[int]bool{1: true, 2: true}, false},
		{"invalid entry", "1,abc,3", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIDSet(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseIDSet(%q): want error, got nil (result %v)", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIDSet(%q): unexpected error: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseIDSet(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseStringSet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]bool
	}{
		{"empty", "", nil},
		{"blank", "   ", nil},
		{"single", "filters", map[string]bool{"filters": true}},
		{"multiple with whitespace and trailing comma", " filters , auth ,", map[string]bool{"filters": true, "auth": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseStringSet(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseStringSet(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFilterCases(t *testing.T) {
	all := []*cases.Case{
		{ID: 1, Area: "filters"},
		{ID: 2, Area: "auth"},
		{ID: 3, Area: "filters"},
	}

	t.Run("both nil selects everything", func(t *testing.T) {
		got := filterCases(all, nil, nil)
		if !reflect.DeepEqual(got, all) {
			t.Errorf("got %+v, want %+v", got, all)
		}
	})

	t.Run("filter by ids", func(t *testing.T) {
		got := filterCases(all, map[int]bool{1: true, 3: true}, nil)
		if ids := caseIDs(got); !reflect.DeepEqual(ids, []int{1, 3}) {
			t.Errorf("got ids %v, want [1 3]", ids)
		}
	})

	t.Run("filter by areas", func(t *testing.T) {
		got := filterCases(all, nil, map[string]bool{"auth": true})
		if len(got) != 1 || got[0].ID != 2 {
			t.Fatalf("got %+v, want just case 2", got)
		}
	})

	t.Run("ids and areas narrow each other", func(t *testing.T) {
		// case 2 matches the id filter but not the area filter, so it's
		// excluded; both conditions must hold (AND, not OR).
		got := filterCases(all, map[int]bool{2: true, 3: true}, map[string]bool{"filters": true})
		if len(got) != 1 || got[0].ID != 3 {
			t.Fatalf("got %+v, want just case 3", got)
		}
	})

	t.Run("unknown id yields an empty, non-nil slice", func(t *testing.T) {
		got := filterCases(all, map[int]bool{999: true}, nil)
		if got == nil {
			t.Fatal("got nil, want an empty non-nil slice")
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})
}

func TestCheckSelection(t *testing.T) {
	t.Run("empty selection is an error", func(t *testing.T) {
		if err := checkSelection(nil); err == nil {
			t.Fatal("got nil error, want a \"no cases selected\" error")
		}
		if err := checkSelection([]*cases.Case{}); err == nil {
			t.Fatal("got nil error, want a \"no cases selected\" error")
		}
	})

	t.Run("non-empty selection is fine", func(t *testing.T) {
		if err := checkSelection([]*cases.Case{{ID: 1}}); err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
	})
}

// TestNoResultsError covers the re-review ordering fix: a cancelled run
// context must be reported as "interrupted" even when the belt-and-
// suspenders zero-results guard also fires, since a SIGINT/SIGTERM landing
// before any case records a result (e.g. mid instance-boot) is a distinct
// situation from an empty case selection and must not be misattributed to
// it.
func TestNoResultsError(t *testing.T) {
	t.Run("cancelled context takes priority and reports interrupted", func(t *testing.T) {
		err := noResultsError(context.Canceled)
		if err == nil || err.Error() != "interrupted" {
			t.Fatalf("noResultsError(context.Canceled) = %v, want \"interrupted\"", err)
		}
	})

	t.Run("nil context error reports no cases selected", func(t *testing.T) {
		err := noResultsError(nil)
		if !errors.Is(err, errNoCasesSelected) {
			t.Fatalf("noResultsError(nil) = %v, want errNoCasesSelected", err)
		}
	})
}

// cliCase and httpCase build minimal cases distinguished only by
// Request.Kind, for effectiveSelection tests.
func cliCase(id int) *cases.Case {
	return &cases.Case{ID: id, Request: cases.Request{Kind: "cli"}}
}

func httpKindCase(id int) *cases.Case {
	return &cases.Case{ID: id, Request: cases.Request{Kind: ""}}
}

// TestEffectiveSelectionAndCheckSelection covers the -skip-cli/-skip-http
// vacuous-green gap directly: an over-narrow id/area filter combined with a
// skip flag that excludes every remaining case (e.g. `-cases 1705
// -skip-cli` where 1705 is cli-only) must be caught by checkSelection run on
// the effective (post-skip) set, not the raw id/area-filtered one.
func TestEffectiveSelectionAndCheckSelection(t *testing.T) {
	cli := cliCase(1705)
	httpC := httpKindCase(1700)

	t.Run("cli-only selection with -skip-cli errors", func(t *testing.T) {
		eff := effectiveSelection([]*cases.Case{cli}, true, false)
		if len(eff) != 0 {
			t.Fatalf("effectiveSelection = %+v, want empty", eff)
		}
		if err := checkSelection(eff); err == nil {
			t.Fatal("checkSelection(empty effective set) = nil, want an error")
		}
	})

	t.Run("http-only selection with -skip-http errors", func(t *testing.T) {
		eff := effectiveSelection([]*cases.Case{httpC}, false, true)
		if len(eff) != 0 {
			t.Fatalf("effectiveSelection = %+v, want empty", eff)
		}
		if err := checkSelection(eff); err == nil {
			t.Fatal("checkSelection(empty effective set) = nil, want an error")
		}
	})

	t.Run("both skips with a nonempty selection errors", func(t *testing.T) {
		eff := effectiveSelection([]*cases.Case{cli, httpC}, true, true)
		if len(eff) != 0 {
			t.Fatalf("effectiveSelection = %+v, want empty", eff)
		}
		if err := checkSelection(eff); err == nil {
			t.Fatal("checkSelection(empty effective set) = nil, want an error")
		}
	})

	t.Run("mixed selection with one skip flag still runs the other half", func(t *testing.T) {
		eff := effectiveSelection([]*cases.Case{cli, httpC}, true, false)
		if ids := caseIDs(eff); !reflect.DeepEqual(ids, []int{1700}) {
			t.Fatalf("effectiveSelection(-skip-cli) ids = %v, want [1700]", ids)
		}
		if err := checkSelection(eff); err != nil {
			t.Fatalf("checkSelection(non-empty effective set) = %v, want nil", err)
		}

		eff = effectiveSelection([]*cases.Case{cli, httpC}, false, true)
		if ids := caseIDs(eff); !reflect.DeepEqual(ids, []int{1705}) {
			t.Fatalf("effectiveSelection(-skip-http) ids = %v, want [1705]", ids)
		}
		if err := checkSelection(eff); err != nil {
			t.Fatalf("checkSelection(non-empty effective set) = %v, want nil", err)
		}
	})

	t.Run("no skips leaves the selection unchanged", func(t *testing.T) {
		selected := []*cases.Case{cli, httpC}
		eff := effectiveSelection(selected, false, false)
		if !reflect.DeepEqual(eff, selected) {
			t.Fatalf("effectiveSelection(no skips) = %+v, want unchanged %+v", eff, selected)
		}
	})
}

func TestHTTPDeviationFindings(t *testing.T) {
	got := httpDeviationFindings()
	if len(got) != 3 {
		t.Fatalf("got %d findings, want 3: %v", len(got), got)
	}
	for _, f := range got {
		if !strings.HasPrefix(f, "HARNESS deviation: §2.1") {
			t.Errorf("finding %q does not start with the expected HARNESS deviation prefix", f)
		}
	}
}

func TestFindingsForRun(t *testing.T) {
	t.Run("ranHTTP false leaves base untouched", func(t *testing.T) {
		base := []string{"case 1: routed to a variant instance but not listed in HARNESS §2.3"}
		got := findingsForRun(base, false)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %v, want unchanged %v", got, base)
		}
	})

	t.Run("ranHTTP true appends the three deviation lines", func(t *testing.T) {
		base := []string{"x"}
		got := findingsForRun(base, true)
		want := append(append([]string{}, base...), httpDeviationFindings()...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("ranHTTP true with nil base still returns just the deviation lines", func(t *testing.T) {
		got := findingsForRun(nil, true)
		if !reflect.DeepEqual(got, httpDeviationFindings()) {
			t.Fatalf("got %v, want %v", got, httpDeviationFindings())
		}
	})
}
