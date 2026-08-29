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
$ awb project create awb --name "Agent Work Board"

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

To see the rest of it without typing any of it, `awb demo` fills a `demo`
project with a sample data set covering every type, priority, status and
relation. It refuses while that project exists, since it replaces the project
wholesale rather than reconciling it; `awb demo --force` says that deleting
whatever is under the key is meant.

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
| `awb attach add <id> <file>` | Attach a file to an issue. Prints the new attachment ID. |
| `awb attach list <id>` | The files attached to an issue. |
| `awb attach show\|get\|delete <id> <name>` | One attachment: its metadata, its content, or its deletion. |
| `awb delete <id> --force` | Hard delete. Not recoverable. |
| `awb project create\|update\|show\|list\|delete` | Projects. |
| `awb demo [--force]` | Fill a `demo` project with a sample data set. `--force` replaces an existing one. |
| `awb serve` | The HTTP API and the bundled web UI. |
| `awb agent-guide [--write FILE]` | The usage block to give an agent. |

`awb <command> --help` has the detail. Exit codes are `0` success, `1` runtime
error, `2` usage error, `3` not found, `4` constraint violation.

A description is Markdown, and `awb show` and `awb project show` draw it as
such on a terminal: emphasis, headings, lists, code and links the terminal can
open. Piped or redirected the description is the source text exactly as it was
written, and so it is under `--json` and `--compact`.

### Attachments

Arbitrary files can be attached to an issue — a log, a stack trace, a
screenshot, anything a link cannot stand in for.

```console
$ awb attach add awb-5c1d84 ./stack-trace.txt
$ awb attach list awb-5c1d84 --compact
awb-5c1d84 214 9f86d0…b0f00a "text/plain; charset=utf-8" "stack-trace.txt"
$ awb attach get awb-5c1d84 stack-trace.txt --output ./trace.txt   # stdout without it
$ awb attach delete awb-5c1d84 stack-trace.txt --force
```

An attachment is addressed by its issue and its name, the way a label is, and
holds no id of its own — so an issue holds at most one attachment under any one
name, and `--name` is how you attach a second `screenshot.png`.

The content does **not** go in the database. It is stored as one file per
distinct content in the attachments directory — `attachments` beside the
database file unless `--attachments`, `AWB_ATTACHMENTS` or the configuration
file says otherwise, so it can sit on a filesystem of its own. Each file is
named by the SHA-256 of what is in it, so two attachments holding the same
bytes share one copy, and removing one leaves the other's content where it is.

What the database holds is the metadata: name, content type, size and that
digest. An attachment is immutable — nothing changes one — and is at most
32 MiB. Its content type is sniffed from the first bytes unless
`--content-type` states it, sniffed from the content rather than the extension
so that the same file is typed the same way on every machine.

The server serves content as `application/octet-stream` with a
`Content-Disposition` of `attachment` whatever the recorded type says, because
uploads come back from the same origin as the UI and a browser must not be
invited to render one there.

Content is streamed in both directions and never held in memory whole, so what
one transfer costs the server is a copy buffer rather than the size of the
file. It is also the one response the server does not compress: an attachment
is opaque bytes and as likely as not already compressed, so gzipping it would
spend time and memory to make it no smaller — and that is what leaves the
download free to state its `Content-Length`, so a client can show progress
instead of reading an unbounded stream to its end.

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
issues or your files are stored, claim to be you, or make you send a password
somewhere.

## Configuration

`$XDG_CONFIG_HOME/awb/config.yaml`, all keys optional:

```yaml
db: /home/you/.local/share/awb/awb.db   # a path, or an http(s) URL
attachments: /files/awb/attachments     # defaults to "attachments" beside the database
user: you                               # basic-auth credentials, remote mode only
password: hunter2
identity: you                           # default assignee, --mine, claim --as
project: awb                            # default project for create
color: auto
```

Precedence is command line flags, then `AWB_DB`, `AWB_ATTACHMENTS`, `AWB_USER`,
`AWB_PASSWORD`, `AWB_IDENTITY`, `AWB_PROJECT` and `AWB_COLOR`, then `.awb.yaml`,
then this file, then the defaults. The database lives at
`$XDG_DATA_HOME/awb/awb.db` unless told otherwise, and one database spans
everything you work on. Attachment content lives in `attachments` beside it,
and `awb init` creates both.

## Server and API

```console
$ awb serve
2026/05/17 09:41:02 awb serving on http://127.0.0.1:7777/
```

That serves a JSON API, the OpenAPI 3.1 document describing it at
`/openapi.json` and `/openapi.yaml`, and a read-only web UI for browsing
projects, issues, search results and dependency trees.

That document — `openapi.yaml` in this repository — is the source of truth
rather than a description written afterwards: the server's routing, decoding
and validation are generated from it, as are the TypeScript types the bundled
UI is written against.

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

`--addr` and `--port` are independent: `--addr 0.0.0.0` reaches other machines
on the default port, `--port 8080` moves the port and leaves the binding alone.

### Behind a reverse proxy

The server does not terminate TLS, so anything beyond one machine wants a
reverse proxy — Apache, nginx or another — in front of it:

```console
$ awb serve --public-url https://example.com/awb/ --https
```

`--public-url` is the URL the proxy publishes the server under, and the proxy
must map that URL to this server with the base path stripped, which is what both
`ProxyPass /awb/ http://127.0.0.1:7777/` and nginx's
`location /awb/ { proxy_pass http://127.0.0.1:7777/; }` do. Everything the
bundled UI asks for is a relative URL, so the base path reaches nothing but the
`<base href>` of the page it is served on; the API is unaffected, and the CLI's
remote mode already carries the base path in its own `--db` URL.

Give it whenever a browser reaches the server by an origin other than the one it
listens on, path or no path: cross-site write protection compares the browser's
origin against this one.

`--https` sends `Strict-Transport-Security`. It is opt-in rather than implied by
an `https://` public URL, because it tells every browser that saw it to refuse
plain HTTP to that host for a year — including any other application on it. It
goes with an `https://` `--public-url`, or with none; the two contradicting each
other is refused, a browser ignoring the header when it arrives over plain HTTP.

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

`task check` generates the code `openapi.yaml` specifies, compiles the
frontend, builds the binary, runs every test and lints. `./build.sh` performs
the same checks and is silent when they all pass. Neither generated output is
committed, so a fresh checkout does not compile until one of those has run.
The component tasks are available separately:

| Task | What it does |
| --- | --- |
| `task generate` | Generate the Go server and the TypeScript types from `openapi.yaml`. |
| `task build` | Compile the frontend and build `awb`. |
| `task install` | Compile the frontend and install `awb` to `GOBIN` or `GOPATH/bin`. |
| `task init-db` | Initialize the development database. |
| `task test` | Run the frontend and Go tests. |
| `task lint` | Run `golangci-lint`. |
| `task run` | Compile the frontend and run the development server. |
| `task watch` | Restart the development server after backend or frontend changes. |

Backend and frontend steps can also be run individually; `task --list` shows
the complete set, as does running `task` without a task name. Set `OUTPUT_DIR`
to choose where a binary is written, for example
`task build OUTPUT_DIR=dist`.

The development server binds loopback by default. To make it reachable from
other machines, or to move it off port 7777, set `ADDR` or `PORT` for either
server task:

```console
$ task run ADDR=0.0.0.0
$ task watch ADDR=0.0.0.0 PORT=8080
```

This exposes the server without authentication unless `--basic-auth-file` is
also passed after `--`.

[License](LICENSE) (MIT)
