package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/fetchbin"
)

func init() {
	dispatch["fetch"] = runFetch
}

// runFetch resolves the repository root (walking up from the current
// working directory until a PIN file is found), fetches and verifies the
// pinned PostgREST release binary, and prints its path.
func runFetch(args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	binPath, err := fetchbin.Fetch(repoRoot)
	if err != nil {
		return err
	}

	fmt.Println(binPath)
	return nil
}

// findRepoRoot walks up from the current working directory until it finds a
// directory containing a PIN file, returning that directory's absolute
// path.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("oracle: getwd: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("oracle: abs %s: %w", dir, err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "PIN")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("oracle: no PIN file found in any parent directory")
		}
		dir = parent
	}
}
