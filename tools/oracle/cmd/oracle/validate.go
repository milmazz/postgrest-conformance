package main

import (
	"flag"
	"fmt"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/validate"
)

func init() {
	dispatch["validate"] = cmdValidate
}

// cmdValidate runs the suite tree check (see the internal/validate package
// comment): per-case schema validation, id/filename/source checks, and
// INDEX.md consistency. It prints one line per finding and returns a
// non-nil error (non-zero exit) iff the tree is unhealthy, in the
// "N cases checked" / findings / "OK" output shape inherited from the
// retired tools/validate.py. (The count is every case file examined;
// validate.py counted unique ids of schema-passing files, so on a broken
// tree the two could differ while agreeing on any healthy tree.)
func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	res, err := validate.Tree(root)
	if err != nil {
		return err
	}

	fmt.Printf("%d cases checked\n", res.CasesChecked)
	for _, f := range res.Findings {
		fmt.Println(f)
	}
	if len(res.Findings) > 0 {
		return fmt.Errorf("%d finding(s)", len(res.Findings))
	}
	fmt.Println("OK")
	return nil
}
