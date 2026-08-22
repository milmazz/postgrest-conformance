package route

import (
	"os"
	"path/filepath"
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
		{httpCase(1, "test", "/items", nil), "bulk", "", false},
		{httpCase(2, "operators", "/items", nil), "bulk", "operators", false},
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
	for _, f := range CrossCheckHarness(all) {
		t.Logf("HARNESS finding: %s", f)
	}
}
