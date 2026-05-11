package main

import (
	"fmt"
	"os"

	"github.com/mrgeoffrich/bacio/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "bacio:", err)
		os.Exit(1)
	}
}
