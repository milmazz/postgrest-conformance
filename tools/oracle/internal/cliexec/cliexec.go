// Package cliexec executes `request.kind: cli` conformance cases (the
// --dump-config / --example / config-file invocations of the pinned
// PostgREST binary described by HARNESS.md's CLI section) and captures the
// resulting exit code, stdout, and stderr for internal/assert.CheckCLI to
// evaluate.
//
// Like internal/instance's HTTP process launcher, a subprocess here runs
// with a deliberately minimal environment (PATH plus only the case's own
// config.env entries) so its behavior is never accidentally shaped by
// whatever PGRST_*/PG* variables happen to be exported in the calling
// shell.
package cliexec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/assert"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/db"
)

// runTimeout bounds how long a single CLI invocation (including the
// dump_reparse_stable redump, run separately) may take before it's killed.
const runTimeout = 30 * time.Second

// dbConfigRole and dbConfigPassword are the role a db-config CLI case
// connects as, and the fixed password cliexec pins on it so the subprocess
// can authenticate via PGPASSWORD. The case's own config.env supplies
// PGUSER=db_config_authenticator; PostgREST's default db-uri
// ("postgresql://") resolves the rest of the connection through libpq's
// PG* environment variables.
const (
	dbConfigRole     = "db_config_authenticator"
	dbConfigPassword = "oracle_cli"
)

// Run executes one kind:cli case against the pinned binary at bin. pg/dbname
// are used only by db-config cases (config.preconditions_sql non-empty); if
// such a case is run with pg == nil, Run returns an error rather than
// silently skipping the database glue.
func Run(c *cases.Case, bin string, pg *db.PGEnv, dbname string) (*assert.CLIResult, error) {
	argv, cleanup, err := buildArgv(c)
	if err != nil {
		return nil, fmt.Errorf("cliexec: case %d: %w", c.ID, err)
	}
	defer cleanup()

	env := buildEnv(c)

	if len(c.Config.PreconditionsSQL) > 0 {
		if pg == nil {
			return nil, fmt.Errorf("cliexec: case %d: config.preconditions_sql is present but no database was provided", c.ID)
		}
		if err := setupDBConfig(*pg, dbname, c.Config.PreconditionsSQL); err != nil {
			return nil, fmt.Errorf("cliexec: case %d: db-config setup: %w", c.ID, err)
		}
		env = append(env,
			"PGHOST="+pg.Host,
			"PGPORT="+pg.Port,
			"PGDATABASE="+dbname,
			"PGPASSWORD="+dbConfigPassword,
		)
	}

	stdout, stderr, exitCode, err := run(bin, argv, env)
	if err != nil {
		return nil, fmt.Errorf("cliexec: case %d: %w", c.ID, err)
	}

	result := &assert.CLIResult{ExitCode: exitCode, Stdout: stdout, Stderr: stderr}

	if stable, ok := c.Expect["dump_reparse_stable"].(bool); ok && stable {
		redumped, err := redump(bin, stdout)
		if err != nil {
			return nil, fmt.Errorf("cliexec: case %d: dump_reparse_stable redump: %w", c.ID, err)
		}
		result.Redump = redumped
		result.RedumpRan = true
	}

	return result, nil
}

// buildArgv assembles a case's argv: a temp file rendered from config.file
// (when present) as the first positional argument, then request.flag —
// either a "--flag" or (case 1719) a bogus config-file path passed
// positionally; either way it's simply the binary's next argv entry. The
// returned cleanup removes the temp config file, if one was created, and is
// always safe to call (a no-op when there is none).
func buildArgv(c *cases.Case) (argv []string, cleanup func(), err error) {
	cleanup = func() {}

	if len(c.Config.File) > 0 {
		path, err := writeConfigFile(c.Config.File)
		if err != nil {
			return nil, cleanup, err
		}
		cleanup = func() { os.Remove(path) }
		argv = append(argv, path)
	}

	argv = append(argv, c.Request.Flag)
	return argv, cleanup, nil
}

// writeConfigFile renders m via RenderConfigFile into a fresh temp file and
// returns its path.
func writeConfigFile(m map[string]any) (string, error) {
	f, err := os.CreateTemp("", "oracle-cliexec-*.conf")
	if err != nil {
		return "", fmt.Errorf("create config file: %w", err)
	}
	path := f.Name()

	if _, err := f.WriteString(RenderConfigFile(m)); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("write config file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("close config file: %w", err)
	}
	return path, nil
}

// buildEnv assembles a case's subprocess environment: PATH copied from the
// calling process, plus the case's own config.env entries verbatim — no
// inherited PGRST_*/PG* variables.
func buildEnv(c *cases.Case) []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range c.Config.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// setupDBConfig prepares the db_config_authenticator role a db-config CLI
// case relies on: drop any stale role left by a previous run, apply the
// case's preconditions_sql (its ALTER ROLE ... SET pgrst.* statements),
// then pin the role's password. The run loop's teardown (Task 15) and
// db.Teardown both drop the role afterward.
func setupDBConfig(pg db.PGEnv, dbname string, preconditions []string) error {
	if err := pg.Psql(dbname, nil, "-c", "DROP ROLE IF EXISTS "+dbConfigRole); err != nil {
		return err
	}
	for _, stmt := range preconditions {
		if err := pg.Psql(dbname, nil, "-c", stmt); err != nil {
			return err
		}
	}
	return pg.Psql(dbname, nil, "-c",
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", dbConfigRole, dbConfigPassword))
}

// run execs bin with argv/env, capturing stdout and stderr separately and
// bounding execution to runTimeout. A subprocess that starts and exits
// (whatever its exit code) is not itself a Go error — exitCode carries
// that outcome for the caller's CheckCLI to judge. A hung process killed by
// the timeout, or a bin that never starts at all, is reported as an error
// instead of a fabricated exit code.
func run(bin string, argv, env []string) (stdout, stderr []byte, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout, stderr = outBuf.Bytes(), errBuf.Bytes()

	if ctx.Err() == context.DeadlineExceeded {
		return stdout, stderr, 0, fmt.Errorf("%s: timed out after %s", bin, runTimeout)
	}
	if runErr != nil && cmd.ProcessState == nil {
		return stdout, stderr, 0, fmt.Errorf("run %s: %w", bin, runErr)
	}
	return stdout, stderr, cmd.ProcessState.ExitCode(), nil
}

// redump reruns bin against a fresh temp file holding dump (the original
// --dump-config stdout), with a PATH-only environment, per the
// dump_reparse_stable assertion (HARNESS §4): a dumped config, written back
// to a file and re-dumped, must reproduce byte-identical output.
func redump(bin string, dump []byte) ([]byte, error) {
	f, err := os.CreateTemp("", "oracle-cliexec-redump-*.conf")
	if err != nil {
		return nil, fmt.Errorf("create redump file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := f.Write(dump); err != nil {
		f.Close()
		return nil, fmt.Errorf("write redump file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close redump file: %w", err)
	}

	stdout, _, _, err := run(bin, []string{path, "--dump-config"}, []string{"PATH=" + os.Getenv("PATH")})
	if err != nil {
		return nil, err
	}
	return stdout, nil
}

// RenderConfigFile renders m as PostgREST config-file syntax: one
// `key = value` line per entry, keys sorted for deterministic output.
// String values are double-quoted, with `\` escaped to `\\` and `"` to
// `\"`; bool/int/float values are written as their bare literal.
//
// config.file map values are schema-restricted to string/boolean/number
// (case.schema.json); a nil or otherwise unsupported value indicates a
// caller bug rather than a recoverable runtime condition, so it panics
// instead of silently emitting invalid config syntax.
func RenderConfigFile(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(" = ")
		b.WriteString(renderConfigValue(k, m[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func renderConfigValue(key string, v any) string {
	switch t := v.(type) {
	case string:
		escaped := strings.ReplaceAll(t, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	case bool, int, int64, uint64, float32, float64:
		return fmt.Sprintf("%v", t)
	default:
		panic(fmt.Sprintf("cliexec: RenderConfigFile: key %q has unsupported value type %T", key, v))
	}
}
