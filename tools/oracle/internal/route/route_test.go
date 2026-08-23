package route

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

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
		c       *cases.Case
		base    string
		profile string
		variant bool
	}{
		{httpCase(1, "test", "/items", nil), "test", "", false},
		{httpCase(2, "operators", "/items", nil), "operators", "", false},
		{httpCase(3, "auth", "/items", nil), "auth", "auth", false},
		{httpCase(4, "test", "/", nil), "auth", "", false}, // root path -> auth
		{httpCase(1005, "multi", "/parents", nil), "multi", "", false},
		{httpCase(1558, "headers", "/x", nil), "multi", "", false}, // headers-profile exception
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

// TestRouteAreaBases covers the issue #2 per-area single-schema routing
// directly: every area schema label routes to its own identically-named
// base (never to a shared wide instance), and an unrecognized schema label
// is a loud error rather than a silent misroute.
func TestRouteAreaBases(t *testing.T) {
	for _, label := range []string{
		"test", "operators", "ordering", "pagination", "representations",
		"mutations", "rpc", "headers", "config", "domain_representations",
		"observability",
	} {
		p, err := Route(httpCase(1, label, "/items", nil))
		if err != nil {
			t.Fatalf("schema %q: %v", label, err)
		}
		if p.Base != label {
			t.Fatalf("schema %q: Base = %q, want %q", label, p.Base, label)
		}
		if p.InjectProfile != "" {
			t.Fatalf("schema %q: InjectProfile = %q, want \"\" (own single-schema instance)", label, p.InjectProfile)
		}
		if p.GroupKey != label {
			t.Fatalf("schema %q: GroupKey = %q, want %q (shared, no overlay)", label, p.GroupKey, label)
		}
	}

	// "" and "public" both fall back to the "test" base.
	for _, schema := range []string{"", "public"} {
		p, err := Route(httpCase(1, schema, "/items", nil))
		if err != nil {
			t.Fatalf("schema %q: %v", schema, err)
		}
		if p.Base != "test" {
			t.Fatalf("schema %q: Base = %q, want \"test\"", schema, p.Base)
		}
	}

	if _, err := Route(httpCase(1, "nonexistent_area", "/items", nil)); err == nil {
		t.Fatal("Route with an unrecognized schema label = nil error, want an error naming the schema")
	} else if !strings.Contains(err.Error(), "nonexistent_area") {
		t.Fatalf("error %q does not name the offending schema", err.Error())
	}
}

// TestBaseConfigsCensus locks in the fourteen expected base names: the
// eleven per-area single-schema bases plus auth/multi/unicode. A base
// appearing or disappearing here is exactly the kind of change that must be
// deliberate (it changes how many PostgREST instances a full run boots).
func TestBaseConfigsCensus(t *testing.T) {
	want := map[string]bool{
		"test": true, "operators": true, "ordering": true, "pagination": true,
		"representations": true, "mutations": true, "rpc": true, "headers": true,
		"config": true, "domain_representations": true, "observability": true,
		"auth": true, "multi": true, "unicode": true,
	}
	bases := BaseConfigs()
	if len(bases) != len(want) {
		t.Fatalf("BaseConfigs() has %d entries, want %d: got keys %v", len(bases), len(want), baseKeys(bases))
	}
	for k := range bases {
		if !want[k] {
			t.Errorf("BaseConfigs() has unexpected base %q", k)
		}
	}
	for k := range want {
		if _, ok := bases[k]; !ok {
			t.Errorf("BaseConfigs() is missing expected base %q", k)
		}
	}

	// Every base — including auth/multi/unicode — must declare its own
	// explicit, non-empty PGRST_DB_SCHEMAS (it's no longer inherited from a
	// shared template default; see BaseConfigs).
	for name, cfg := range bases {
		if v, ok := cfg["PGRST_DB_SCHEMAS"]; !ok || v == "" {
			t.Errorf("base %q: PGRST_DB_SCHEMAS missing or empty (got %q, present=%v)", name, v, ok)
		}
	}

	// Every area base must declare exactly one PGRST_DB_SCHEMAS entry
	// containing only its own single schema, except auth/multi/unicode
	// which keep their (wider or differently-scoped) pre-issue-#2 values.
	for _, label := range areaSchemaLabels {
		if got := bases[label]["PGRST_DB_SCHEMAS"]; got != label {
			t.Errorf("base %q: PGRST_DB_SCHEMAS = %q, want %q", label, got, label)
		}
	}
}

func baseKeys(bases map[string]map[string]string) []string {
	keys := make([]string, 0, len(bases))
	for k := range bases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestRouteSatisfaction(t *testing.T) {
	// server-timing-enabled true == the observability base's effective
	// value -> shared
	p, _ := Route(httpCase(1750, "observability", "/x", map[string]any{"server-timing-enabled": true}))
	if len(p.Overlay) != 0 {
		t.Fatal("1750 must be satisfied by the observability base")
	}
	// db-max-rows 2 != unset default -> variant
	p, _ = Route(httpCase(1700, "config", "/items", map[string]any{"db-max-rows": 2}))
	if v := p.Overlay["PGRST_DB_MAX_ROWS"]; v.V != "2" || p.GroupKey == "config" {
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
	if !p.SafeUpdate || p.Base != "mutations" || p.InjectProfile != "" || p.GroupKey != "mutations+safeupdate" {
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

// TestRouteSafeupdateRejectsConfig covers Minor #2 from the PR #1 review:
// the 1387/1388/1389 safeupdate branch returns early before config
// translation runs, so a case in that id range that ever grows a `config:`
// block would otherwise have it silently ignored. Route must fail loudly
// instead.
func TestRouteSafeupdateRejectsConfig(t *testing.T) {
	c := httpCase(1387, "mutations", "/safe_update_items", map[string]any{"db-max-rows": 2})
	p, err := Route(c)
	if err == nil {
		t.Fatalf("Route(1387 with config) = %+v, nil error, want an error", p)
	}
	if !strings.Contains(err.Error(), "1387") || !strings.Contains(err.Error(), "extend Route") {
		t.Fatalf("Route(1387 with config) error = %q, want it to name the case and say to extend Route", err.Error())
	}

	// A config block present but with no keys (present-but-empty) must not
	// trip the guard, matching the len(...) > 0 check.
	empty := &cases.Case{ID: 1388, Schema: "mutations",
		Request: cases.Request{Method: "GET", Path: "/safe_update_items"},
		Config:  cases.Config{Present: true, Keys: map[string]any{}},
	}
	if _, err := Route(empty); err != nil {
		t.Fatalf("Route(1388 with empty config.keys) = %v, want nil", err)
	}
}

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

	// goldenUnlistedVariantIDs pins the KNOWN, accepted disagreements between
	// this package's routing and HARNESS.md §2.3's table: the 30
	// config-carrying ids §2.3 does not yet list (issue #5, third checkbox —
	// enumerated with their config keys in the 2026-08-22 triage doc). Any
	// OTHER finding is drift — a §2.3 row without a harnessVariantIDs entry
	// or vice versa (the defect class PR #12 fixed for case 1573) — and must
	// either fix HARNESS.md §2.3 / harnessVariantIDs or be explicitly
	// accepted here. When issue #5 lands its §2.3 amendments, shrink this
	// list in the same change.
	goldenUnlistedVariantIDs := []int{
		1129, 1130, 1131, 1132, 1133, 1140, 1147, 1148, 1149, 1387, 1388,
		1389, 1466, 1475, 1476, 1477, 1492, 1494, 1497, 1700, 1701, 1765,
		1766, 1767, 11801, 11806, 11808, 11815, 11816, 11817,
	}
	want := make(map[string]bool, len(goldenUnlistedVariantIDs))
	for _, id := range goldenUnlistedVariantIDs {
		want[fmt.Sprintf("case %d: routed to a variant instance but not listed in HARNESS §2.3", id)] = true
	}
	got := CrossCheckHarness(all)
	seen := make(map[string]bool, len(got))
	for _, f := range got {
		t.Logf("HARNESS finding: %s", f)
		seen[f] = true
		if !want[f] {
			t.Errorf("unexpected HARNESS finding (drift — fix §2.3/harnessVariantIDs or accept it in the golden list): %s", f)
		}
	}
	for f := range want {
		if !seen[f] {
			t.Errorf("golden HARNESS finding no longer reported (was it fixed? then remove it from the golden list): %s", f)
		}
	}
}
