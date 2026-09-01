# Agent Work Board

[![CI](https://github.com/tofutools/awb/actions/workflows/ci.yml/badge.svg)](https://github.com/tofutools/awb/actions/workflows/ci.yml)

Agent Work Board (`awb`) is a small issue tracker built for coding agents and
the humans working with them. It is one Go binary backed by SQLite: use it
directly from the command line, or start its HTTP server to share the same work
through the CLI, API, and bundled web UI.

![The awb board showing issues grouped by epic and workflow status](docs/assets/board.png)

awb is deliberately narrower than Jira, Linear, or GitHub Issues. It keeps a
fixed workflow and a small vocabulary, makes dependencies part of the data
model, and gives agents compact, deterministic interfaces. There is no workflow
designer, custom-field system, sprint machinery, or reporting suite to teach an
agent before it can safely pick up work.

## Why awb

- **Agents can discover work.** `awb ready` returns open, unblocked,
  unassigned issues in the team's work order, with priority as the automatic
  fallback.
- **Coordination is atomic.** Claiming an issue joins its assignees and moves it
  to `in_progress` in one transaction. Concurrent agents cannot both perform a
  conflicting write unnoticed.
- **Outputs have explicit audiences.** The default presentation is for humans,
  `--compact` spends little context, and `--json` is the stable automation
  contract.
- **Dependencies mean something.** `blocked-by` drives readiness; parent,
  provenance, and related links preserve useful structure without changing the
  workflow.
- **Local use needs no service.** A SQLite file is enough. The same CLI can
  later point at an awb URL without changing commands.
- **Human collaboration is included.** The web UI provides lists, full-text
  search, editable issue pages, comments, attachments, user administration,
  and a draggable board with saved shareable views.

## Try it

Install with Go:

```console
go install github.com/tofutools/awb@latest
```

Create a database and load the built-in demonstration:

```console
awb init
awb demo
awb serve
```

Open <http://127.0.0.1:7777/>. The demo workspace exercises every issue type,
priority, status, and relation, and gives the board enough structure to explore.

For an empty workspace instead:

```console
awb init
awb workspace create app --name "My application"
printf 'workspace: app\n' > .awb.yaml
awb create "Parser crashes on empty input" --type bug --priority 1 --label parser
```

`.awb.yaml` is directory context: awb finds it by walking upward and uses its
workspace for creation and listings. It is safe and useful to commit.

## An agent-sized workflow

```console
$ awb ready --compact
app-a3f9c1 P1 open bug "Parser crashes on empty input" #parser

$ awb claim app-a3f9c1
$ awb show app-a3f9c1 --json
$ awb comment add app-a3f9c1 --body "Reproduced with an empty token stream."
$ awb close app-a3f9c1 --reason "Guard against the empty token stream"
```

Work commands are non-interactive unless interactivity is explicitly requested.
Account password commands are the deliberate exception: they read from standard
input and hide terminal entry, while scripts can pipe a password or provide a
bcrypt hash. Successful mutations are transactional, errors have classified
exit codes, and the compact vocabulary fits in a short agent instruction. Print
or install that instruction with:

```console
awb agent-guide
awb agent-guide install-skills
```

The bundled skill supports Claude Code, Codex, OpenCode, and GitHub Copilot.
Installing it does not edit a repository's `AGENTS.md`, `CLAUDE.md`, or similar
instruction files.

## The model

Every issue belongs permanently to one **workspace**. Its ID begins with that
workspace key and may be abbreviated wherever the abbreviation is unambiguous.
An issue has one type, one workflow status, a priority, labels, zero or more
assignees, Markdown description and comments, relations, and attachments.

| Concept | Values and meaning |
| --- | --- |
| Type | `epic`, `feature`, `bug`, `task`, `chore` |
| Status | `open`, `in_progress`, `closed` |
| Priority | `0` highest through `4` lowest; default `2` |
| `blocked-by` | The subject cannot become ready until the other issue closes |
| `has-parent` | Places the subject below an epic or other parent |
| `discovered-from` | Records where work was found |
| `related` | Symmetric association with no workflow effect |

Blocked state is derived from the graph rather than stored. Closing a blocker
can therefore make another issue ready without rewriting that issue. A workspace
can be archived into retained read-only history and restored later without
changing its IDs or URLs.

## One backend, three ways to work

The CLI operates directly on a database file by default:

```console
awb ready --compact
```

Start a server to expose the OpenAPI-described JSON API and bundled UI:

```console
awb serve
```

Point the same CLI at it locally, through an environment variable, or through
the user configuration file:

```console
AWB_DB=http://127.0.0.1:7777 awb ready --compact
```

Commands use one backend interface, so direct and remote mode share command
behavior and output. Authorization is intentionally a server concern: direct
mode trusts filesystem access, while server mode can authenticate users and
scope them to workspaces.

## Documentation

- [Getting started](docs/getting-started.md) — build a real workspace and work
  an issue through its lifecycle.
- [Command-line guide](docs/cli.md) — output modes, discovery, relations,
  activity, attachments, and automation behavior.
- [Web UI](docs/web-ui.md) — boards, issue editing, navigation, and
  administration.
- [Configuration](docs/configuration.md) — user settings, directory context,
  environment variables, and precedence.
- [Server and API](docs/server.md) — remote mode, authentication,
  authorization, reverse proxies, and backups.
- [Development](docs/development.md) — repository layout, generation, builds,
  tests, and releases.
- [Architecture](spec/ARCHITECTURE.md) — the system boundaries and the reasons
  behind them.
- [Future work](spec/TODO.md) — intentionally deferred capabilities.

Run `awb <command> --help` for the exact command contract of the installed
version.

## Project status

awb is beta software. CLI and API compatibility may change before 1.0, but the
database is migrated forward rather than discarded. It is aimed at individuals,
small teams, and open-source projects that want a dependable shared work graph
without operating a large project-management system.

[MIT License](LICENSE)
