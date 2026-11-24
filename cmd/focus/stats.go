package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-focus/internal/cli"
)

func newStatsCmd() *cobra.Command {
	return cli.NewStatsCmd(os.Stdout)
}
