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

// TestLoadNormalizesNonStringKeyedExpectMappings covers Minor #3 from the PR
// #1 review: an unquoted integer-looking key inside expect: decodes via
// yaml.v3 as map[interface{}]interface{} (its fallback for non-string-keyed
// mappings), which jsonval.DeepEqual doesn't recognize. Load must run
// normalizeYAML over the decoded Expect map so this reaches assert.CheckHTTP
// as an ordinary map[string]any.
func TestLoadNormalizesNonStringKeyedExpectMappings(t *testing.T) {
	p := writeCase(t, t.TempDir(), "4_x.yaml", `
id: 4
feature: a/b
request: { method: GET, path: /x }
schema: test
expect: { status: 200, body_exact: { 1: x } }
notes: n
source: s
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	be, ok := c.Expect["body_exact"].(map[string]any)
	if !ok {
		t.Fatalf("body_exact = %T, want map[string]any (unquoted int key must normalize to a string key)", c.Expect["body_exact"])
	}
	if v, ok := be["1"]; !ok || v != "x" {
		t.Fatalf(`body_exact["1"] = %v (present=%v), want "x"`, v, ok)
	}
}

func TestLoadAllRealCorpus(t *testing.T) {
	root := repoRoot(t) // helper: walk up from CWD until PIN exists
	cs, err := LoadAll(filepath.Join(root, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 808 {
		t.Fatalf("got %d cases, want 808", len(cs))
	}
	cli := 0
	for _, c := range cs {
		if c.Request.Kind == "cli" {
			cli++
		}
	}
	if cli != 43 {
		t.Fatalf("got %d cli cases, want 43", cli)
	}
}
