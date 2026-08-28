// Command awb is an agent-first issue tracker: a single binary over SQLite,
// with a command line interface for coding agents, humans and scripts.
//
// See spec/ARCHITECTURE.md for the shape of the system.
package main

import (
	"context"
	_ "embed"
	"os"
	"runtime/debug"
	"strings"

	"github.com/tofutools/awb/internal/cli"
	"github.com/tofutools/awb/internal/openapi"
)

// openAPIDocument is the OpenAPI document that specifies the HTTP API, which
// serve publishes at /openapi.json and /openapi.yaml.
//
// It lives at the repository root because it is what the Go server in
// internal/api and the TypeScript types in web/ts/api/types.ts are generated
// from, and a generator's input belongs to the repository rather than to one
// package. Embedding reaches no further up than its own directory, so this is
// the only package that can carry it, and it is handed down from here.
//
//go:embed openapi.yaml
var openAPIDocument []byte

// version is the binary's version, which --version prints. It is overridden at
// build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	info, _ := debug.ReadBuildInfo()
	os.Exit(cli.Execute(context.Background(), versionString(version, info),
		openapi.New(openAPIDocument), os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

// versionString is what --version prints: the version stamp followed by the
// commit the binary was built from, as "1.2.3 (<hash>, <commit time>)". The Go
// toolchain records the commit under -buildvcs=true, which is how awb is
// built. A build from a dirty tree says "modified", because then the commit
// does not describe what was compiled.
//
// A build the toolchain recorded no commit for gets the bare version instead,
// which is not a corner case: `go run` and `go test` do not stamp, nor does a
// build from the module cache rather than a checkout, and cmd/go does not
// recognise a linked Git worktree as a checkout at all — it looks for a .git
// directory and a worktree's .git is a file — so a build from one is
// unstamped too, silently, because -buildvcs=true only fails when it finds a
// repository it cannot read.
//
// The version stamp is always the first whitespace-separated field, which is
// what the release workflow asserts.
func versionString(version string, info *debug.BuildInfo) string {
	if info == nil {
		return version
	}

	var revision, commitTime string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			commitTime = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return version
	}

	parts := []string{revision}
	if commitTime != "" {
		parts = append(parts, commitTime)
	}
	if modified {
		parts = append(parts, "modified")
	}
	return version + " (" + strings.Join(parts, ", ") + ")"
}
