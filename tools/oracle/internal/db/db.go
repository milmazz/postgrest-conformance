// Package db bootstraps and tears down the fixture database used by the
// conformance runner: the numbered SQL chain under fixtures/ (see
// fixtures/README.md), loaded against a Postgres instance reachable via
// PGHOST/PGPORT/PGUSER/PGPASSWORD (or their defaults).
package db

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// PGEnv holds the connection parameters used to reach the Postgres cluster
// hosting the fixture database.
type PGEnv struct {
	Host     string
	Port     string
	User     string
	Password string
}

// FromEnv builds a PGEnv from PGHOST/PGPORT/PGUSER/PGPASSWORD, defaulting to
// localhost/6432/postgres/postgres for whichever are unset.
func FromEnv() PGEnv {
	return PGEnv{
		Host:     envOrDefault("PGHOST", "localhost"),
		Port:     envOrDefault("PGPORT", "6432"),
		User:     envOrDefault("PGUSER", "postgres"),
		Password: envOrDefault("PGPASSWORD", "postgres"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// URI returns a postgresql:// connection string for dbname. Built with
// net/url's URL type (rather than manual string formatting) so userinfo
// escaping is correct by construction: url.QueryEscape is form-encoding
// (a space becomes "+", which libpq's URI parser does not decode back to a
// space, only percent-decoding), and "@"/":" need userinfo-specific
// escaping that QueryEscape doesn't provide either.
func (p PGEnv) URI(dbname string) string {
	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(p.User, p.Password),
		Host:   net.JoinHostPort(p.Host, p.Port),
		Path:   "/" + dbname,
	}
	return u.String()
}

// Psql runs psql against dbname with -v ON_ERROR_STOP=1 -q, args appended,
// PG* env vars set from p (plus PGDATABASE=dbname), and any extraEnv applied
// on top. On failure, the returned error includes psql's stderr.
func (p PGEnv) Psql(dbname string, extraEnv []string, args ...string) error {
	fullArgs := append([]string{"-v", "ON_ERROR_STOP=1", "-q"}, args...)
	cmd := exec.Command("psql", fullArgs...)
	cmd.Env = append(os.Environ(),
		"PGHOST="+p.Host,
		"PGPORT="+p.Port,
		"PGUSER="+p.User,
		"PGPASSWORD="+p.Password,
		"PGDATABASE="+dbname,
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql -d %s %v: %w: %s", dbname, args, err, stderr.String())
	}
	return nil
}

// Setup loads the fixture chain from fixturesDir into dbname, following
// fixtures/README.md exactly:
//  1. 01_roles.sql against the postgres maintenance database.
//  2. DROP DATABASE IF EXISTS <dbname>; CREATE DATABASE <dbname> pinned to
//     byte-ordering collation.
//  3. Each 0[2-7]_*.sql file, sorted, against dbname with PGTZ=UTC.
func Setup(p PGEnv, dbname, fixturesDir string) error {
	if err := p.Psql("postgres", nil, "-f", filepath.Join(fixturesDir, "01_roles.sql")); err != nil {
		return err
	}

	if err := p.Psql("postgres", nil, "-c", "DROP DATABASE IF EXISTS "+dbname); err != nil {
		return err
	}
	createStmt := fmt.Sprintf(
		"CREATE DATABASE %s TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'",
		dbname,
	)
	if err := p.Psql("postgres", nil, "-c", createStmt); err != nil {
		return err
	}

	matches, err := filepath.Glob(filepath.Join(fixturesDir, "0[2-7]_*.sql"))
	if err != nil {
		return fmt.Errorf("glob fixture chain: %w", err)
	}
	sort.Strings(matches)
	for _, f := range matches {
		if err := p.Psql(dbname, []string{"PGTZ=UTC"}, "-f", f); err != nil {
			return err
		}
	}
	return nil
}

// Teardown drops dbname and the db_config_authenticator role created while
// running the fixture chain.
func Teardown(p PGEnv, dbname string) error {
	if err := p.Psql("postgres", nil, "-c", "DROP DATABASE IF EXISTS "+dbname); err != nil {
		return err
	}
	return p.Psql("postgres", nil, "-c", "DROP ROLE IF EXISTS db_config_authenticator")
}
