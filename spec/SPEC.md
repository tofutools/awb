# Agent Work Board (awb) — Specification

## 1. Overview

`awb` is an agent-first issue tracker: a single Go binary backed by SQLite, driven primarily
through a command line interface that is equally usable by coding agents, humans and scripts.

It targets individuals, small teams and open source projects. It deliberately does not target
enterprises: there is no permission model, no configurable workflow engine, no custom fields and
no reporting suite.

The design is inspired by [Beads](https://beads.gascity.com/), in particular its dependency-aware
"what can I work on right now" model and its assumption that agents, not humans, file and close
most issues. It differs from Beads in three deliberate ways:

* Storage is a plain SQLite database. There is no version control, no branching and no merge.
* The issue database is not tied to one Git repository. One database spans many projects and many
  repositories; repository association is an optional per-issue tag used for convenience filtering.
* There are no exotic modelling concepts (no formulas, molecules, wisps or gates).

### 1.1 Design goals

1. **Agent-first.** Every operation is a non-interactive command with stable, parseable output and
   meaningful exit codes. Output modes exist that minimise context consumption.
2. **Small surface.** A fixed vocabulary of types, statuses and priorities that an agent can be
   taught in a few lines of instructions.
3. **Single source of truth.** One database per user, shared by all their projects and
   repositories, reachable over HTTP by tools other than the CLI.
4. **No ceremony.** No server required, no configuration required, no repository required.

### 1.2 Non-goals

Versioning, history, merge or offline replication; comments and discussion threads; field-level
audit logs; sprints, boards, burndowns or time tracking; notifications; continuous synchronisation
with GitHub, Jira or Linear; user accounts, roles and permissions; custom fields or configurable
workflows; an MCP server; bulk import from stdin; attachment or blob storage; a write-capable web
UI.

## 2. Concepts

### 2.1 Project

The top-level organising unit. Every issue belongs to exactly one project.

| Field | Description |
| --- | --- |
| `key` | Short lowercase identifier, e.g. `awb`. Unique. Used as the issue ID prefix. Immutable. |
| `name` | Human-readable name. |
| `description` | Optional markdown text. |

### 2.2 Repository

An optional, database-global registry of Git repositories. A repository is not owned by a project:
one repository may serve several projects and one project may span several repositories.

| Field | Description |
| --- | --- |
| `name` | Short identifier, e.g. `awb`. Unique. |
| `remotes` | Zero or more Git remote URLs. |
| `paths` | Zero or more absolute local working-tree paths. |
| `default_project` | Optional project used when creating issues from inside this repository. |

Either a remote URL or a local path is enough to identify the repository; see §5.

### 2.3 Issue

| Field | Type | Description |
| --- | --- | --- |
| `id` | string | `<project-key>-<hash>`, e.g. `awb-a3f9c1`. Assigned at creation, immutable. |
| `project` | project key | Required. Immutable after creation. |
| `title` | string | Required, single line. |
| `description` | markdown | Optional. The only free text field on the issue. Links to pull requests, CI runs, logs and documents are written as ordinary Markdown links inside it. |
| `type` | enum | `bug`, `feature`, `task`, `chore`. Default `task`. |
| `status` | enum | `open`, `in_progress`, `closed`. Default `open`. |
| `priority` | integer | `0` (highest) to `3` (lowest). Default `2`. |
| `labels` | set of strings | Free-form, lowercase, no spaces. |
| `assignee` | string | Free-form, e.g. `mikael` or `claude-1`. Empty means unassigned. |
| `repo` | repository name | Optional. |
| `created_at` | timestamp | Set automatically (UTC, RFC 3339). |
| `updated_at` | timestamp | Set automatically on every write. |

`blocked` is **not** a stored status. It is derived: an issue is blocked when at least one issue
that `blocks` it is not closed. This makes it impossible for the recorded state to disagree with
the dependency graph.

The fixed vocabulary above is not configurable. Everything a team wants to express beyond it goes
into labels.

### 2.4 Relation

A directed link between two issues. Both issues may belong to different projects.

| Type | Meaning |
| --- | --- |
| `blocks` | `A blocks B`: B cannot start until A is closed. Drives readiness. |
| `parent-child` | `A parent-of B`: B is part of decomposing A. |
| `discovered-from` | `B discovered-from A`: B was found while working on A. Provenance only. |
| `related` | Loose, symmetric association. No behaviour attached. |

`blocks` and `parent-child` graphs must remain acyclic; a command that would create a cycle fails.
An issue has at most one parent. Relations are deleted with either endpoint issue.

### 2.5 External artifacts

There is no separate attachment or link entity. References to pull requests, CI runs, logs, design
documents and files on disk are Markdown links in the issue description. The database therefore
stores no file contents and no link records.

To make those links useful without a separate model, `awb` parses the description as Markdown:
`awb show` lists the links it finds under the rendered text, and the web UI renders the description
and turns the links into anchors.

### 2.6 Readiness

An issue is **ready** when all of the following hold:

* `status` is `open`,
* it is not blocked (§2.3),
* `assignee` is empty,
* it is not excluded by the active repository context (§5).

`awb ready` is the primary agent entry point: it answers "what should I pick up next".

## 3. Storage

A single SQLite database file holds projects, repositories, issues and relations.

* Default location: `$XDG_DATA_HOME/awb/awb.db`, falling back to `~/.local/share/awb/awb.db`.
* Overridable, in increasing precedence, by the config file, the `AWB_DB` environment variable and
  the `--db` global flag.
* The value is either a filesystem path (direct mode) or an `http(s)://` URL (remote mode, §6).

There is no per-repository or per-directory database and no upward directory search: one user has
one database unless they explicitly point at another.

Schema migrations are embedded in the binary and applied automatically when the database is opened.
The database is opened with WAL journalling, foreign keys enabled and a busy timeout, so several
local processes — for example three agents in three terminals — can use the same file safely.

Full text search over titles and descriptions uses SQLite FTS5, kept in sync by triggers.

## 4. Command line interface

### 4.1 Conventions

* Every command is non-interactive and safe to script. Destructive commands require a confirmation
  flag rather than a prompt.
* Global flags: `--db`, `--json`, `--compact`, `--all`, `--repo`, `--project`, `--no-color`.
* Output modes:
  * default — aligned, coloured table for humans;
  * `--compact` — one line per issue, no padding, minimal punctuation, designed to consume as
    little agent context as possible:
    `awb-a3f9c1 P1 open bug "Parser crashes on empty input" @claude-1 #parser repo:awb`
  * `--json` — stable JSON, one object or one array per invocation, suitable for `jq`.
* Exit codes: `0` success · `1` runtime error · `2` usage error · `3` not found ·
  `4` constraint violation (e.g. dependency cycle).
* Errors go to stderr as a single line; with `--json`, as `{"error": "..."}`.

### 4.2 Setup

| Command | Description |
| --- | --- |
| `awb init` | Create the database if absent. With no arguments it is idempotent. |
| `awb project add <key> [--name] [--description]` | Create a project. |
| `awb project ls` | List projects with open issue counts. |
| `awb project rm <key> --force` | Delete a project. Refuses while it has issues unless `--force` is repeated with `--cascade`. |
| `awb repo add <name> [--remote URL]... [--path DIR]... [--project KEY]` | Register a repository. With no flags inside a working tree, infers remotes and path from it. |
| `awb repo ls` | List repositories and their matching rules. |
| `awb repo rm <name>` | Unregister. Issues tagged with it keep the tag as a dangling name until retagged. |
| `awb agent-guide [--write FILE]` | Print a compact usage block for agents; `--write` appends it to `AGENTS.md`/`CLAUDE.md`. |

### 4.3 Issues

| Command | Description |
| --- | --- |
| `awb create <title> [--type] [--priority] [--label]... [--assignee] [--project] [--repo] [--description] [--description-file FILE] [--parent ID] [--blocked-by ID]... [--discovered-from ID]` | Create an issue. Prints the new ID. |
| `awb show <id>` | Full issue, including relations, derived blocked state and the Markdown links found in the description. |
| `awb list [filters]` | List issues. |
| `awb ready [filters]` | List ready issues (§2.6), highest priority first. |
| `awb blocked [filters]` | List open issues that are blocked, each with its blockers. |
| `awb search <terms> [filters]` | Full text search over title and description. |
| `awb update <id> [--title] [--description] [--type] [--priority] [--assignee] [--repo] [--status]` | Change fields. |
| `awb label add\|rm <id> <label>...` | Manage labels. |
| `awb claim <id> [--as NAME]` | Atomically set assignee and `status=in_progress`. Fails if already assigned to someone else unless `--force`. |
| `awb release <id>` | Clear the assignee and set status back to `open`. |
| `awb close <id> [--reason TEXT]` | Set `status=closed`. `--reason` is appended to the description. |
| `awb reopen <id>` | Set `status=open`. |
| `awb delete <id> --force` | Hard delete the issue and its relations. Not recoverable. |

Filters accepted by `list`, `ready`, `blocked` and `search`:
`--status`, `--type`, `--priority`, `--priority-max`, `--label`, `--assignee`, `--mine`,
`--unassigned`, `--project`, `--repo`, `--all`, `--parent`, `--limit`, `--sort`.

Defaults: closed issues are hidden unless `--status closed`/`--status all` is given; results are
sorted by priority ascending, then `created_at` ascending. Nothing is ever archived or purged —
closed issues remain queryable forever.

`--mine` resolves to the configured identity (§7), which is also the default `--as` for `claim`.

### 4.4 Relations

| Command | Description |
| --- | --- |
| `awb dep add <id> --blocks <id>` | Record that one issue blocks another. |
| `awb dep add <id> --parent <id>` | Set the parent of an issue. |
| `awb dep add <id> --related <id>` | Record a loose association. |
| `awb dep add <id> --discovered-from <id>` | Record provenance. |
| `awb dep rm <id> <type> <id>` | Remove a relation. |
| `awb dep tree <id>` | Print the parent-child subtree with status and blocked markers. |

### 4.5 Serving

| Command | Description |
| --- | --- |
| `awb serve [--addr 127.0.0.1:7777]` | Serve the HTTP API and the read-only web UI over the local database. |

## 5. Git repository context

`awb` is usable with no Git repository at all. When it is run inside a working tree it uses that
as context to reduce noise.

Resolution, performed once per invocation:

1. Walk up from the working directory looking for a `.git` directory or file. If none is found,
   there is no repository context.
2. Collect the working tree's top-level path and the URLs of all its remotes.
3. Match against registered repositories: a repository matches if any of its `paths` is a prefix of
   the working tree path, or if any of its `remotes` matches a remote of the working tree after
   normalisation (scp-style URLs rewritten to a canonical form, `.git` suffix stripped, host
   lower-cased, credentials and ports ignored).
4. If several repositories match, the one with the longest matching path wins; if the ambiguity
   cannot be resolved that way, the command fails with exit code 4 and asks for `--repo`.

When a repository context is active, list-like commands (`list`, `ready`, `blocked`, `search`)
show only issues tagged with that repository. Untagged, project-level issues are hidden by default,
on the assumption that work inside a checkout is work on that checkout.

A repository may opt out of that assumption in its repository-level configuration file (§7.2):

```toml
[repo]
include_untagged = true    # also show project-level issues in this working tree
```

Overrides on the command line, in increasing precedence over the above:

* `--repo <name>` — use that repository as context regardless of the working directory.
* `--repo none` — show only untagged issues.
* `--all` — disable repository filtering entirely.

`awb create` tags the new issue with the context repository, and picks the project from
`--project`, else the context repository's `default_project`, else the configured default project;
if none of these yields a project the command fails with exit code 2.

## 6. Server mode

`awb serve` is optional. It exists so that things other than the CLI can reach the database —
third-party user interfaces, dashboards and integrations today, a shared team instance later (§10).
It serves:

* a JSON HTTP API that mirrors the CLI one-to-one,
* the OpenAPI document describing that API (§6.1), and
* a read-only web UI for browsing projects, issues and dependency trees.

Setting `AWB_DB`/`--db` to the server's URL makes the CLI operate against it; every command
behaves identically to direct mode, and repository context is still resolved on the client.

**There is no authentication and no authorization.** Any client that can reach the port has full
read and write access. The server therefore binds `127.0.0.1` by default and is documented as a
local surface for the machine's own user; exposing it beyond that requires an external reverse
proxy supplying TLS and access control. A built-in answer to shared access belongs to version 2
(§10).

API shape (illustrative; the OpenAPI document is authoritative):

```
GET    /api/issues?status=open&label=parser&repo=awb
POST   /api/issues
GET    /api/issues/{id}
PATCH  /api/issues/{id}
DELETE /api/issues/{id}
POST   /api/issues/{id}/relations
DELETE /api/issues/{id}/relations/{type}/{other}
GET    /api/ready
GET    /api/blocked
GET    /api/search?q=...
GET    /api/projects        POST /api/projects
GET    /api/repos           POST /api/repos
```

Request and response bodies use exactly the same JSON representation as `--json` output, so agents
can move between the two transports without relearning anything. Query parameters carry the same
names as the corresponding CLI filter flags. Repository context is resolved on the client, so the
server never inspects the caller's working directory: the client sends a resolved `repo` parameter.

### 6.1 OpenAPI

The HTTP API is specified by an OpenAPI 3.1 document, so third-party user interfaces and
integrations can be built against it and clients can be generated from it. The document lives in
the repository, is embedded in the binary, and is served at `/openapi.json` and `/openapi.yaml`.

* Its component schemas are the CLI's `--json` structures: `Issue`, `Relation`, `Project`, `Repo`,
  `Error`. There is no second, HTTP-only representation of anything — if the shapes ever diverge,
  the JSON output is what changed and both must be corrected together.
* Enumerations (`type`, `status`, relation types) and the priority range are declared in the schema,
  so a generated client validates the same vocabulary the CLI enforces.
* Errors use the exit-code taxonomy of §4.1 mapped onto status codes: `400` usage, `404` not found,
  `409` constraint violation, `500` runtime error, with an `Error` body.
* The document is treated as a compatibility contract: within a major version, changes are additive
  only. `info.version` tracks the API, not the binary.

Authoring the document is deferred to implementation; this specification fixes only that it must
exist, be authoritative for the API, and reuse the CLI JSON schemas.

## 7. Configuration

Both configuration files are TOML and both are optional; `awb` runs with neither.

### 7.1 User configuration

`$XDG_CONFIG_HOME/awb/config.toml`, falling back to `~/.config/awb/config.toml`:

```toml
db       = "/home/mikael/.local/share/awb/awb.db"  # path or http(s) URL
identity = "mikael"                                 # default assignee, --mine, claim --as
project  = "awb"                                    # default project for create
color    = "auto"
```

When `identity` is unset it defaults to the OS username.

### 7.2 Repository configuration

`.awb.toml` in the root of a Git working tree. It is meant to be committed, so that a repository
carries its own `awb` settings for everyone who checks it out, and it is a general-purpose file
rather than a single-purpose filter switch:

```toml
repo    = "awb"      # bind this working tree to a registered repository by name
project = "awb"      # default project for issues created here

[repo]
include_untagged = true    # see §5
```

A `repo` key here identifies the repository directly and takes precedence over the URL and path
matching of §5, which makes registration optional for anyone who clones the repository.

Because this file comes from a checkout and may not have been written by the person running the
command, it may **not** set `db`, `identity` or `color`: a repository can shape what you see, but
cannot redirect where your issues are stored or claim to be you. Unknown keys are ignored, so
future versions can add settings without breaking older binaries.

### 7.3 Precedence

Command line flags, then environment variables (`AWB_DB`, `AWB_IDENTITY`, `AWB_PROJECT`,
`AWB_COLOR`), then the repository configuration file, then the user configuration file, then the
built-in defaults.

## 8. Identifiers

An issue ID is `<project-key>-<hash>` where the hash is six lowercase hexadecimal characters
generated randomly and checked for uniqueness within the project at insert time. IDs are stable and
never reused, including after deletion.

Commands accept an unambiguous hash prefix in place of a full ID, and accept a bare hash when it is
unique across the database.

## 9. Implementation notes

* Go, one statically linked binary, no cgo, so `go install` and cross-compilation both work.
* A pure Go SQLite driver (`modernc.org/sqlite`) provides FTS5 without a C toolchain.
* A CommonMark parser (e.g. `goldmark`) extracts links from descriptions for `awb show` and renders
  them in the web UI. Descriptions are stored verbatim; parsing is a read-time concern only, and
  rendered HTML is sanitised.
* Layering: a storage package owning the schema and queries; a domain package owning readiness,
  relation validation and repository matching; thin CLI and HTTP adapters over it, so both surfaces
  exercise the same code paths.
* All timestamps are stored in UTC.
* Concurrency is handled by SQLite itself: short transactions, WAL mode and a busy timeout. There
  are no leases, locks or claim expiry — `claim` is a single atomic update, and a crashed agent
  leaves an assigned issue that a human or another agent releases explicitly.

## 10. Delivery plan

### 10.1 Version 1 — single user, single machine

Everything specified above, with no synchronisation and no shared deployment: one person, one
machine, one database file, any number of local processes and agents against it. `awb serve` is in
scope as a local surface — it is what third-party UIs and integrations are built against — but is
bound to loopback and unauthenticated, so it serves the machine's own user.

### 10.2 Version 2 — multi-user and multi-machine

Deferred: identity and authentication for the server, a shared instance for a team or an open source
project, and some form of synchronisation or replication between databases. Nothing about it is
designed here, and version 1 must not carry half of it.

### 10.3 Preparations made now

Choices that cost version 1 nothing but keep version 2 open:

* **Collision-resistant IDs.** Issue IDs are random hashes rather than sequence numbers (§8), so
  issues created independently on different machines can be merged without renumbering.
* **Identity on every mutation.** Every mutating command and API call resolves a caller identity
  (§7), even though version 1 mostly records it only as `assignee`. The plumbing exists when
  attribution or authentication needs it.
* **A domain layer, not a database.** All mutations go through a small set of explicit, atomic
  domain operations (§9) rather than ad-hoc SQL in the CLI or HTTP handlers. That is the seam where
  a change log or replication hook is later inserted.
* **Versioned schema from the start.** Migrations are numbered and recorded in the database from
  the first release, so a version 2 schema can be reached from any version 1 database.
* **UTC timestamps.** `created_at` and `updated_at` are UTC on every row, which is what any
  cross-machine ordering or conflict rule would need.
* **A published API contract.** The OpenAPI document (§6.1) fixes the wire format before other
  people build against it, so a version 2 server can stay compatible with version 1 clients.

Explicitly *not* prepared for: a change log, tombstones for deletions, vector clocks or any other
merge machinery. Version 1 deletes rows outright and keeps no history, and version 2 is free to
decide that a synchronised database needs a different mechanism.

## 11. Worked example

```console
$ awb init
$ awb project add awb --name "Agent Work Board"
$ awb repo add awb --project awb           # inside the working tree; infers remote and path

$ awb create "Parser crashes on empty input" --type bug --priority 1 --label parser
awb-a3f9c1

$ awb create "Add fuzz tests for parser" --discovered-from awb-a3f9c1 --blocked-by awb-a3f9c1
awb-77e0b2

$ awb ready --compact
awb-a3f9c1 P1 open bug "Parser crashes on empty input" #parser repo:awb

$ awb claim awb-a3f9c1
$ awb close awb-a3f9c1 --reason "Guard against empty token stream"

$ awb ready --compact
awb-77e0b2 P2 open task "Add fuzz tests for parser" repo:awb
```
