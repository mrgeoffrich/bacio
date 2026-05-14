package main

import (
	"fmt"
	"os"

	"github.com/mrgeoffrich/bacio/internal/cli"
)

func main() {
	root, cleanup := cli.NewRoot()
	err := root.Execute()
	// cleanup flushes any pprof profiles; run it before os.Exit so the
	// profiles are written even when the command errored.
	cleanup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bacio:", err)
		os.Exit(1)
	}
}
