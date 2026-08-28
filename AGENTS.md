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

`./build.sh` is the whole build: it generates the code `openapi.yaml`
specifies, compiles the frontend, builds the binary, runs the Go and frontend
tests, and lints. It is silent on success and prints the failing step's output
on failure. `-o DIR` sets where the binary goes.

Prerequisites on `$PATH`: `go` (1.26.6 or later), `ogen`,
`openapi-typescript`, `tsc`, `golangci-lint`, `node`. No package manager is
ever invoked: the browser bundles under `web/static/vendor/` are pre-built
committed artifacts.

Neither generated output is committed, so a fresh checkout does not compile
until the generators have run — `./build.sh`, or `task generate`.

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
| `internal/cli` | The cobra tree, the three output modes, `serve`, the `demo` data set. |
| `internal/handler` | The JSON API: the implementation of the generated server interface. |
| `internal/config` | The two config files, precedence, identity, colour. |
| `internal/awberr` | The error taxonomy both surfaces report. |
| `internal/api` | **Generated** from `openapi.yaml` by ogen. Never edited. |
| `internal/openapi` | The document itself: the JSON form, the two handlers that publish it, and what it says each operation accepts. |
| `web/` | The frontend: `ts/` sources (`api/types.ts` **generated**), `static/` build output, `embed.go`. |

Three structural rules hold the design together:

* **One backend interface, two implementations.** Every command is written
  against `internal/backend`, so it cannot tell direct mode from remote mode
  apart, and the HTTP handlers sit on the same interface over the same local
  implementation. That is what makes "remote mode behaves identically" and
  "the API mirrors the CLI" structural rather than something to maintain by
  hand. Do not give a command a second code path.
* **The domain layer does no I/O.** Rules live there and are shared wholesale
  by both surfaces. When a rule needs to read the graph, the rule stays in
  `domain` as a function over sets and the traversal goes in `storage`.
* **`openapi.yaml` is the source of truth for the HTTP API.** The Go server in
  `internal/api` is generated from it by ogen and the TypeScript types in
  `web/ts/api/types.ts` by openapi-typescript; neither is committed and neither
  is ever edited. Routing, decoding, the vocabulary and the length rules are
  therefore what the document says, not a second Go copy of it, and so is what
  each operation accepts: `internal/openapi` reads the declared query
  parameters and request bodies back out of the document, and the handler
  refuses anything else. **To change the API, change the document first**, then
  regenerate, then the handler.

## Conventions

* Cross-cutting HTTP middleware — auth, CSRF, gzip, security headers, recovery,
  static serving, SQLite opening and migration — comes from
  `github.com/mikaelstaldal/go-server-common`. Prefer it over reimplementing.
* Tests use `testify`: `require` for fatal assertions, `assert` for non-fatal.
* Frontend tests are `node --test` over `web/ts/tests/*.test.mjs`.
* A `default` schema value is inherited by every field that references the
  schema. Keep them off the shared vocabulary schemas, or a generated decoder
  fills in a `type` or a `priority` on a `PATCH` that did not send one.
* Every mutation is one `BEGIN IMMEDIATE` transaction, so checks and the write
  they guard happen inside one writer's exclusive turn.
* A released migration batch is never edited, only followed by another.
* The default table output is explicitly not a compatibility surface. `--json`
  and `--compact` are; changing either is a breaking change.
* `awb demo` fills the `demo` project from the table in `internal/cli/demo.go`.
  When a feature is added that the data set could show — a new type, status,
  priority, relation, or anything else worth seeing in a listing or the web UI —
  add or amend a row so it does. `TestDemoCoversTheVocabulary` fails when a
  vocabulary value has no row.

## Rules

* Do not put history or changelog into any document unless the specific document is obviously ment for that. 
We have Git version history.

* Keep it simple, do not make it overly flexible or configurable unless that is required by the goals or 
requested by the user. 
