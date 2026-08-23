package main

import "testing"

// TestCmdValidateOnRealTree runs the validate subcommand against the
// repository's own tree (the test CWD is inside the repo, and cmdValidate
// resolves the root the same way the other subcommands do); the real tree
// must pass.
func TestCmdValidateOnRealTree(t *testing.T) {
	if err := cmdValidate(nil); err != nil {
		t.Fatalf("oracle validate failed on the real tree: %v", err)
	}
}

// TestCmdValidateIsRegistered checks the subcommand is dispatchable.
func TestCmdValidateIsRegistered(t *testing.T) {
	if _, ok := dispatch["validate"]; !ok {
		t.Fatal("validate not registered in dispatch")
	}
}
