// Package oracle is the suite's internal conformance runner: it executes
// every case in cases/ against real PostgREST (the version pinned in PIN)
// and reports divergences.
//
// "Oracle" is the software-testing term: a test oracle is the authoritative
// mechanism that decides what the correct output is — here, real PostgREST
// itself. It is not a reference to Oracle Database.
//
// This is internal tooling, not a supported consumer API. It never modifies
// cases/, spec/, or fixtures/; failures are findings routed through
// CONTRIBUTING.md.
package oracle
