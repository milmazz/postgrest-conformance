package main

import (
	"flag"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/db"
)

const defaultDBName = "postgrest_conf_oracle"

func init() {
	dispatch["db-setup"] = runDBSetup
	dispatch["db-teardown"] = runDBTeardown
}

// runDBSetup loads the fixture chain (fixtures/README.md) into the target
// database, creating it fresh.
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

	return db.Setup(db.FromEnv(), *name, repoRoot+"/fixtures")
}

// runDBTeardown drops the target database and the fixture-chain role.
func runDBTeardown(args []string) error {
	fs := flag.NewFlagSet("db-teardown", flag.ExitOnError)
	name := fs.String("db", defaultDBName, "target database name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return db.Teardown(db.FromEnv(), *name)
}
