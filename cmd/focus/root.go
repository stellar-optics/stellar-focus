package main

import (
	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-focus/internal/cli"
)

// RootCmd returns the root cobra command configured with all focus subcommands.
func RootCmd() *cobra.Command {
	return cli.NewRootCmdForTest(newAssertCmd(), newGoldenCmd(), newStatsCmd())
}
