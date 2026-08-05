// Command focus records real Stellar and Soroban data as test fixtures, and
// checks that stored fixtures still match the chain.
//
// The assertions this repository provides live in the Go package
// github.com/stellar-optics/stellar-focus/pkg/focus. This command manages the
// fixtures those assertions run against; importing the library never pulls in
// this command or its dependencies.
//
// Usage:
//
//	focus capture tx <hash>          record a transaction as fixtures
//	focus capture ledger-entry <key> record a ledger entry
//	focus verify [paths...]          re-check stored fixtures against the chain
//
// See https://github.com/stellar-optics/stellar-focus for full documentation.
package main

import (
	"fmt"
	"os"

	"github.com/stellar-optics/stellar-focus/internal/cli"
)

func main() {
	err := cli.Execute(os.Args[1:], os.Stdout, os.Stderr)
	code, printable := cli.ExitCode(err)
	if printable && err != nil {
		fmt.Fprintln(os.Stderr, "focus:", err)
	}
	os.Exit(code)
}
