package db

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestValidateDBName is unguarded (no database needed) and asserts
// Setup/Teardown reject any dbname that isn't a bare
// letters/digits/underscore identifier before it's interpolated into a
// DROP/CREATE DATABASE statement.
func TestValidateDBName(t *testing.T) {
	valid := []string{"postgrest_conf_oracle", "db2", "ORACLE_TEST_DB", "_leading_underscore"}
	for _, name := range valid {
		if err := validateDBName(name); err != nil {
			t.Errorf("validateDBName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"db; DROP DATABASE postgres",
		"db name",
		"db-name",
		"db.name",
		"db'name",
		"db\"name",
		"db\nname",
	}
	for _, name := range invalid {
		if err := validateDBName(name); err == nil {
			t.Errorf("validateDBName(%q) = nil, want an error", name)
		}
	}

	p := PGEnv{Host: "localhost", Port: "6432", User: "postgres", Password: "postgres"}
	const bad = "db; DROP DATABASE postgres"
	if err := Setup(p, bad, "/nonexistent"); err == nil {
		t.Errorf("Setup(%q, ...) = nil error, want rejection before touching the DB", bad)
	}
	if err := Teardown(p, bad); err == nil {
		t.Errorf("Teardown(%q, ...) = nil error, want rejection before touching the DB", bad)
	}
}

// TestChildEnvDoesNotInheritAmbientEnv covers Minor #4 from the PR #1
// review: Psql's child environment must be built from scratch (PATH + the
// PGEnv's own connection vars + PGDATABASE + extraEnv), not from
// os.Environ(), so an ambient PGOPTIONS/PGSSLMODE/PGTZ/etc. set in the
// caller's own shell can't silently change psql's behavior during fixture
// loading.
func TestChildEnvDoesNotInheritAmbientEnv(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c statement_timeout=1")
	t.Setenv("PGSSLMODE", "require")
	t.Setenv("PGTZ", "America/New_York")
	t.Setenv("SOME_UNRELATED_VAR", "leak-me-not")

	p := PGEnv{Host: "dbhost", Port: "6432", User: "u", Password: "pw"}
	// PGTZ passed as extraEnv, the way Setup calls Psql for the fixture
	// chain (PGTZ=UTC) — must be the value present in the result, not the
	// ambient ("America/New_York") ever set above.
	got := childEnv(p, "mydb", []string{"PGTZ=UTC"})

	want := map[string]string{
		"PATH":       os.Getenv("PATH"),
		"PGHOST":     "dbhost",
		"PGPORT":     "6432",
		"PGUSER":     "u",
		"PGPASSWORD": "pw",
		"PGDATABASE": "mydb",
		"PGTZ":       "UTC",
	}

	seen := map[string]string{}
	for _, kv := range got {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", kv)
		}
		if _, dup := seen[key]; dup {
			t.Fatalf("env var %q appears more than once in %v", key, got)
		}
		seen[key] = val
		switch key {
		case "PGOPTIONS", "PGSSLMODE", "SOME_UNRELATED_VAR":
			t.Errorf("ambient var %q leaked into child env: %q", key, kv)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("child env has %d vars %v, want exactly %d: %v", len(seen), got, len(want), want)
	}
	for key, wantVal := range want {
		if gotVal, ok := seen[key]; !ok {
			t.Errorf("expected env var %q missing from child env %v", key, got)
		} else if gotVal != wantVal {
			t.Errorf("%s = %q, want %q", key, gotVal, wantVal)
		}
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
