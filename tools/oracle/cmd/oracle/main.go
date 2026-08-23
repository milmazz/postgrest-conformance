package main

import (
	"fmt"
	"os"
)

// dispatch maps subcommand name to implementation; later tasks add entries
// (fetch, db-setup, db-teardown, run). Each takes the remaining args.
var dispatch = map[string]func(args []string) error{}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	fn, ok := dispatch[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "oracle:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: oracle <fetch|db-setup|db-teardown|run> [flags]`)
}
