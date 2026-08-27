# Agent Work Board

[![CI](https://github.com/tofutools/awb/actions/workflows/ci.yml/badge.svg)](https://github.com/tofutools/awb/actions/workflows/ci.yml)

`awb` is an agent-first issue tracker: a single Go binary backed by SQLite, with
a command line interface for coding agents, humans and scripts.

It takes the dependency-aware "what can I work on now" model seriously and
leaves nearly everything else out. There is no permission model, no configurable
workflow engine, no custom fields and no reporting suite. It targets
individuals, small teams and open source projects.

```console
$ awb ready --compact
awb-a3f9c1 P1 open bug "Parser crashes on empty input" #parser
awb-77e0b2 P2 open task "Add fuzz tests for parser"
```

## What makes it agent-first

* **Every command is non-interactive** and has a meaningful exit code, so an
  agent can act on failure instead of parsing prose. Destructive commands take
  a confirmation flag rather than prompting.
* **`--compact` is one line per issue**, designed to cost as little context as
  possible. `--json` is the stable full representation. The default table is for
  humans and nothing should parse it.
* **The vocabulary is fixed and small** — five types, three statuses, five
  priorities, four relation types — so an agent can be taught the whole tool in
  a few lines. `awb agent-guide` prints exactly those lines.
* **`awb ready` answers one question**: what is open, unblocked and unassigned,
  highest priority first. That is the primary entry point.

## Install

```console
$ go install github.com/tofutools/awb@latest
```

Or take a binary for Linux or macOS, amd64 or arm64, from the
[releases](https://github.com/tofutools/awb/releases) page.

Or build from a checkout with `task build`, which produces the same single
static binary with the web UI embedded — no cgo, so it cross-compiles.
`./build.sh` remains available as an alternative.

The required development tools and their versions are declared in `mise.toml`;
run `mise install` to install them.

## Quickstart

```console
$ awb init
$ awb project add awb --name "Agent Work Board"

$ cat .awb.yaml                            # committed at the top of the working tree
project: awb

$ awb create "Parser crashes on empty input" --type bug --priority 1 --label parser
awb-a3f9c1

$ awb create "Add fuzz tests for parser" --discovered-from awb-a3f9c1 --blocked-by awb-a3f9c1
awb-77e0b2

$ awb ready --compact
awb-a3f9c1 P1 open bug "Parser crashes on empty input" #parser

$ awb claim awb-a3f9c1
$ awb close awb-a3f9c1 --reason "Guard against empty token stream"

$ awb ready --compact
awb-77e0b2 P2 open task "Add fuzz tests for parser"
```

Closing the blocker made the second issue ready, with nothing written to it:
`blocked` is derived from the dependency graph rather than stored, so the
recorded state cannot disagree with it.

## Commands

| Command | What it does |
| --- | --- |
| `awb init` | Create the database. The only command that does. |
| `awb create <title>` | Create an issue, with its labels and relations, in one transaction. Prints the new ID. |
| `awb ready` | Open, unblocked, unassigned issues, highest priority first. |
| `awb list` / `blocked` / `search` | The other listings. |
| `awb show <id>` | One issue: relations, blockers and the links found in its description. |
| `awb claim` / `release` / `close` / `reopen` | The only four ways status or assignee ever move. |
| `awb update <id>` | Title, description, type, priority — and nothing else. |
| `awb label add\|rm <id> <label>` | One label per invocation. |
| `awb dep add\|rm <id> --<relation> <id>` | Relations. |
| `awb dep tree <id>` | The decomposition below an issue. |
| `awb delete <id> --force` | Hard delete. Not recoverable. |
| `awb project add\|update\|ls\|rm` | Projects. |
| `awb serve` | The HTTP API and the bundled web UI. |
| `awb agent-guide [--write FILE]` | The usage block to give an agent. |

`awb <command> --help` has the detail. Exit codes are `0` success, `1` runtime
error, `2` usage error, `3` not found, `4` constraint violation.

### Vocabulary

Everything a team wants to express beyond this goes into labels.

* **type** — `epic`, `feature`, `bug`, `task` (default), `chore`
* **status** — `open`, `in_progress`, `closed`
* **priority** — `0` (highest) to `4` (lowest), default `2`
* **relations** — `blocked-by`, `has-parent`, `discovered-from`, `related`

Every relation reads *subject — relation — other*, everywhere: in `awb create`,
in `awb dep add`, and in the API. Only `blocked-by` affects readiness.

An issue ID is `<project>-<hash>`, and any unambiguous prefix — or a bare hash —
works wherever an ID does.

## Teaching an agent

```console
$ awb agent-guide --write AGENTS.md
```

That writes a short block, delimited by marker lines, into the file. Running it
again replaces the block rather than appending a second one, so it is safe to
re-run after upgrading.

## Directory context

`awb` knows nothing about Git. What a directory means is written in that
directory, in `.awb.yaml`, and everything follows from that:

```yaml
project: web        # issues here belong to this project
label: frontend     # work here carries this label
```

The file is found by searching upwards from the working directory, so putting it
at the top of a checkout gives that checkout its own scope. It is meant to be
committed.

The project is the default for `awb create` and the default `--project` filter.
The label is added to issues created here *in addition to* any `--label` given,
so an issue created here stays visible here. `--no-context` ignores both for one
invocation.

Because the file may have been committed by somebody else, it may set only those
two keys. A directory can shape what you see, but cannot redirect where your
issues are stored, claim to be you, or make you send a password somewhere.

## Configuration

`$XDG_CONFIG_HOME/awb/config.yaml`, all keys optional:

```yaml
db: /home/you/.local/share/awb/awb.db   # a path, or an http(s) URL
user: you                               # basic-auth credentials, remote mode only
password: hunter2
identity: you                           # default assignee, --mine, claim --as
project: awb                            # default project for create
color: auto
```

Precedence is command line flags, then `AWB_DB`, `AWB_USER`, `AWB_PASSWORD`,
`AWB_IDENTITY`, `AWB_PROJECT` and `AWB_COLOR`, then `.awb.yaml`, then this file,
then the defaults. The database lives at `$XDG_DATA_HOME/awb/awb.db` unless
told otherwise, and one database spans everything you work on.

## Server and API

```console
$ awb serve
awb serving on http://127.0.0.1:7777/
```

That serves a JSON API, the OpenAPI 3.1 document describing it at
`/openapi.json` and `/openapi.yaml`, and a read-only web UI for browsing
projects, issues, search results and dependency trees.

The API mirrors the CLI one to one, and is complete enough to drive a fully
functional read/write UI — it has optimistic concurrency through `ETag` and
`If-Match`, paging with `X-Total-Count`, and facet endpoints for populating
filter menus. Only the *bundled* UI is limited to reading; making it writable
later is a change to the UI alone.

Pointing the CLI at a server makes every command work against it:

```console
$ AWB_DB=http://127.0.0.1:7777 awb ready --compact
```

Commands behave identically in both modes, because each one is written against
a single interface with two implementations and cannot tell them apart.

**There is authentication but no authorization.** `--basic-auth-file` turns on
HTTP basic authentication against an `htpasswd -B` file, and every user it knows
may do everything every other one may; credentials serve only to say who is
calling. Without it there is no authentication at all, which is why the default
binds loopback. `--cors-origin` lets a separately hosted UI call the API, and is
opt-in for the same reason.

The server does not terminate TLS, so anything beyond one machine wants a
reverse proxy in front of it.

## Concurrency

Several agents can share one database: the file is opened WAL, every mutation is
a single immediate transaction, and `claim` is a compare-and-set, so two agents
racing for the same issue cannot both win. There are no leases and no claim
expiry — a crashed agent leaves an assigned issue that somebody releases
explicitly.

## Development

`spec/ARCHITECTURE.md` describes the shape of the system and why it is that
shape; `spec/TODO.md` lists what is left for future versions; `AGENTS.md`
describes the layout and the conventions. The code and its tests are
authoritative for behaviour.

`task check` compiles the frontend, builds the binary, runs every test and
lints. `./build.sh` performs the same checks and is silent when they all pass.
The component tasks are available separately:

| Task | What it does |
| --- | --- |
| `task build` | Compile the frontend and build `awb`. |
| `task install` | Compile the frontend and install `awb` to `GOBIN` or `GOPATH/bin`. |
| `task init-db` | Initialize the development database. |
| `task test` | Run the frontend and Go tests. |
| `task lint` | Run `golangci-lint`. |
| `task run` | Compile the frontend and run the development server. |
| `task fe:watch` | Recompile the frontend when its TypeScript changes. |

Backend and frontend steps can also be run individually; `task --list` shows
the complete set, as does running `task` without a task name. Set `OUTPUT_DIR`
to choose where a binary is written, for example
`task build OUTPUT_DIR=dist`.

[License](LICENSE) (MIT)
