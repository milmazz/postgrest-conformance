package cliexec

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

// --- Brief's tests (RenderConfigFile: unguarded everywhere) ---

func TestRenderConfigFile(t *testing.T) {
	got := RenderConfigFile(map[string]any{
		"db-max-rows":          100,
		"log-level":            "warn",
		"jwt-secret-is-base64": `"true"`, // case 1741: literal quotes in the value
		"db-channel-enabled":   true,
	})
	want := "db-channel-enabled = true\n" +
		"db-max-rows = 100\n" +
		"jwt-secret-is-base64 = \"\\\"true\\\"\"\n" +
		"log-level = \"warn\"\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderConfigFileEscapesBackslash(t *testing.T) {
	got := RenderConfigFile(map[string]any{"db-schemas": `SPECIAL "@/\#~_-`})
	want := "db-schemas = \"SPECIAL \\\"@/\\\\#~_-\"\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

// --- Brief's tests (real binary: guarded on ORACLE_TEST_BIN) ---

func TestDumpConfigDefaults(t *testing.T) { // shape of case 1705
	bin := os.Getenv("ORACLE_TEST_BIN")
	if bin == "" {
		t.Skip("set ORACLE_TEST_BIN")
	}
	c := &cases.Case{ID: 1705, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 || !strings.Contains(string(r.Stdout), `db-schemas = "public"`) {
		t.Fatalf("exit %d stdout %.200s", r.ExitCode, r.Stdout)
	}
}

func TestFatalEnvConfig(t *testing.T) { // shape of case 1713
	bin := os.Getenv("ORACLE_TEST_BIN")
	if bin == "" {
		t.Skip("set ORACLE_TEST_BIN")
	}
	c := &cases.Case{ID: 1713, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true, Env: map[string]string{"PGRST_DB_TX_END": "random"}}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || !strings.Contains(string(r.Stderr), "Invalid transaction termination") {
		t.Fatalf("exit %d stderr %.200s", r.ExitCode, r.Stderr)
	}
}

// --- Additional unit tests (unguarded): drive Run's argv/env/timeout/redump
// wiring against a throwaway shell-script "binary" instead of the real
// PostgREST binary, mirroring instance_test.go's split between a pure,
// always-run assembly test (TestBuildEnvOnlyInheritsPATH) and a guarded
// real-process test (TestStartAgainstRealDB). ---

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-postgrest")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// parseArgLines extracts "ARG<n>:<value>" lines emitted by the argv-dump
// script below, in order.
func parseArgLines(out []byte) []string {
	var args []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if _, v, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(line, "ARG") {
			args = append(args, v)
		}
	}
	return args
}

const argvDumpScript = `i=0
for a in "$@"; do
  echo "ARG$i:$a"
  i=$((i+1))
done
`

// TestRunArgvPositionalOnly pins down case 1719's shape: no config.file, and
// a request.flag that is itself a bogus positional path rather than a
// "--flag". argv must be exactly that one positional value — no config file
// is synthesized.
func TestRunArgvPositionalOnly(t *testing.T) {
	bin := writeScript(t, argvDumpScript)
	c := &cases.Case{ID: 1719, Request: cases.Request{Kind: "cli", Flag: "does_not_exist.conf"},
		Config: cases.Config{Present: true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	args := parseArgLines(r.Stdout)
	if len(args) != 1 || args[0] != "does_not_exist.conf" {
		t.Fatalf("argv = %v, want [does_not_exist.conf]", args)
	}
}

// TestRunArgvConfigFileThenFlag pins down the config.file + flag shape
// (e.g. case 1741): a rendered temp config file is passed positionally
// first, then the "--flag" second.
func TestRunArgvConfigFileThenFlag(t *testing.T) {
	bin := writeScript(t, `cat "$1"
echo "---"
echo "$2"
`)
	c := &cases.Case{ID: 1741, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true, File: map[string]any{"log-level": "warn"}}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := RenderConfigFile(c.Config.File) + "---\n--dump-config\n"
	if string(r.Stdout) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.Stdout, want)
	}
}

// TestRunEnvIsolated guards against ambient environment leakage into the
// subprocess: only PATH (from the calling process) and the case's own
// config.env entries may appear, never anything else the test process
// happens to have set (e.g. a real PGDATABASE from the caller's shell).
func TestRunEnvIsolated(t *testing.T) {
	t.Setenv("ORACLE_CLIEXEC_TEST_POISON", "should-not-leak")
	bin := writeScript(t, "env\n")
	c := &cases.Case{ID: 9001, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true, Env: map[string]string{"PGRST_LOG_LEVEL": "info"}}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	out := string(r.Stdout)
	if strings.Contains(out, "ORACLE_CLIEXEC_TEST_POISON") {
		t.Fatalf("leaked ambient env var into subprocess:\n%s", out)
	}
	if !strings.Contains(out, "PGRST_LOG_LEVEL=info") {
		t.Fatalf("missing case env var:\n%s", out)
	}
	if !strings.Contains(out, "PATH=") {
		t.Fatalf("missing PATH:\n%s", out)
	}
}

// TestRunExitCode confirms ExitCode/Stdout/Stderr are captured separately
// and a nonzero exit is reported without itself being a Go error.
func TestRunExitCode(t *testing.T) {
	bin := writeScript(t, "echo out; echo err >&2; exit 7\n")
	c := &cases.Case{ID: 9002, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", r.ExitCode)
	}
	if strings.TrimSpace(string(r.Stdout)) != "out" {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if strings.TrimSpace(string(r.Stderr)) != "err" {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

// TestRunDBConfigRequiresPG requires an error (not a panic, not a silent
// no-op) when a case carries preconditions_sql but the caller passed a nil
// *db.PGEnv.
func TestRunDBConfigRequiresPG(t *testing.T) {
	bin := writeScript(t, "echo should-not-run\n")
	c := &cases.Case{ID: 9003, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true, PreconditionsSQL: []string{"SELECT 1"}}}
	if _, err := Run(c, bin, nil, "somedb"); err == nil {
		t.Fatal("want error when preconditions_sql is present but pg is nil")
	}
}

// TestRunDumpReparseStable exercises the dump_reparse_stable redump path
// end to end (case 1726's shape) against a stub binary that always emits
// identical output regardless of its argv, so Stdout and Redump must match
// and RedumpRan must be set.
func TestRunDumpReparseStable(t *testing.T) {
	bin := writeScript(t, "printf 'db-max-rows = 1000\\n'\n")
	c := &cases.Case{ID: 1726, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true},
		Expect: map[string]any{"dump_reparse_stable": true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.RedumpRan {
		t.Fatal("want RedumpRan")
	}
	if !bytes.Equal(r.Stdout, r.Redump) {
		t.Fatalf("stdout %q != redump %q", r.Stdout, r.Redump)
	}
}

// TestRunDumpReparseStableFalseSkipsRedump confirms the redump only runs
// when the case actually asserts dump_reparse_stable: true.
func TestRunDumpReparseStableFalseSkipsRedump(t *testing.T) {
	bin := writeScript(t, "printf 'db-max-rows = 1000\\n'\n")
	c := &cases.Case{ID: 1705, Request: cases.Request{Kind: "cli", Flag: "--dump-config"},
		Config: cases.Config{Present: true}}
	r, err := Run(c, bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.RedumpRan {
		t.Fatal("RedumpRan must be false when dump_reparse_stable is not asserted")
	}
}
