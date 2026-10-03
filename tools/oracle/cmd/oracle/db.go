package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/db"
)

const defaultDBName = "postgrest_conf_oracle"

func init() {
	dispatch["db-setup"] = runDBSetup
	dispatch["db-teardown"] = runDBTeardown
}

// baselinePath returns where the sequence baseline for dbname on p's server
// lives: tools/oracle/.cache/sequences/, beside the fetched binaries. See
// the internal/db sequences.go comment for why it is a file and not a table.
func baselinePath(repoRoot string, p db.PGEnv, dbname string) string {
	return db.BaselinePath(filepath.Join(repoRoot, "tools", "oracle", ".cache", "sequences"), p, dbname)
}

// runDBSetup loads the fixture chain (fixtures/README.md) into the target
// database, creating it fresh, then records every sequence's state as the
// baseline `oracle run` restores before each run (issue #22).
func runDBSetup(args []string) error {
	fs := flag.NewFlagSet("db-setup", flag.ExitOnError)
	name := fs.String("db", defaultDBName, "target database name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	p := db.FromEnv()
	if err := db.Setup(p, *name, repoRoot+"/fixtures"); err != nil {
		return err
	}
	b, err := db.CaptureSequences(p, *name)
	if err != nil {
		return err
	}
	return db.WriteBaseline(baselinePath(repoRoot, p, *name), b)
}

// runDBTeardown drops the target database and the fixture-chain role, and
// removes the database's sequence baseline, which would be stale anyway.
func runDBTeardown(args []string) error {
	fs := flag.NewFlagSet("db-teardown", flag.ExitOnError)
	name := fs.String("db", defaultDBName, "target database name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	p := db.FromEnv()
	if err := db.Teardown(p, *name); err != nil {
		return err
	}
	return db.RemoveBaseline(baselinePath(repoRoot, p, *name))
}

// resetSequences restores dbname's sequence baseline before a run, so a
// repeat run without a fixture reload starts from the sequence state the
// fixtures define (issue #22). A missing or stale baseline is a hard error
// naming the fix, rather than a run that surfaces it later as a bare
// Location mismatch on a serial-PK case.
func resetSequences(repoRoot string, p db.PGEnv, dbname string) error {
	path := baselinePath(repoRoot, p, dbname)
	b, err := db.ReadBaseline(path)
	if err == nil {
		err = db.RestoreSequences(p, b)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNoBaseline) || errors.Is(err, db.ErrStaleBaseline) {
		return fmt.Errorf("%w\nsequences cannot be reset to their fixture state, so serial-key cases (e.g. 1305) would fail on a repeat run;\n"+
			"rebuild the database with `oracle db-setup -db %s` (or scripts/fresh-db), "+
			"or pass -no-reset-sequences to run without resetting", err, dbname)
	}
	return err
}
