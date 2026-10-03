package db

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestQuoting is unguarded and pins the two hand-rolled quoters: embedded
// quotes are doubled, nothing else is touched (including non-ASCII, as in
// the fixture chain's تست schema).
func TestQuoting(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:  `"plain"`,
		`a"b`:    `"a""b"`,
		`تست`:    `"تست"`,
		`Mixed`:  `"Mixed"`,
		`a b.c`:  `"a b.c"`,
		`"`:      `""""`,
		`x\y`:    `"x\y"`,
		`_seq_1`: `"_seq_1"`,
	} {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{
		`plain`: `'plain'`,
		`it's`:  `'it''s'`,
		`x\y`:   `'x\y'`,
		`''`:    `''''''`,
	} {
		if got := quoteLiteral(in); got != want {
			t.Errorf("quoteLiteral(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestSequenceStateSQL is unguarded: names are quoted as identifiers in
// FROM and as literals in the select list, one UNION ALL arm per sequence.
func TestSequenceStateSQL(t *testing.T) {
	got := sequenceStateSQL([]SequenceState{
		{Schema: "test", Name: "items_id_seq"},
		{Schema: `we"ird`, Name: "o'seq"},
	})
	want := `SELECT json_agg(s) FROM (` +
		`SELECT 'test' AS schema, 'items_id_seq' AS name, last_value, is_called FROM "test"."items_id_seq"` +
		` UNION ALL ` +
		`SELECT 'we"ird' AS schema, 'o''seq' AS name, last_value, is_called FROM "we""ird"."o'seq"` +
		`) s`
	if got != want {
		t.Errorf("sequenceStateSQL =\n%s\nwant\n%s", got, want)
	}
}

// TestRestoreSQL is unguarded: the states travel as one quoted JSON
// literal (so a quote in a name is doubled, never closes the literal) and
// names are quoted by Postgres' own format('%I.%I'), not spliced in.
func TestRestoreSQL(t *testing.T) {
	got, err := restoreSQL([]SequenceState{
		{Schema: "representations", Name: "auto_incrementing_pk_id_seq", LastValue: 2, IsCalled: false},
		{Schema: "x", Name: "o'seq", LastValue: 15, IsCalled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{
		"SET standard_conforming_strings = on; ",
		`format('%I.%I', s.schema, s.name)::regclass`,
		`'[{"schema":"representations","name":"auto_incrementing_pk_id_seq","last_value":2,"is_called":false},` +
			`{"schema":"x","name":"o''seq","last_value":15,"is_called":true}]'::json`,
		"AS s(schema text, name text, last_value bigint, is_called boolean)",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("restoreSQL output missing %q:\n%s", frag, got)
		}
	}
}

// TestBaselinePath is unguarded: the file is keyed by host, port and
// database, and a unix-socket PGHOST still yields one flat file name.
func TestBaselinePath(t *testing.T) {
	dir := "/cache/sequences"
	tcp := PGEnv{Host: "localhost", Port: "6432"}
	if got, want := BaselinePath(dir, tcp, "postgrest_conf_oracle"),
		"/cache/sequences/localhost_6432_postgrest_conf_oracle.json"; got != want {
		t.Errorf("BaselinePath = %q, want %q", got, want)
	}
	other := PGEnv{Host: "localhost", Port: "6434"}
	if BaselinePath(dir, tcp, "db") == BaselinePath(dir, other, "db") {
		t.Error("two ports share one baseline path")
	}
	sock := PGEnv{Host: "/var/run/postgresql", Port: "5432"}
	if got := BaselinePath(dir, sock, "db"); filepath.Dir(got) != dir {
		t.Errorf("unix-socket host escaped %s: %q", dir, got)
	}
}

// TestBaselineFileRoundTrip is unguarded: write, read back equal, remove,
// and a missing file reads as ErrNoBaseline (removal stays idempotent).
func TestBaselineFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "b.json")
	b := &SequenceBaseline{
		Database:    "postgrest_conf_oracle",
		DatabaseOID: 16384,
		Sequences: []SequenceState{
			{Schema: "representations", Name: "auto_incrementing_pk_id_seq", LastValue: 2},
			{Schema: "test", Name: "items_id_seq", LastValue: 15, IsCalled: true},
		},
	}
	if err := WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Errorf("ReadBaseline = %+v, want %+v", got, b)
	}
	if err := RemoveBaseline(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBaseline(path); err != nil {
		t.Errorf("second RemoveBaseline = %v, want nil", err)
	}
	if _, err := ReadBaseline(path); !errors.Is(err, ErrNoBaseline) {
		t.Errorf("ReadBaseline(missing) = %v, want ErrNoBaseline", err)
	}
}

// TestSequenceFuncsRejectBadDBName is unguarded: both entry points reject
// a non-identifier database name before running anything.
func TestSequenceFuncsRejectBadDBName(t *testing.T) {
	p := PGEnv{Host: "localhost", Port: "6432", User: "postgres", Password: "postgres"}
	const bad = "db; DROP DATABASE postgres"
	if _, err := CaptureSequences(p, bad); err == nil {
		t.Errorf("CaptureSequences(%q) = nil error", bad)
	}
	if err := RestoreSequences(p, &SequenceBaseline{Database: bad}); err == nil {
		t.Errorf("RestoreSequences(%q) = nil error", bad)
	}
}

// TestSequenceBaselineRestoresAfterRollback reproduces issue #22 against a
// real fixture load and shows the baseline undoing it: a rolled-back
// nextval() still advances the sequence case 1305 depends on, and
// RestoreSequences puts it back so the next nextval() yields 2 again. It
// also checks a baseline from an earlier build of the database is refused.
func TestSequenceBaselineRestoresAfterRollback(t *testing.T) {
	if os.Getenv("ORACLE_TEST_DB") == "" {
		t.Skip("set ORACLE_TEST_DB=1 with `make db-up` running")
	}
	p := FromEnv()
	root := repoRoot(t)
	const name = "postgrest_conf_oracle_seq_test"
	defer Teardown(p, name)
	if err := Setup(p, name, root+"/fixtures"); err != nil {
		t.Fatal(err)
	}

	b, err := CaptureSequences(p, name)
	if err != nil {
		t.Fatal(err)
	}
	const seq = "representations.auto_incrementing_pk_id_seq"
	var found *SequenceState
	for i, s := range b.Sequences {
		if s.Schema+"."+s.Name == seq {
			found = &b.Sequences[i]
		}
	}
	if found == nil {
		t.Fatalf("%s missing from baseline %+v", seq, b.Sequences)
	}
	// fixtures/06_area_schemas.sql: setval(…, 2, false).
	if found.LastValue != 2 || found.IsCalled {
		t.Fatalf("%s baseline = (%d, %v), want (2, false)", seq, found.LastValue, found.IsCalled)
	}

	nextval := func() string {
		t.Helper()
		out, err := p.PsqlQuery(name, "BEGIN; SELECT nextval('"+seq+"'); ROLLBACK;")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := nextval(); got != "2" {
		t.Fatalf("first nextval = %q, want 2", got)
	}
	if got := nextval(); got != "3" {
		t.Fatalf("nextval after a rolled-back one = %q, want 3 (issue #22 no longer reproduces?)", got)
	}

	if err := RestoreSequences(p, b); err != nil {
		t.Fatal(err)
	}
	if got := nextval(); got != "2" {
		t.Errorf("nextval after RestoreSequences = %q, want 2", got)
	}

	stale := *b
	stale.DatabaseOID++
	if err := RestoreSequences(p, &stale); !errors.Is(err, ErrStaleBaseline) {
		t.Errorf("RestoreSequences(stale oid) = %v, want ErrStaleBaseline", err)
	}

	// The nextval above left the sequence at (2, true); a restore must
	// bring back every sequence exactly as captured, is_called included.
	if err := RestoreSequences(p, b); err != nil {
		t.Fatal(err)
	}
	again, err := CaptureSequences(p, name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, b) {
		t.Errorf("capture after restore = %+v, want the original baseline %+v", again, b)
	}
}
