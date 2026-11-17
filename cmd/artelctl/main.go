// artelctl provides local Artel diagnostics.
package main

import (
	"fmt"
	"io"
	"os"
)

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			return printOutput(out, "Usage: artelctl --help | --version\nFoundation skeleton; API diagnostics are not implemented yet.")
		case "--version":
			return printOutput(out, "artelctl 0.1.0-dev")
		}
	}
	_, _ = fmt.Fprintln(errOut, "artelctl: expected --help or --version")
	return 2
}

func printOutput(out io.Writer, text string) int {
	if _, err := fmt.Fprintln(out, text); err != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
