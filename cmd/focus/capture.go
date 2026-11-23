package main

import (
	"github.com/spf13/pflag"
)

// AddExtractSpecsFlag binds the --extract-specs flag to a command flag set.
func AddExtractSpecsFlag(flags *pflag.FlagSet, target *bool) {
	flags.BoolVar(target, "extract-specs", false,
		"automatically dump ScSpecEntry interfaces during capture as <tx_hash>.spec.json")
}
