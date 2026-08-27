// Command awb is an agent-first issue tracker: a single binary over SQLite,
// with a command line interface for coding agents, humans and scripts.
//
// See spec/ARCHITECTURE.md for the shape of the system.
package main

import (
	"context"
	"os"

	"github.com/tofutools/awb/internal/cli"
)

// version is the binary's version, which --version prints. It is overridden at
// build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Execute(context.Background(), version, os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}
