# Coding agent instructions

## About

Agent Work Board (awb) is an agent-first issue tracker: a single Go binary over
SQLite, with a command line interface, an optional HTTP server and a bundled
read-only web UI.

## Project status

Implementation phase. Version 1 is implemented.

**The code and its tests are authoritative for behaviour.** The specification
that version 1 was built from has served its purpose and is gone; it is in the
Git history if you ever need it. `spec/ARCHITECTURE.md` describes the shape of
the system and the reasoning behind it, and `spec/TODO.md` lists what is left
for future versions.

That means a behavioural rule is documented in exactly one place: the comment
next to the code that enforces it, and the test that pins it down. When you
change behaviour, change both, and check whether `ARCHITECTURE.md` still
describes the system truthfully.

## Build

`./build.sh` is the whole build: it compiles the frontend, builds the binary,
runs the Go and frontend tests, and lints. It is silent on success and prints
the failing step's output on failure. `-o DIR` sets where the binary goes.

Prerequisites on `$PATH`: `go` (1.26.6 or later), `tsc`, `golangci-lint`,
`node`. No package manager is ever invoked: the browser bundles under
`web/static/vendor/` are pre-built committed artifacts.

CI runs that same script rather than a second definition of the build. Every
pull request and every push to `main` builds and tests on Linux and macOS and
cross-compiles each platform listed in `.github/targets.json`, which is also
what a tagged release ships; `main` is separately scanned for known
vulnerabilities. The pieces both workflows share live in `.github/actions/`.
Third-party actions are pinned to a commit with the version in a trailing
comment; GitHub's own `actions/*` are pinned to a major tag.

## Layout

| Path | Holds |
| --- | --- |
| `internal/domain` | The rules, and no I/O: vocabulary, the text gate, hash IDs, GFM link extraction, the relation graph, readiness, the `--compact` encoders. |
| `internal/storage` | The schema, the migrations and all SQL. |
| `internal/local` | The operations, one `BEGIN IMMEDIATE` transaction each. |
| `internal/backend` | The one interface every command is written against. |
| `internal/remote` | The same interface over HTTP, for `--db https://…`. |
| `internal/cli` | The cobra tree, the three output modes, `serve`. |
| `internal/handler` | The JSON API. |
| `internal/config` | The two config files, precedence, identity, colour. |
| `internal/awberr` | The error taxonomy both surfaces report. |
| `internal/api` | The OpenAPI document, embedded. |
| `web/` | The frontend: `ts/` sources, `static/` build output, `embed.go`. |

Two structural rules hold the design together:

* **One backend interface, two implementations.** Every command is written
  against `internal/backend`, so it cannot tell direct mode from remote mode
  apart, and the HTTP handlers sit on the same interface over the same local
  implementation. That is what makes "remote mode behaves identically" and
  "the API mirrors the CLI" structural rather than something to maintain by
  hand. Do not give a command a second code path.
* **The domain layer does no I/O.** Rules live there and are shared wholesale
  by both surfaces. When a rule needs to read the graph, the rule stays in
  `domain` as a function over sets and the traversal goes in `storage`.

## Conventions

* Cross-cutting HTTP middleware — auth, CSRF, gzip, security headers, recovery,
  static serving, SQLite opening and migration — comes from
  `github.com/mikaelstaldal/go-server-common`. Prefer it over reimplementing.
* Tests use `testify`: `require` for fatal assertions, `assert` for non-fatal.
* Frontend tests are `node --test` over `web/ts/tests/*.test.mjs`.
* Every mutation is one `BEGIN IMMEDIATE` transaction, so checks and the write
  they guard happen inside one writer's exclusive turn.
* A released migration batch is never edited, only followed by another.
* The default table output is explicitly not a compatibility surface. `--json`
  and `--compact` are; changing either is a breaking change.

## Rules

* Do not put history or changelog into any document unless the specific document is obviously ment for that. 
We have Git version history.

* Keep it simple, do not make it overly flexible or configurable unless that is required by the goals or 
requested by the user. 
