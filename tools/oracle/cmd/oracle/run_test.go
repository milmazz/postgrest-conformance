package main

import (
	"reflect"
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
	t.Run("shared bases first in bulk auth multi unicode order regardless of input order", func(t *testing.T) {
		// Global ascending id order with groups interleaved — mirrors the
		// real invariant that `selected` is always id-sorted (cases.LoadAll
		// sorts, filterCases preserves order), with different groups'
		// cases appearing in whatever order their ids happen to fall.
		selected := []*cases.Case{
			httpTestCase(1, "unicode-area"),
			httpTestCase(2, "auth-area"),
			httpTestCase(3, "bulk-area"),
			httpTestCase(4, "auth-area"),
			httpTestCase(5, "bulk-area"),
			httpTestCase(6, "multi-area"),
			httpTestCase(7, "unicode-area"),
			httpTestCase(8, "bulk-area"),
		}
		placements := map[int]*route.Placement{
			1: httpPlacement("unicode", "unicode"),
			2: httpPlacement("auth", "auth"),
			3: httpPlacement("bulk", "bulk"),
			4: httpPlacement("auth", "auth"),
			5: httpPlacement("bulk", "bulk"),
			6: httpPlacement("multi", "multi"),
			7: httpPlacement("unicode", "unicode"),
			8: httpPlacement("bulk", "bulk"),
		}

		got := groupsInOrder(placements, selected)

		wantKeys := []string{"bulk", "auth", "multi", "unicode"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}

		// cases within each group keep ascending id order
		wantIDs := map[string][]int{
			"bulk":    {3, 5, 8},
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

	t.Run("variant groups sorted by min case id, after shared groups", func(t *testing.T) {
		selected := []*cases.Case{
			httpTestCase(100, "v1"),
			httpTestCase(10, "v2"),
			httpTestCase(50, "v3"),
			httpTestCase(1, "bulk-area"),
		}
		placements := map[int]*route.Placement{
			100: httpPlacement("bulk", "bulk|A=1"),
			10:  httpPlacement("auth", "auth|B=2"),
			50:  httpPlacement("bulk", "bulk+safeupdate"),
			1:   httpPlacement("bulk", "bulk"),
		}

		got := groupsInOrder(placements, selected)

		// "bulk" is the only shared group present, so it comes first;
		// the three variant groups follow, ordered by their minimum id
		// (10, 50, 100) rather than by input order or key.
		wantKeys := []string{"bulk", "auth|B=2", "bulk+safeupdate", "bulk|A=1"}
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

		// bulk/multi are absent from the selection entirely; auth/unicode
		// must still come out in their relative bulk<auth<multi<unicode
		// positions, not input order (unicode's case appears first here).
		wantKeys := []string{"auth", "unicode"}
		if gotKeys := groupKeys(got); !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatalf("group order = %v, want %v", gotKeys, wantKeys)
		}
	})

	t.Run("non-http placements are excluded", func(t *testing.T) {
		selected := []*cases.Case{httpTestCase(1, "x"), httpTestCase(2, "y")}
		placements := map[int]*route.Placement{
			1: {Kind: "cli"},
			2: httpPlacement("bulk", "bulk"),
		}

		got := groupsInOrder(placements, selected)

		if len(got) != 1 || got[0].key != "bulk" || !reflect.DeepEqual(caseIDs(got[0].cases), []int{2}) {
			t.Fatalf("got %+v, want a single bulk group containing only case 2", got)
		}
	})
}

func TestIsSharedGroupKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"bulk", true},
		{"auth", true},
		{"multi", true},
		{"unicode", true},
		{"bulk+safeupdate", false},
		{"bulk|PGRST_DB_MAX_ROWS=2", false},
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
		{"bulk", 0},
		{"auth", 1},
		{"multi", 2},
		{"unicode", 3},
		{"bulk+safeupdate", len(sharedGroupOrder)},
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
	for _, baseName := range []string{"bulk", "multi", "unicode"} {
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
