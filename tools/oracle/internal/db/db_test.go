package db

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestURI is unguarded (no database needed) and asserts URI() escapes
// userinfo correctly by construction: a password containing "@"/":"/space
// must round-trip through url.Parse back to the original username,
// password, host, and path.
func TestURI(t *testing.T) {
	p := PGEnv{Host: "localhost", Port: "6432", User: "postgres", Password: "postgres"}
	if got, want := p.URI("postgrest_conf_oracle_test"),
		"postgresql://postgres:postgres@localhost:6432/postgrest_conf_oracle_test"; got != want {
		t.Fatalf("URI() = %q, want %q", got, want)
	}

	tricky := PGEnv{Host: "localhost", Port: "6432", User: "postgres", Password: "p@ss word:x"}
	uri := tricky.URI("db")
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", uri, err)
	}
	if got := u.User.Username(); got != tricky.User {
		t.Errorf("Username() = %q, want %q", got, tricky.User)
	}
	if pass, ok := u.User.Password(); !ok || pass != tricky.Password {
		t.Errorf("Password() = %q, ok=%v, want %q, ok=true", pass, ok, tricky.Password)
	}
	if want := tricky.Host + ":" + tricky.Port; u.Host != want {
		t.Errorf("Host = %q, want %q", u.Host, want)
	}
	if u.Path != "/db" {
		t.Errorf("Path = %q, want %q", u.Path, "/db")
	}
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
