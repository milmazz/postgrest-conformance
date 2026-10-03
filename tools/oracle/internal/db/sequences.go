package db

// Sequence baselines (issue #22).
//
// Postgres sequences are non-transactional: nextval() consumes a value that
// ROLLBACK does not return. The runner boots every PostgREST instance with
// db-tx-end=rollback, which undoes every row a case writes but not the
// sequence advances behind serial/identity defaults, so a case asserting a
// generated key (case 1305's Location: …?id=eq.2) passes once per fixture
// load and fails on every repeat run.
//
// The fix is generic rather than per-case: CaptureSequences records every
// sequence's (last_value, is_called) right after the fixture chain loads,
// and RestoreSequences puts them all back with setval before a run, so each
// run starts from exactly the state the fixtures define.
//
// Where the baseline lives: a JSON file outside the database (the caller
// picks the path; cmd/oracle uses tools/oracle/.cache/sequences/), never a
// table in the fixture database. Any object added to the database — even in
// a dedicated schema — is visible to PostgREST's catalog introspection and
// to anything that enumerates schemas or relations, and would make the
// oracle's database differ from the one the fixture chain describes (and
// from the one bier's loader builds, see tools/verify_equivalence.sh). A
// file cannot change a case result. Its one weakness, outliving the
// database it describes, is covered by recording the database's OID:
// DROP/CREATE DATABASE always allocates a new one, so a baseline from an
// earlier build is detected as stale rather than silently applied.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SequenceState is one sequence's restorable state: the two values setval
// takes. log_cnt is deliberately not recorded — it is a WAL-batching
// detail that does not affect which value nextval() returns next.
type SequenceState struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	LastValue int64  `json:"last_value"`
	IsCalled  bool   `json:"is_called"`
}

// SequenceBaseline is every sequence's state in one database, as captured
// right after the fixture chain loaded, plus the identity of that database.
type SequenceBaseline struct {
	Database    string          `json:"database"`
	DatabaseOID int64           `json:"database_oid"`
	Sequences   []SequenceState `json:"sequences"`
}

// ErrNoBaseline is wrapped by ReadBaseline when no baseline file exists —
// typically a database loaded by a tool version that predates baselines,
// or by something other than `oracle db-setup`.
var ErrNoBaseline = errors.New("no sequence baseline")

// ErrStaleBaseline is wrapped by RestoreSequences when the baseline was
// captured from a different build of the database than the one now live.
var ErrStaleBaseline = errors.New("stale sequence baseline")

// PsqlQuery runs a single SQL statement against dbname and returns its
// unaligned, tuples-only output with the trailing newline trimmed. It uses
// the same from-scratch child environment as Psql, plus -X so a ~/.psqlrc
// cannot inject extra lines (e.g. \timing) into the output being parsed.
func (p PGEnv) PsqlQuery(dbname, sql string) (string, error) {
	cmd := exec.Command("psql", "-X", "-v", "ON_ERROR_STOP=1", "-q", "-t", "-A", "-c", sql)
	cmd.Env = childEnv(p, dbname, nil)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("psql -d %s: %w: %s", dbname, err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// listSequencesSQL returns the database's OID (cast to bigint, since json
// renders the oid type as a string) and every user sequence, as one JSON
// object. pg_* schemas (pg_catalog, pg_toast, other sessions'
// pg_temp_N) and information_schema are excluded: none is fixture state,
// and another session's temp sequence cannot even be read.
const listSequencesSQL = `SELECT json_build_object(
  'oid', (SELECT oid::bigint FROM pg_catalog.pg_database WHERE datname = current_database()),
  'sequences', coalesce((
    SELECT json_agg(json_build_object('schema', n.nspname, 'name', c.relname)
                    ORDER BY n.nspname, c.relname)
    FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relkind = 'S'
      AND n.nspname NOT LIKE 'pg\_%'
      AND n.nspname <> 'information_schema'
  ), '[]'::json))`

// CaptureSequences reads every user sequence's (last_value, is_called) in
// dbname. is_called is not exposed by pg_sequences (whose last_value is
// NULL whenever is_called is false, losing exactly the setval(…, 2, false)
// state case 1305 depends on), so each sequence relation is read directly,
// in one UNION ALL over the names listed first.
func CaptureSequences(p PGEnv, dbname string) (*SequenceBaseline, error) {
	if err := validateDBName(dbname); err != nil {
		return nil, err
	}

	out, err := p.PsqlQuery(dbname, listSequencesSQL)
	if err != nil {
		return nil, fmt.Errorf("list sequences: %w", err)
	}
	var listed struct {
		OID       int64           `json:"oid"`
		Sequences []SequenceState `json:"sequences"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, fmt.Errorf("list sequences: parse %q: %w", out, err)
	}

	b := &SequenceBaseline{Database: dbname, DatabaseOID: listed.OID, Sequences: []SequenceState{}}
	if len(listed.Sequences) == 0 {
		return b, nil
	}

	out, err = p.PsqlQuery(dbname, sequenceStateSQL(listed.Sequences))
	if err != nil {
		return nil, fmt.Errorf("read sequence state: %w", err)
	}
	if err := json.Unmarshal([]byte(out), &b.Sequences); err != nil {
		return nil, fmt.Errorf("read sequence state: parse %q: %w", out, err)
	}
	// UNION ALL has no guaranteed output order; sort so the baseline file
	// is stable across captures of the same state.
	sort.Slice(b.Sequences, func(i, j int) bool {
		if b.Sequences[i].Schema != b.Sequences[j].Schema {
			return b.Sequences[i].Schema < b.Sequences[j].Schema
		}
		return b.Sequences[i].Name < b.Sequences[j].Name
	})
	return b, nil
}

// sequenceStateSQL builds the query reading each listed sequence's state as
// a JSON array of SequenceState objects. seqs must be non-empty.
func sequenceStateSQL(seqs []SequenceState) string {
	parts := make([]string, len(seqs))
	for i, s := range seqs {
		parts[i] = fmt.Sprintf(
			"SELECT %s AS schema, %s AS name, last_value, is_called FROM %s.%s",
			quoteLiteral(s.Schema), quoteLiteral(s.Name), quoteIdent(s.Schema), quoteIdent(s.Name),
		)
	}
	return "SELECT json_agg(s) FROM (" + strings.Join(parts, " UNION ALL ") + ") s"
}

// RestoreSequences checks that b was captured from the live build of its
// database, then setvals every sequence in it back to its recorded state,
// in one statement (so either all are restored or, on error, none).
// A sequence named in b but missing from the database is an error: it
// means the database is not the one the fixture chain built.
func RestoreSequences(p PGEnv, b *SequenceBaseline) error {
	if err := validateDBName(b.Database); err != nil {
		return err
	}

	out, err := p.PsqlQuery(b.Database,
		"SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()")
	if err != nil {
		return fmt.Errorf("restore sequences: %w", err)
	}
	oid, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return fmt.Errorf("restore sequences: parse database oid %q: %w", out, err)
	}
	if oid != b.DatabaseOID {
		return fmt.Errorf("%w: captured from database %q with oid %d, but the live %q has oid %d (it was rebuilt by something other than `oracle db-setup`)",
			ErrStaleBaseline, b.Database, b.DatabaseOID, b.Database, oid)
	}
	if len(b.Sequences) == 0 {
		return nil
	}

	sql, err := restoreSQL(b.Sequences)
	if err != nil {
		return err
	}
	out, err = p.PsqlQuery(b.Database, sql)
	if err != nil {
		return fmt.Errorf("restore sequences: %w", err)
	}
	if n, err := strconv.Atoi(out); err != nil || n != len(b.Sequences) {
		return fmt.Errorf("restore sequences: restored %q of %d", out, len(b.Sequences))
	}
	return nil
}

// restoreSQL builds the single statement that setvals every sequence in
// seqs and returns how many it set. The states travel as one JSON literal
// unpacked by json_to_recordset, and Postgres itself quotes the names via
// format('%I.%I'), so no identifier is ever spliced into SQL by hand.
// standard_conforming_strings is pinned on so the JSON's own backslash
// escapes reach the JSON parser unchanged.
func restoreSQL(seqs []SequenceState) (string, error) {
	js, err := json.Marshal(seqs)
	if err != nil {
		return "", fmt.Errorf("restore sequences: encode baseline: %w", err)
	}
	return "SET standard_conforming_strings = on; " +
		"SELECT count(pg_catalog.setval(format('%I.%I', s.schema, s.name)::regclass, s.last_value, s.is_called)) " +
		"FROM json_to_recordset(" + quoteLiteral(string(js)) + "::json) " +
		"AS s(schema text, name text, last_value bigint, is_called boolean)", nil
}

// quoteIdent double-quotes a Postgres identifier, doubling embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral single-quotes a Postgres string literal, doubling embedded
// quotes. Correct under standard_conforming_strings = on, the default
// since PostgreSQL 9.1 (backslashes are then ordinary characters).
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// unsafePathChars matches every character BaselinePath replaces, so a
// PGHOST that is a unix-socket directory still yields one flat file name.
var unsafePathChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// BaselinePath returns where the baseline for dbname on p's server lives
// under dir. It is keyed by host and port as well as database name, so two
// servers carrying the same database name (e.g. two fixture containers on
// different ports) never share — or overwrite — one baseline.
func BaselinePath(dir string, p PGEnv, dbname string) string {
	name := unsafePathChars.ReplaceAllString(p.Host+"_"+p.Port+"_"+dbname, "_")
	return filepath.Join(dir, name+".json")
}

// WriteBaseline writes b to path as indented JSON, creating its directory.
func WriteBaseline(path string, b *SequenceBaseline) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write sequence baseline: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("write sequence baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write sequence baseline: %w", err)
	}
	return nil
}

// ReadBaseline reads the baseline at path. A missing file is reported as
// an error wrapping ErrNoBaseline.
func ReadBaseline(path string) (*SequenceBaseline, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNoBaseline, path)
	}
	if err != nil {
		return nil, fmt.Errorf("read sequence baseline: %w", err)
	}
	var b SequenceBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("read sequence baseline %s: %w", path, err)
	}
	return &b, nil
}

// RemoveBaseline deletes the baseline at path; a missing file is not an
// error, so teardown stays idempotent.
func RemoveBaseline(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove sequence baseline: %w", err)
	}
	return nil
}
