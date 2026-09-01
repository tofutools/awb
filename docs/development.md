# Development

awb is a Go application with a TypeScript frontend embedded into the resulting
binary. SQLite access uses pure Go, so release binaries do not depend on cgo.

## Toolchain

Required tools and pinned development versions live in `mise.toml`:

- Go;
- Node.js and TypeScript;
- `ogen`;
- `openapi-typescript`;
- `golangci-lint`;
- Task;
- `watchexec` for the development watcher.

With mise installed:

```console
mise install
```

No package manager is used during the build. Browser dependencies under
`web/static/vendor/` are committed pre-built artifacts with license and
provenance files.

## Build and test

`./build.sh` is the complete CI build. It generates both API clients, compiles
the frontend and binary, runs Go and frontend tests, and lints. It is silent on
success and prints the failed step on error:

```console
./build.sh
./build.sh -o dist
```

Task provides component workflows:

| Command | Purpose |
| --- | --- |
| `task generate` | Generate Go server code and TypeScript API types |
| `task build` | Compile frontend and binary |
| `task test` | Run frontend and Go tests |
| `task lint` | Run `golangci-lint` |
| `task run` | Build the frontend and run a development server |
| `task watch` | Restart the server after backend or frontend edits |
| `task check` | Build, test, and lint everything |

Move the development listener with `ADDR` and `PORT`:

```console
task run ADDR=127.0.0.1 PORT=8080
```

Generated `internal/api/` and `web/ts/api-types.ts` output is not committed. A
fresh checkout therefore needs generation before Go or TypeScript compilation.

## Repository map

| Path | Responsibility |
| --- | --- |
| `internal/domain` | Pure vocabulary, validation, graph, readiness, authorization rules, IDs, links, and compact encoders |
| `internal/storage` | SQLite schema, migrations, queries, transaction scope, and attachment blob store |
| `internal/local` | One transactional local implementation of each operation |
| `internal/backend` | Interface used by every command and handler |
| `internal/remote` | Backend interface implemented over HTTP |
| `internal/cli` | Commands, output modes, configuration entry points, demo, and server |
| `internal/handler` | Generated-server implementation of the JSON API |
| `internal/openapi` | Embedded document and operation-contract checks |
| `internal/api` | Generated Go API code; never edit directly |
| `internal/config` | User configuration, directory context, and precedence |
| `internal/awberr` | Shared error taxonomy |
| `web/ts` | Frontend source and tests |
| `web/static` | HTML, CSS, compiled frontend, and vendored browser dependencies |
| `openapi.yaml` | HTTP API source of truth |

See [Architecture](../spec/ARCHITECTURE.md) for the boundaries that this layout
enforces.

## Changing behavior

Behavioral rules live in comments beside the code that enforces them and in the
tests that pin them down. Change both. If the general shape or reasoning changes,
update `spec/ARCHITECTURE.md` in the same pull request.

To change the HTTP API:

1. edit `openapi.yaml`;
2. run `task generate`;
3. implement the generated server interface in `internal/handler` and the
   backend paths as needed;
4. update frontend use and tests.

Do not edit `internal/api` or `web/ts/api-types.ts` directly.

Every mutation should remain one `BEGIN IMMEDIATE` transaction. Domain rules
stay free of I/O. New reads must honor transaction authorization scope; missing
that scope leaks data instead of returning an error.

## CI and releases

Pull requests and pushes to `main` run `./build.sh` on Linux and macOS and
cross-compile every target in `.github/targets.json`. A separate workflow scans
`main` for known vulnerabilities.

Version tags trigger the release workflow. It repeats the native build and
tests, builds each declared target, verifies the version stamp, and publishes
archives plus checksums. The repository does not publish binaries for a platform
whose test suite is never executed in CI.
