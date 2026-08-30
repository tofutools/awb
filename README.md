# Agent Work Board

[![CI](https://github.com/tofutools/awb/actions/workflows/ci.yml/badge.svg)](https://github.com/tofutools/awb/actions/workflows/ci.yml)

`awb` is an agent-first issue tracker: a single Go binary backed by SQLite, with
a command line interface for coding agents, humans and scripts. It is a
deliberately smaller, agent-first alternative to Jira, Linear and GitHub Issues;
different projects can use different trackers.

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
  a confirmation flag rather than prompting. The one interactive thing there is,
  `-i` on the list commands, refuses to run without a terminal.
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
| `awb status` | Show the active local database or remote server and web UI, identity, configuration, environment overrides and per-project issue counts. |
| `awb create <title>` | Create an issue, with its labels and relations, in one transaction. Prints the new ID. |
| `awb ready` | Open, unblocked, unassigned issues, highest priority first. |
| `awb list` / `blocked` / `search` | The other listings. |
| `awb show <id>` | One issue: relations, blockers and the links found in its description. |
| `awb claim` / `release` / `close` / `reopen` | The only post-creation ways status or assignees move. Claim joins; release leaves. |
| `awb update <id>` | Title, description, type, priority — and nothing else. |
| `awb label add\|rm <id> <label>` | One label per invocation. |
| `awb dep add\|rm <id> --<relation> <id>` | Relations. |
| `awb dep tree <id>` | The decomposition below an issue. |
| `awb comment add\|list <id>` | Add or list append-only Markdown comments. |
| `awb activity <id>` | Comments and recorded changes, newest first. |
| `awb attach add <id> <file>` | Attach a file to an issue. Prints the new attachment ID. |
| `awb attach list <id>` | The files attached to an issue. |
| `awb attach show\|get\|delete <id> <name>` | One attachment: its metadata, its content, or its deletion. |
| `awb delete <id> --force` | Hard delete. Not recoverable. |
| `awb project create\|update\|show\|list\|delete` | Projects. |
| `awb project grant\|revoke <key> <user>` | Who may work in a project, and at which level. |
| `awb project members <key>` | The users with access to a project. |
| `awb user add\|update\|show\|list\|delete` | The accounts a server authenticates and authorizes. |
| `awb demo [--force]` | Fill a `demo` project with a sample data set. `--force` replaces an existing one. |
| `awb serve` | The HTTP API and the bundled web UI. |
| `awb agent-guide [--write FILE]` | The usage block to give an agent. |
| `awb agent-guide install-skills [--harness NAME]` | Install that guide as a skill for agent harnesses. |

`awb <command> --help` has the detail. Exit codes are `0` success, `1` runtime
error, `2` usage error, `3` not found, `4` constraint violation, `5` forbidden.

A description is Markdown, and `awb show` and `awb project show` draw it as
such on a terminal: emphasis, headings, lists, code and links the terminal can
open. Piped or redirected the description is the source text exactly as it was
written, and so it is under `--json` and `--compact`.

### Comments and activity

Each issue has an append-only timeline containing Markdown comments and compact
system records of its changes.

```console
$ awb comment add awb-5c1d84 --body "Reproduced with an empty token stream."
$ awb comment add awb-5c1d84 --body-file investigation.md
$ awb activity awb-5c1d84 --compact
$ awb comment list awb-5c1d84 --json
```

An ordinary comment records the current identity and is stored byte-for-byte as
sent. A successful mutation records its change in the same transaction; a
failed or no-op mutation records nothing. A non-empty `awb close --reason` is a
typed comment with action `closed`, recorded atomically with that transition;
it remains in the timeline if the issue is reopened. This is a work log rather
than immutable compliance history: hard-deleting an issue deletes its activity
too.

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

There are two independent ways to teach agents about awb. Installing the skill
does not read or change `AGENTS.md`, `CLAUDE.md` or any other project file.

Install the bundled `awb` skill into every supported agent harness's user
skill directory:

```console
$ awb agent-guide install-skills
```

The command is idempotent and refreshes the installed copy after an awb
upgrade. To target only particular harnesses, repeat `--harness` with any of
`claude`, `codex`, `opencode` and `copilot`; `all` is the default. The native
user roots are `~/.claude/skills`, `~/.agents/skills` and
`${CODEX_HOME:-~/.codex}/skills`, `${XDG_CONFIG_HOME:-~/.config}/opencode/skills`,
and `${COPILOT_HOME:-~/.copilot}/skills`, respectively.

Alternatively, keep the compact guide in a checked-in agent instruction file:

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
user: you                               # the account to log in as, remote mode only
password: hunter2
identity: you                           # default assignee, --mine, claim --as
project: awb                            # default project for create and issue listings
color: auto
```

Precedence is command line flags, then `AWB_DB`, `AWB_ATTACHMENTS`, `AWB_USER`,
`AWB_PASSWORD`, `AWB_IDENTITY`, `AWB_PROJECT` and `AWB_COLOR`, then `.awb.yaml`,
then this file, then the defaults. `AWB_CONFIG_FILE` reads this file from
somewhere else; a path it names must exist, so that a typo cannot quietly leave
you with the defaults. A leading `~` in `AWB_CONFIG_FILE`, `AWB_DB` or
`AWB_ATTACHMENTS` means the current user's home directory. The database lives at
`$XDG_DATA_HOME/awb/awb.db` unless told otherwise, and one database spans
everything you work on. Attachment content lives in `attachments` beside it,
and `awb init` creates both. Pass `--all-projects` to an issue listing command
to ignore the configured project for that invocation.

## Server and API

```console
$ awb serve
2026/05/17 09:41:02 awb serving on http://127.0.0.1:7777/
```

That serves a JSON API, the OpenAPI 3.1 document describing it at
`/openapi.json` and `/openapi.yaml`, and a web UI for browsing and editing
projects and issues, searching, viewing dependency trees and posting comments.

That document — `openapi.yaml` in this repository — is the source of truth
rather than a description written afterwards: the server's routing, decoding
and validation are generated from it, as are the TypeScript types the bundled
UI is written against.

The API mirrors the CLI one to one, and is complete enough to drive a fully
functional read/write UI — it has optimistic concurrency through `ETag` and
`If-Match`, paging with `X-Total-Count`, and facet endpoints for populating
filter menus. The bundled UI uses that write surface to edit project and issue
fields, lifecycle state, labels, relations, attachments and comments.

Pointing the CLI at a server makes every command work against it:

```console
$ AWB_DB=http://127.0.0.1:7777 awb ready --compact
```

Commands behave identically in both modes, because each one is written against
a single interface with two implementations and cannot tell them apart.

`--cors-origin` lets a separately hosted UI call the API, and is opt-in because
any page in the browser could otherwise reach it.

To test the UI bundled into a local build against an existing awb server
without changing that server, use the UI proxy:

```console
$ awb serve --proxy-to https://example.com/awb/
2026/05/17 09:41:02 awb serving on http://127.0.0.1:7777/
```

The UI and its assets come from the local binary. Requests under `/api/` are
forwarded to the remote server, including its Basic Authentication challenge.
Browser writes must pass the local server's cross-site request check before
they are forwarded. The default loopback binding keeps the proxy local to this
machine, and no CORS change is needed on the remote server.

`--addr` and `--port` are independent: `--addr 0.0.0.0` reaches other machines
on the default port, `--port 8080` moves the port and leaves the binding alone.

### Users and permissions

**The database decides whether the server authenticates.** One holding no user
authenticates nobody, and any client that can reach the port has full read and
write access — which is why the default binds loopback. Adding the first user
turns authentication on, from the next request onwards.

Deleting the last user does not turn it off again. The server answers nothing —
`503`, with no challenge, because no credentials could open a server with no
accounts — until a user is added, which again takes effect from the next
request. That a database has had users is recorded in the database itself, so
it holds whether or not a server was running or watching at the time; turning a
guarded database back into an open one takes saying so.

Which is what `--no-auth` is. A server started with it authenticates nobody
whatever the database holds — it consults no users at all, so adding one does
not close the door either — and taking it back is a restart without the flag.

A server started over a database whose users are already gone starts locked and
says so in its log, rather than refusing: it answers nothing to anybody, so it
exposes nothing wherever it is bound, and it recovers from the next
`awb user add` without a further restart. What refuses to start is a server
that would authenticate nobody where that looks like a mistake: one over a
database that never held a user, and either bound to something other than
loopback, or given `--public-url`, `--https` or `--basic-auth-realm`. The
binding is what other machines can reach; those three reach nothing by
themselves, and each describes a deployment published beyond this machine,
which is the intention the refusal is about.

```console
$ echo hunter2 | awb user add alice --user-admin --project-admin
$ awb project grant awb bob --access regular
```

A password is read from stdin, never from a flag: on the command line it would
be in the process listing and in the shell history. At a terminal it is prompted
for and typed without echo. `--password-hash` takes a bcrypt hash computed
elsewhere instead — what `htpasswd -Bn alice` writes — so the plaintext never
reaches `awb` at all.

A user works in the projects they have been granted access to and sees nothing
else: a project they hold no access to, and every issue in it, is simply not
there for them — absent from every listing, search, facet and tree, and answered
"no such project" rather than "forbidden", since it is not theirs to know about.

The dependency graph is the exception, and deliberately: a visible issue's
relations and blockers may *name* issues in projects you cannot reach, and
whether it is blocked is computed over all of them. Readiness has to be true —
an issue held up by work you cannot see is still held up — and a name is all
that is exposed, since fetching one of those issues is still "no such issue".

| | |
| --- | --- |
| `regular` in a project | Work with its issues: read, create, edit, claim, close, attach. |
| `admin` in a project | That, and granting and revoking the project's other users. |
| `--project-admin` | Create, change and delete projects, and `admin` access in every one. |
| `--user-admin` | Create, change and delete users, which includes granting these two flags. |

`admin` in a project is not power over the project itself: a project's own
existence is `--project-admin`'s, because it is not something its members decide.
Anybody may change their own password and read their own account — `awb user
show` with no argument — without being able to grant themselves anything.

Upgrading from a server that used `--basic-auth-file`: that flag is gone, and
the entries move across without anybody re-typing a password, since a bcrypt
hash is a bcrypt hash.

```console
$ while IFS=: read -r name _; do
>   awb user add "$name" --password-hash "$(grep "^$name:" htpasswd)"
> done < htpasswd
```

**None of this applies to the CLI on a database file.** Direct mode applies no
authorization at all, because whoever can open the file can already read and
write every byte of it, and a check there would be a suggestion rather than a
control. That is how the first user is created, and how an instance whose last
administrator is gone is recovered.

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

That reaches other machines, so it needs a database that holds a user — or
`task run ADDR=0.0.0.0 -- --no-auth` to serve one that does not.

[License](LICENSE) (MIT)
