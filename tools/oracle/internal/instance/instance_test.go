package instance

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/route"
)

// TestBuildEnv is unguarded (no process, no DB) and pins down env assembly:
// overlay Clear removes a base key, overlay V overrides/adds one, the
// dynamic ports and db-uri land under their PGRST_* names, and a
// safeupdate request appends the expected query string to the URI.
func TestBuildEnv(t *testing.T) {
	base := map[string]string{"PGRST_LOG_LEVEL": "error", "PGRST_JWT_SECRET": "s"}
	overlay := map[string]route.Val{
		"PGRST_JWT_SECRET":  {Clear: true},
		"PGRST_DB_MAX_ROWS": {V: "2"},
	}
	env := BuildEnv(base, overlay, "postgresql://u:p@h:6432/d", false, 3001, 3002)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if _, ok := got["PGRST_JWT_SECRET"]; ok {
		t.Fatal("cleared key must be absent")
	}
	if got["PGRST_DB_MAX_ROWS"] != "2" || got["PGRST_SERVER_PORT"] != "3001" ||
		got["PGRST_ADMIN_SERVER_PORT"] != "3002" || got["PGRST_DB_URI"] != "postgresql://u:p@h:6432/d" {
		t.Fatalf("env: %v", got)
	}

	env = BuildEnv(base, nil, "postgresql://u:p@h:6432/d", true, 1, 2)
	for _, kv := range env {
		if strings.HasPrefix(kv, "PGRST_DB_URI=") &&
			!strings.HasSuffix(kv, "?options=-csession_preload_libraries%3Dsafeupdate") {
			t.Fatalf("safeupdate uri: %s", kv)
		}
	}
}

// TestBuildEnvOnlyInheritsPATH guards against ambient PGRST_*/PG* leakage
// from the calling shell into the child's environment: BuildEnv's output
// must consist of exactly base+overlay+the three injected keys, plus PATH
// copied from the parent process — nothing else the test process happens to
// have set.
func TestBuildEnvOnlyInheritsPATH(t *testing.T) {
	t.Setenv("PGDATABASE", "should-not-leak")
	t.Setenv("PGRST_LOG_LEVEL", "should-not-leak-either")

	env := BuildEnv(map[string]string{"PGRST_DB_POOL": "10"}, nil,
		"postgresql://u:p@h:6432/d", false, 3001, 3002)

	want := map[string]bool{
		"PGRST_DB_POOL":           true,
		"PGRST_DB_URI":            true,
		"PGRST_SERVER_PORT":       true,
		"PGRST_ADMIN_SERVER_PORT": true,
		"PATH":                    true,
	}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !want[k] {
			t.Fatalf("unexpected inherited key %q in env: %v", k, env)
		}
	}
	if len(env) != len(want) {
		t.Fatalf("env has %d entries, want %d: %v", len(env), len(want), env)
	}
}

// TestStartAgainstRealDB is guarded: it boots a real PostgREST binary
// against a real, already-loaded fixture database and proves the admin
// /ready poll and dynamic port allocation actually work end to end.
func TestStartAgainstRealDB(t *testing.T) {
	bin, uri := os.Getenv("ORACLE_TEST_BIN"), os.Getenv("ORACLE_TEST_DB_URI")
	if bin == "" || uri == "" {
		t.Skip("set ORACLE_TEST_BIN and ORACLE_TEST_DB_URI (a loaded fixture DB)")
	}
	inst, err := Start(bin, map[string]string{
		"PGRST_DB_SCHEMAS": "test", "PGRST_DB_TX_END": "rollback",
		"PGRST_SERVER_HOST": "127.0.0.1", "PGRST_DB_CONFIG": "false",
		"PGRST_LOG_LEVEL": "error",
	}, nil, uri, false)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Stop()
	r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", inst.Port))
	if err != nil || r.StatusCode >= 500 {
		t.Fatalf("root request: %v %v", r, err)
	}
}
