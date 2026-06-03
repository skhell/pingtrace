// Package main is the pingtrace CLI entry point.
package main

import (
	"fmt"
	"os"

	"github.com/skhell/pingtrace/cmd/pingtrace/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
