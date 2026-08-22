package db

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

func TestSetupLoadsFixtureChain(t *testing.T) {
	if os.Getenv("ORACLE_TEST_DB") == "" {
		t.Skip("set ORACLE_TEST_DB=1 with `make db-up` running")
	}
	p := FromEnv()
	root := repoRoot(t)
	const name = "postgrest_conf_oracle_test"
	defer Teardown(p, name)
	if err := Setup(p, name, root+"/fixtures"); err != nil {
		t.Fatal(err)
	}
	// -X: ignore ~/.psqlrc, so a developer's personal startup file (e.g. one
	// that turns on \timing) can't inject extra lines into -tAc output.
	out, err := exec.Command("psql", "-X", p.URI(name), "-tAc",
		`SELECT count(*) FROM pg_namespace WHERE nspname IN ('test','mutations','تست','v1','v2')`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "5\n" {
		t.Fatalf("schemas missing: %q", out)
	}
}
