# Agent Work Board (awb) — Specification

## 1. Overview

`awb` is an agent-first issue tracker: a single Go binary backed by SQLite, with a command line
interface for coding agents, humans and scripts.

It targets individuals, small teams and open source projects. It deliberately does not target
enterprises: there is no permission model, no configurable workflow engine, no custom fields and
no reporting suite.

The design takes Beads' dependency-aware "what can I work on now" model and agent-first assumption,
but differs in three ways:

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

Versioning, history, merge or offline replication; comments; audit logs; planning and reporting
features such as sprints, boards, burndowns and time tracking; notifications; continuous external
tracker synchronisation; accounts and permissions; custom fields or workflows; an MCP server;
bulk stdin import; attachments and blobs.

The web UI shipped in version 1 is read-only. That is a scope decision about the bundled UI, not
about the API: the HTTP API is required to be complete enough that a fully-functional read/write
web UI can be built on it, by this project later or by somebody else now (§6.2).

## 2. Concepts

### 2.1 Project

The top-level organising unit. Every issue belongs to exactly one project.

| Field | Description |
| --- | --- |
| `key` | Short identifier, e.g. `awb`. Lowercase ASCII letters, digits and hyphens, starting with a letter, at most 16 characters. Unique. Used as the issue ID prefix. Immutable. |
| `name` | Human-readable name. Defaults to the key and is never empty (§4.1). |
| `description` | Optional markdown text. |

### 2.2 Repository

An optional, database-global registry of Git repositories. A repository is not owned by a project:
one repository may serve several projects and one project may span several repositories.

An issue names at most one repository in its `repo` field; this document calls that being *tagged*
with the repository, and it is unrelated to the issue's `labels`.

| Field | Description |
| --- | --- |
| `name` | Short identifier, e.g. `awb`. Same character rules as a project key, and not the reserved word `none`. Unique. |
| `remotes` | Zero or more Git remote URLs. |
| `paths` | Zero or more absolute local working-tree paths, symlinks resolved. |
| `default_project` | Optional project used when creating issues from inside this repository. Must name an existing project; cleared if that project is deleted. |

Either a remote URL or a local path is enough to identify the repository; see §5.

### 2.3 Issue

| Field | Type | Description |
| --- | --- | --- |
| `id` | string | `<project-key>-<hash>`, e.g. `awb-a3f9c1`. Assigned at creation, immutable. |
| `project` | project key | Required. Immutable after creation. |
| `title` | string | Required, single line. Leading and trailing whitespace is trimmed, and a title that is empty after trimming, or that contains a line break, is rejected. |
| `description` | markdown | Optional. The only free text field on the issue. Links to pull requests, CI runs, logs and documents are written as ordinary Markdown links inside it. |
| `type` | enum | `bug`, `feature`, `task`, `chore`. Default `task`. |
| `status` | enum | `open`, `in_progress`, `closed`. Default `open`. Changed only by `claim`, `release`, `close` and `reopen`. |
| `close_reason` | string | Optional. Set by `close --reason`, and cleared by `reopen` and by any other transition out of `closed` (§4.3). Empty otherwise: a non-closed issue never carries one. |
| `priority` | integer | `0` (highest) to `3` (lowest). Default `2`. |
| `labels` | set of strings | Free-form, but restricted to lowercase ASCII letters, digits, hyphens, underscores, dots and slashes. A label outside that set is rejected rather than normalised. |
| `assignee` | string | Free-form, e.g. `mikael` or `claude-1`. Same character set as a label, and like a label rejected rather than normalised, so `claim --as Mikael` is a usage error. Empty means unassigned. |
| `repo` | repository name | Optional, and must name a registered repository (§2.2). |
| `created_at` | timestamp | Set automatically (UTC, RFC 3339 with millisecond precision, e.g. `2026-08-26T09:12:03.412Z`). |
| `updated_at` | timestamp | Set automatically whenever a stored field of the issue, including its labels, actually changes. A write that changes nothing leaves it alone, and adding or removing a relation does not change either endpoint. Strictly increasing per issue: if the clock yields a value that is not greater than the stored one — the system clock may have a coarser resolution than a millisecond — the stored value plus one millisecond is written instead. |

Only timestamp ordering is guaranteed. Rapid writes on a coarse clock can push `updated_at`
slightly ahead of wall time, so timestamps are reliable as versions and ordering keys, but as time
measurements only to the second.

`blocked` is **not** a stored status. It is derived: an issue is blocked when at least one issue it
is `blocked-by` is not closed. This makes it impossible for the recorded state to disagree with the
dependency graph.

The fixed vocabulary above is not configurable. Everything a team wants to express beyond it goes
into labels.

### 2.4 Relation

A directed link between two issues. Both issues may belong to different projects.

Every relation type is named from the point of view of its subject, and reads
"*subject* — *type* — *other*". That one convention holds everywhere a relation is named: in this
table, in `awb create`, in `awb dep add` and in the API.

| Type | Meaning |
| --- | --- |
| `blocked-by` | `A blocked-by B`: A cannot start until B is closed. Drives readiness. |
| `parent` | `A parent B`: B is the parent of A, which is part of decomposing B. |
| `discovered-from` | `A discovered-from B`: A was found while working on B. Provenance only. |
| `related` | `A related B`: loose, symmetric association. No behaviour attached. |

The `blocked-by` and `parent` graphs must each remain acyclic, and are checked separately. A
command that would create a cycle fails with exit code 4, as does a relation from an issue to
itself. Adding a relation that already exists succeeds and changes nothing.

An issue also may not be `blocked-by` any ancestor or descendant in the `parent` graph. This
inverts decomposition and can deadlock readiness even though each graph is acyclic. The rule covers
the full ancestor/descendant chain and violations exit 4 like cycles.

An issue has at most one parent; `dep add` on an issue that already has one fails with exit code 4
unless `--force` is given, which replaces it. Relations are deleted with either endpoint issue.

A relation is stored once but shown on both issues; `direction` identifies the viewed endpoint
(§4.6). A symmetric `related` pair is stored canonically with the smaller ID as subject. Adding it
from either end is therefore idempotent, removal works in either order, and both views use
`direction: out`.

### 2.5 External artifacts

There is no separate attachment or link entity. References to pull requests, CI runs, logs, design
documents and files on disk are Markdown links in the issue description. The database therefore
stores no file contents and no link records.

To make those links useful without a separate model, `awb` parses the description as Markdown:
`awb show` prints the description verbatim and lists the links it finds beneath it, and the web UI
renders the description and turns the links into anchors.

Extraction takes inline links, reference links and autolinks, and ignores images. Each distinct
destination appears once, in the order it first occurs in the description, with the link text of
that first occurrence. Two destinations are distinct when they differ byte for byte; no
normalisation, resolution or validation is applied, so a relative destination such as `./notes.md`
is extracted as written. The text is the link's rendered plain text, with inline markup removed and
whitespace collapsed — `[**CI** run](https://ci/1)` yields `CI run` — and an autolink's text is its
destination. The result is a derived, read-only property of the issue and appears as `links` in the
JSON representation (§4.6).

### 2.6 Readiness

An issue is **ready** when all of the following hold:

* `status` is `open`,
* it is not blocked (§2.3),
* it has no direct child that is not closed — a decomposed issue is worked through its children,
  not directly. Only direct children count, as with `--parent` (§4.3); a closed child hides its own
  open children from this test, which is a consequence of `close` never looking at another issue.

Readiness guides listings rather than enforcing workflow. Non-ready issues may still be claimed or
closed, and closing never inspects related issues. The sole exception is claiming a blocked issue,
which requires `--force` (§4.3).

Readiness says nothing about the assignee. `awb ready` nevertheless defaults to unassigned issues,
because "what should I pick up next" is the question it exists to answer; `awb ready --mine` asks
the companion question, "which of the issues I hold can I actually work on", and `awb ready
--assignee X` asks it about somebody else. Repository context and the other filters of §4.3 apply
to `awb ready` exactly as they do to `awb list`.

`awb ready` is the primary agent entry point.

## 3. Storage

A single SQLite database file holds projects, repositories, issues and relations.

* Default location: `$XDG_DATA_HOME/awb/awb.db`, falling back to `~/.local/share/awb/awb.db`.
* Overridable, in increasing precedence, by the config file, the `AWB_DB` environment variable and
  the `--db` global flag.
* The value is either a filesystem path (direct mode) or an `http(s)://` URL (remote mode, §6).

There is no per-repository or per-directory database and no upward directory search: one user has
one database unless they explicitly point at another.

The database file is created by `awb init` and by nothing else. Any other command that finds it
missing fails with exit code 1 and a message naming the path, so that a typo in `--db` or `AWB_DB`
cannot silently produce a second, empty tracker.

Schema migrations are embedded in the binary, numbered, recorded in the database, and applied
automatically when an existing database is opened. They run inside a transaction that takes the write lock
first, so that several processes opening a stale database at once cannot race. A binary refuses to
open a database whose recorded schema version is newer than the highest migration it carries,
failing with exit code 1 rather than operating on a schema it does not understand.

The database is opened with WAL journalling, foreign keys enabled and a busy timeout, so several
local processes — for example three agents in three terminals — can use the same file safely.

Full text search over titles and descriptions uses SQLite FTS5, kept in sync by triggers.

## 4. Command line interface

### 4.1 Conventions

* Every command is non-interactive and safe to script. Destructive commands require a confirmation
  flag rather than a prompt.
* Global flags: `--db`, `--json`, `--compact`, `--all-repos`, `--repo`, `--project`, `--color`.
  Where a command defines a flag of the same name — `--repo` on `create` and `update`, `--project`
  on `create` — the command's meaning wins.
* Output modes, of which `--json` and `--compact` are mutually exclusive; giving both is a usage
  error:
  * default — aligned, coloured table for humans;
  * `--compact` — one line per issue, no padding, minimal punctuation, designed to consume as
    little agent context as possible:
    `awb-5c1d84 P1 open bug "Tokeniser drops the trailing newline" @claude-1 #tokeniser repo:awb !blocked`
    — the same issue the JSON of §4.6 shows.
  * `--json` — stable JSON, one object or one array per invocation, suitable for `jq` (§4.6).
* The `--compact` line begins with five mandatory positional fields — id, `P<priority>`, status,
  type and the title. The title is encoded as a JSON string, including the surrounding double
  quotes and JSON escaping; it is the only field that may contain literal spaces after decoding.
  Any further fields are optional and identified by
  their prefix rather than their position, and appear in this fixed order when present:
  `@<assignee>`, one `#<label>` per label in sorted order, `repo:<name>`, `!blocked` and, for
  `awb blocked`, one `blocked-by:<id>` per blocker in sorted order. The character restrictions on
  labels and assignees (§2.3) keep those tokens free of spaces, so a line is parseable by splitting
  on whitespace outside the quoted title.
* `--compact` for the commands that do not list issues: `project ls` prints
  `<key> <active_issues> <name>` per line, where `<name>` is a JSON string. `repo ls` prints
  `<name> [remote:<url>]... [path:<dir>]... [project:<key>]` per line; each URL and path is a JSON
  string immediately after its prefix, so a path with spaces is written as
  `path:"/home/me/my project"`. `dep tree` prints the
  ordinary compact issue line per node, prefixed by two spaces per level of depth — the root is at
  depth zero and therefore unindented. That prefix is the one thing that may precede the id, so a
  consumer strips the leading spaces, counts them to recover the depth, and then parses the rest of
  the line exactly as above.
* Mutating commands print nothing on success in the default and `--compact` modes, except `create`,
  which prints the new ID, and the deleting commands, which print a one-line summary of what was
  removed. Under `--json` every mutating command prints the resulting object; a deleting command
  prints the object as it was immediately before deletion, which for an issue includes the
  `relations` that went with it.
* Exit codes: `0` success · `1` runtime error · `2` usage error · `3` not found ·
  `4` constraint violation (e.g. dependency cycle).
* Invalid argument vocabulary (title, label, priority, enum, sort value, project key, or reserved
  repository name `none`) exits `2`. Constraints that depend on database state (cycles,
  duplicates, claim or parent conflicts, and deletion without required `--cascade`) exit `4`.
* An empty string clears an optional string value: `--assignee ""` unassigns,
  `--description ""` blanks the description, `--project ""` on `repo add` or `repo update`
  clears its optional `default_project`, and `--remote ""` or `--path ""` empties that whole set.
  An empty occurrence of `--remote` or `--path` may not be combined with a non-empty occurrence of
  the same flag in one invocation; that is a usage error rather than an order-dependent result.
  Empty `--project` is invalid for issue creation or filtering. Two exceptions: project
  `--name ""` restores the key as its name, and issue `--repo ""` means `--repo none` (§5).
* On destructive commands, `--force` confirms deletion and `--cascade` permits deleting dependent
  issues or clearing repository tags. On `claim`, `release` and `dep add --parent`, `--force`
  overrides a refusal.
* No match exits `3`; an ambiguous ID or repository match exits `2`; creating a duplicate exits `4`.
* An empty list is success (`0`) and renders as an empty table, no compact output, or `[]`.
* Errors always go to stderr, as a single line in the default and `--compact` modes and as
  `{"error": "..."}` under `--json`. The exit code is the machine-readable classification; the
  message is human-readable text.
* Colour is used in the default output mode when stdout is a terminal. `--color`, `color` in the
  configuration file and `AWB_COLOR` accept `auto`, `always` and `never`; `--no-color` is an alias
  for `--color never`, and a `NO_COLOR` environment variable of any value is equivalent to `never`.
* Repeated values of one filter are ORed; different filters are ANDed. No filter accepts
  comma-separated lists. Only `--status`, `--type`, `--priority`, `--label`, `--assignee` and
  `--project` repeat; other filters and context flags may occur once. Elsewhere `...` marks a
  repeatable flag.

### 4.2 Setup

| Command | Description |
| --- | --- |
| `awb init` | Create the database if absent and bring its schema up to date. The only command that creates it (§3). Takes no arguments and is idempotent. Fails with exit code 2 if the configured database location is an `http(s)` URL. |
| `awb project add <key> [--name] [--description] [--description-file FILE]` | Create a project. `--name` defaults to the key. |
| `awb project update <key> [--name] [--description] [--description-file FILE]` | Change a project's name or description. The key itself is immutable. |
| `awb project ls` | List projects with counts of issues that are not closed. |
| `awb project rm <key> --force [--cascade]` | Delete a project. Refuses while it holds any issue at all — closed ones included, so the refusal is wider than the count `project ls` shows and `--force` alone can never destroy closed history — unless `--cascade` is also given, which deletes those issues and their relations — including relations to issues in other projects, which may unblock work elsewhere. Any repository whose `default_project` names it has that value cleared. |
| `awb repo add <name> [--remote URL]... [--path DIR]... [--project KEY]` | Register a repository. Given neither `--remote` nor `--path`, infers remotes and path from the surrounding working tree, storing the resolved top-level path of that tree; outside a working tree that is a usage error. `--project` must name an existing project. |
| `awb repo update <name> [--remote URL]... [--path DIR]... [--project KEY]` | Change a repository. Each flag that is given replaces that whole set or value; the name itself is immutable. |
| `awb repo ls` | List repositories with their remotes, paths and default project. |
| `awb repo rm <name> --force [--cascade]` | Unregister. Refuses while any issue is tagged with it, closed ones included, unless `--cascade`, which clears the tag on those issues, leaving them untagged rather than pointing at a name that no longer resolves. |
| `awb agent-guide [--write FILE]` | Print a compact usage block for agents to stdout. `--write` instead writes it into `FILE` (typically `AGENTS.md` or `CLAUDE.md`), creating the file if absent, delimited by marker comments so that a second run replaces the existing block rather than appending a duplicate. |

### 4.3 Issues

| Command | Description |
| --- | --- |
| `awb create <title> [--type] [--priority] [--label]... [--assignee] [--project] [--repo] [--description] [--description-file FILE] [--parent ID] [--blocked-by ID]... [--discovered-from ID]... [--related ID]...` | Create an issue, with its labels and relations, in one transaction. Prints the new ID. |
| `awb show <id>` | Full issue, including relations, derived blocked state and the Markdown links found in the description. |
| `awb list [filters]` | List issues. |
| `awb ready [filters]` | List ready issues (§2.6), highest priority first. |
| `awb blocked [filters]` | List issues that are not closed and are blocked, each with its blockers. |
| `awb search <terms> [filters]` | Full text search over title and description. |
| `awb update <id> [--title] [--description] [--description-file FILE] [--type] [--priority] [--assignee] [--repo]` | Change fields. It cannot change `status`: the four commands below are the only status transitions, which keeps `in_progress` and an assignee from drifting apart. |
| `awb label add\|rm <id> <label>...` | Manage labels. Adding a label the issue already carries, or removing one it does not, succeeds and changes nothing. |
| `awb claim <id> [--as NAME] [--force]` | Atomically set assignee and `status=in_progress`. Claiming an issue you already hold succeeds; if it is already `in_progress` nothing changes. Fails with exit code 4 if it is assigned to someone else, blocked, or closed; `--force` overrides all three, and a forced claim on a closed issue clears `close_reason` along with the status, since a non-closed issue never carries one (§2.3). |
| `awb release <id> [--force]` | Clear the assignee and set status back to `open`. Releasing an issue that is already open and unassigned succeeds and changes nothing. Fails with exit code 4 on a closed issue, or on one assigned to someone else, unless `--force`; a forced release of a closed issue clears `close_reason` too, for the same reason as `claim`. |
| `awb close <id> [--reason TEXT]` | Set `status=closed` and, if `--reason` is given, `close_reason`. Closing a closed issue succeeds; omitting `--reason` leaves the recorded reason alone and `--reason ""` clears it. The assignee is left alone, since it records who did the work. |
| `awb reopen <id>` | Set `status=open`, clear `close_reason` and clear the assignee, so that the issue returns to the pool `awb ready` draws from. It acts only on a closed issue: on an issue that is not closed it succeeds and changes nothing, whatever its assignee, so it can never take a claim away from somebody who is working. Clearing the assignee of a closed issue is the point of the command and needs no `--force`, the assignee there being a record of who did the work rather than a claim on it. |
| `awb delete <id> --force` | Hard delete the issue and its relations. Not recoverable. Reports how many relations went with it, since removing a blocker silently makes other issues ready and orphaning children makes a decomposed parent's work top-level. |

`--description` and `--description-file` are mutually exclusive; `--description-file -` reads the
description from stdin. Both replace the description outright. The same holds for the description
of a project (§4.2). This is the only use of stdin, and is not the bulk import excluded by §1.2.

Filters accepted by `list`, `ready`, `blocked` and `search`:
`--status`, `--include-closed`, `--type`, `--priority`, `--priority-max`, `--label`, `--assignee`,
`--mine`, `--unassigned`, `--project`, `--repo`, `--all-repos`, `--include-untagged`, `--parent`,
`--limit`, `--sort`.

* `--status` takes the enum values of §2.3 and may be repeated. There is no magic `all` value, so
  the flag's vocabulary is exactly the enum the OpenAPI document declares (§6.1). By default
  closed issues are hidden; `--include-closed` widens whatever status set is in force to include
  them, and giving `--status closed` explicitly selects only closed issues.
* `--priority` selects one priority exactly and may be repeated. `--priority-max` is inclusive and
  reads as urgency, not as a number: because `0` is the highest priority, `--priority-max 1`
  means P0 and P1. There is deliberately no `--priority-min`; nobody asks for the unimportant half.
* `--parent <id>` selects the direct children of that issue, not the whole subtree. `awb dep tree`
  is how you see a subtree.
* `--limit <n>` caps results and must be non-negative; zero returns none. There is no default limit
  or CLI offset, so every match remains reachable; use `--compact` to reduce output size.
* `--mine` resolves to the configured identity (§7), which is also the default `--as` for `claim`.
  It is shorthand for `--assignee <identity>`. `--mine`, `--assignee` and `--unassigned` are
  mutually exclusive: giving more than one of them is a usage error. Giving any of them to
  `awb ready` replaces that command's default of `--unassigned` (§2.6).
* `--sort` takes one of `priority`, `created`, `updated`, `id` or, for `search` only, `relevance`,
  optionally prefixed with `-` for descending order. Every sort ends with `id` ascending as a
  final tiebreak, so the order is total and two invocations against unchanged data agree.
  `priority` inserts `created_at` ascending before that tiebreak — oldest first within a priority —
  so `--sort priority` is exactly the default order below; the other keys use the tiebreak alone.
  `relevance` is the one key whose bare form is descending, because best match first is what it
  means; `-relevance` is accepted and is worst match first.

Defaults: results are sorted as `--sort priority`, that is by priority ascending, then `created_at`
ascending, then `id` ascending — except `search`, which defaults to `relevance`. `awb ready` and
`awb blocked` fix the status set themselves (§2.6) and reject `--status` and `--include-closed`;
`awb ready` additionally defaults to `--unassigned`.
Nothing is ever archived or purged — closed issues remain queryable forever.

`awb search` treats its arguments as literal terms rather than as FTS5 syntax: each argument is
quoted before it reaches the query, and an issue matches when the title and description together
contain all of them. No operator, wildcard or column prefix is passed through, so no user or agent
input can produce a query syntax error.

Matching is by whole token, using the FTS5 `unicode61` tokenizer with its default settings:
case- and diacritic-insensitive, splitting on non-alphanumeric characters, no stemming and no
prefix matching. `awb search parser` finds "Parser" and "parser," but neither `pars` nor `parsers`
finds "parser". Since no wildcard reaches the query, an agent that wants a wider net widens its
terms rather than its syntax. `awb search` with no terms is a usage error.

### 4.4 Relations

| Command | Description |
| --- | --- |
| `awb dep add <id> --blocked-by <id>` | Record that the first issue cannot start until the second is closed. |
| `awb dep add <id> --parent <id> [--force]` | Set the parent of the first issue to the second. |
| `awb dep add <id> --related <id>` | Record a loose association. |
| `awb dep add <id> --discovered-from <id>` | Record provenance. |
| `awb dep rm <id> <type> <id>` | Remove a relation, where `<type>` is `blocked-by`, `parent`, `related` or `discovered-from`. |
| `awb dep tree <id>` | Print the subtree of children rooted at that issue, with status and blocked markers. |

Every one of these reads "*first id* — *relation* — *second id*", the single convention of §2.4;
the relation flags of `awb create` read the same way about the issue being created. `dep rm` takes
its two ids in that same order, so removing a relation is the `add` command with `rm` substituted.

`dep add` takes exactly one relation flag per invocation; giving two is a usage error. `dep rm` of
a relation that does not exist succeeds and changes nothing, like `label rm`.

`dep tree` descends from the named issue to its full depth, following children across project
boundaries, and does not show ancestors. It shows the whole subtree, closed children included and
marked as such, and accepts none of the filters of §4.3 — a tree with holes in it would misrepresent
the decomposition. Repository context (§5) does not apply to it either. Siblings at every level are
ordered as the default of §4.3 — priority ascending, then `created_at` ascending, then `id`
ascending — and `--sort` is not accepted, so the tree is reproducible like every other output.

### 4.5 Serving

| Command | Description |
| --- | --- |
| `awb serve [--addr 127.0.0.1:7777] [--cors-origin ORIGIN]...` | Serve the HTTP API and the bundled read-only web UI over the local database. Fails with exit code 2 if the configured database location is an `http(s)` URL, like `init`. `--cors-origin` allows that exact origin to call the API from a browser; it is repeatable, empty by default and does not accept `*`, so a separately hosted web UI is opt-in rather than any page in the browser being able to read the database. |

### 4.6 JSON representation

`--json` and the HTTP API share one set of shapes; §6.1 makes the OpenAPI document their formal
declaration, but the shapes themselves are fixed here so that neither surface can invent a second
one.

```json
{
  "id": "awb-5c1d84",
  "project": "awb",
  "title": "Tokeniser drops the trailing newline",
  "description": "Reproduced on Linux and macOS. See [CI run](https://ci/1).",
  "type": "bug",
  "status": "open",
  "priority": 1,
  "labels": ["tokeniser"],
  "assignee": "claude-1",
  "repo": "awb",
  "close_reason": "",
  "created_at": "2026-08-26T09:12:03.412Z",
  "updated_at": "2026-08-26T09:12:03.412Z",
  "blocked": true,
  "blockers": ["awb-9b2f60"],
  "relations": [
    {"type": "blocked-by", "other": "awb-9b2f60", "direction": "out"},
    {"type": "discovered-from", "other": "awb-9b2f60", "direction": "in"}
  ],
  "links": [{"text": "CI run", "url": "https://ci/1"}]
}
```

* There is one `Issue` shape, always complete. A list-like command returns an array of exactly
  these objects; nothing is trimmed for size, because `--compact` is the answer to output size.
* Every field is always present. An unset string is `""`, never `null` or absent, so consumers
  need no absence handling.
* `blocked`, `blockers`, `relations` and `links` are derived and read-only: they cannot be written
  through `update` or `PATCH`. `blockers` lists only the not-closed issues that cause `blocked`,
  while `relations` lists every relation the issue takes part in, at either end.
* A `Relation` is `{"type", "other", "direction"}`. A directed relation always keeps the reading of
  §2.4, and `direction` says which end this issue is: `out` when this issue is the subject — the one named
  first — and `in` when `other` is. So `A blocked-by B` appears on A as
  `{"blocked-by", "B", "out"}` and on B as `{"blocked-by", "A", "in"}`, and both still read
  "A blocked-by B". A `related` relation is always `out`, since it is symmetric.
* `labels` and `blockers` are sorted; `relations` is sorted by `type`, then `other`, then
  `direction`; `links` keeps the order of §2.5. Two invocations against unchanged data therefore produce byte-identical
  output. `remotes` and `paths` on a `Repo` keep the order they were given in. A list of projects
  is ordered by `key` ascending and a list of repositories by `name` ascending, in every output
  mode and on the corresponding endpoints, which is also what makes those endpoints pageable
  (§6.2).
* `Project` is `{"key", "name", "description", "active_issues"}`, where `active_issues` counts the
  issues that are not closed, and `Repo` is
  `{"name", "remotes", "paths", "default_project"}`. An error is `{"error": "..."}` on stderr; the
  exit code, or the HTTP status, carries the classification.
* `awb dep tree --json` returns one `Issue` object extended with a `children` array of the same
  shape, recursively.

## 5. Git repository context

`awb` is usable with no Git repository at all. When it is run inside a working tree it uses that
as context to reduce noise.

Resolution, performed once per invocation:

1. Walk up from the working directory looking for a `.git` directory or file. If none is found,
   there is no repository context.
2. Collect the working tree's top-level path and the URLs of all its remotes.
3. Match against registered repositories: a repository matches if any of its `paths` is a prefix of
   the working tree path at a path-component boundary (`/home/me/proj` matches `/home/me/proj` and
   `/home/me/proj/sub`, but not `/home/me/project-two`), or if any of its `remotes` matches a remote
   of the working tree after normalisation (scp-style URLs rewritten to a canonical form, `.git`
   suffix stripped, host lower-cased, credentials and ports ignored). Remotes and paths are stored
   exactly as they were given; normalisation is applied only when comparing.
4. If several repositories match, a repository matched by path beats one matched only by remote,
   and among path matches the longest matching path wins. A tie that neither rule settles — two
   repositories registered with the same path, or two matched only by remote — cannot be resolved
   and the command fails with exit code 2, asking for `--repo`.

When a repository context is active, list-like commands (`list`, `ready`, `blocked`, `search`)
show only issues tagged with that repository. Untagged, project-level issues are hidden by default,
on the assumption that work inside a checkout is work on that checkout.

A repository may opt out of that assumption in its repository-level configuration file (§7.2):

```yaml
include_untagged: true    # also show project-level issues in this working tree
```

Overrides on the command line, mutually exclusive — giving more than one is a usage error:

* `--repo <name>` — use that repository as context regardless of the working directory.
* `--repo none` — show only untagged issues.
* `--all-repos` — disable repository filtering entirely, showing tagged and untagged issues from
  every repository.

`--include-untagged` is not one of these: it combines with the resolved context, widening it to
also show untagged issues, and is the command line equivalent of `include_untagged` in the
repository configuration file. Its negative form `--no-include-untagged` turns that widening off
again, so a committed `include_untagged: true` can be overridden on the command line like every
other setting (§7.3). With `--all-repos` both forms are a no-op, since nothing is filtered out;
with `--repo none` both are a usage error, since only untagged issues are shown in the first place.
That refusal applies to the flags alone. An `include_untagged: true` inherited from a checkout's
configuration file is ignored under both `--all-repos` and `--repo none` rather than failing the
command, because it is not a mistake the person running the command is in a position to correct.

`none` is therefore a reserved repository name that `repo add` refuses.

Repository context and `--project` are independent filters that are ANDed: inside a working tree
bound to repository `awb`, `awb list --project other` lists issues that are in project `other`
*and* tagged with `awb`, which may well be none.

On `create` and `update`, `--repo` is not a context override but the value of the issue's `repo`
field; `--repo none` there creates or leaves an untagged issue. Otherwise `awb create` tags the new
issue with the context repository. The project of a new issue is resolved as `--project`, else
`AWB_PROJECT`, else `project` in the repository configuration file, else the context repository's
`default_project`, else `project` in the user configuration file; if none of these yields a project
the command fails with exit code 2.

## 6. Server mode

`awb serve` is optional. It exists so that things other than the CLI can reach the database —
third-party user interfaces, dashboards and integrations today, a shared team instance later (§10).
It serves:

* a JSON HTTP API that mirrors the CLI one-to-one and is complete enough to drive a fully
  functional read/write web UI (§6.2),
* the OpenAPI document describing that API (§6.1), and
* the bundled web UI, which in version 1 is read-only (§6.3).

Setting `AWB_DB`/`--db` to the server's URL makes the CLI operate against it; every command
behaves identically to direct mode, and repository context is still resolved on the client. The
exceptions are `init` and `serve`, which are about a local database file and refuse a URL with exit
code 2 (§4.2, §4.5). The CLI inverts the mapping of §6.1 to keep its exit codes identical in both
modes — `400` becomes 2, `404` becomes 3, `409` becomes 4, and any other failure, including a
transport error or an unreachable server, becomes 1 — and prints the `Error` message it received.

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
POST   /api/issues/{id}/claim      /release   /close   /reopen
POST   /api/issues/{id}/labels
DELETE /api/issues/{id}/labels?label=...
POST   /api/issues/{id}/relations
DELETE /api/issues/{id}/relations/{type}/{other}
GET    /api/issues/{id}/tree
GET    /api/ready
GET    /api/blocked
GET    /api/search?q=...&q=...
GET    /api/projects        POST /api/projects        PATCH /api/projects/{key}    DELETE …
GET    /api/repos           POST /api/repos           PATCH /api/repos/{name}      DELETE …
GET    /api/labels
GET    /api/assignees
```

The status transitions are endpoints rather than `PATCH` bodies for the same reason `update` cannot
set `status` (§4.3), and because `claim` must be a compare-and-set: it may carry the assignee it
expects to replace, so two agents racing for the same issue cannot both win. A plain `PATCH` of the
assignee field would be a lost update. Labels are added and removed individually for the same
reason — a whole-set replace would silently discard a concurrent edit. The label being removed
travels as a query parameter rather than a path segment because a label may contain a slash (§2.3).

Every request may carry an `X-Awb-Identity` header holding the caller's resolved identity (§7).
Version 1 does not authenticate it and mostly ignores it — the client already sends explicit
`assignee` values — but it is the field a version 2 server would authenticate and attribute, and
having it on the wire from the first release is one of the preparations of §10.3.

Everything under `/api/` is the JSON API and `/openapi.json` and `/openapi.yaml` are the document;
every other path belongs to the web UI.

Request and response bodies use exactly the same JSON representation as `--json` output, so agents
can move between the two transports without relearning anything. Query parameters carry the same
names as the corresponding CLI filter flags, in the same kebab-case spelling (`include-closed`,
`priority-max`, `all-repos`); a boolean parameter is written `name=true` or `name=false` and a
repeatable filter is repeated rather than comma-separated, exactly as on the command line.
`GET /api/search` carries its terms the same way: `q` is repeated once per positional argument of
`awb search`, each value one literal term that may itself contain spaces, and a request with no `q`
is a `400`. The confirmation and override flags of the destructive commands are boolean query
parameters too:
`DELETE /api/projects/{key}?cascade=true` and `DELETE /api/repos/{name}?cascade=true`. There is no
`force` parameter anywhere: the HTTP method is the confirmation that `--force` supplies on the
command line, and the overrides that `--force` carries on `claim` and `release` are body fields
(§6.4). Repository context is resolved on the client, so the
server never inspects the caller's working directory: the client sends a resolved `repo` parameter.
The three shapes that context can take all have a spelling — `repo=<name>`, `repo=none` for
untagged issues only, and `all-repos=true` for no filtering at all — and the widening of §5 is
`include-untagged=true` alongside `repo=<name>`, which ORs untagged issues into that filter. The
server implements those parameters; what it does not do is decide which of them applies.
Resolving context needs the repository registry, so in remote mode the client first fetches
`GET /api/repos`. If `.awb.yaml` names a missing repository, the client infers its remotes and path
and registers it with `POST /api/repos` as required by §7.2. `--mine` likewise never reaches the
server: the client sends `assignee=<identity>`.

### 6.1 OpenAPI

The HTTP API is specified by an OpenAPI 3.1 document, so third-party user interfaces and
integrations can be built against it and clients can be generated from it. The document lives in
the repository, is embedded in the binary, and is served at `/openapi.json` and `/openapi.yaml`.

* Its component schemas are the CLI's `--json` structures: `Issue`, `Relation`, `Project`, `Repo`,
  `Error`, plus `Facet` for the two endpoints the CLI has no counterpart for (§6.2) and the request
  bodies of §6.4. There is no second, HTTP-only *response* representation of anything the CLI also
  returns — if the shapes ever diverge, the JSON output is what changed and both must be corrected
  together.
* Enumerations (`type`, `status`, relation types) and the priority range are declared in the schema,
  so a generated client validates the same vocabulary the CLI enforces.
* Errors use the exit-code taxonomy of §4.1 mapped onto status codes: `400` usage, `404` not found,
  `409` constraint violation, `500` runtime error, with an `Error` body. `412` is the one status
  with no exit code behind it: it answers a failed `If-Match` (§6.2), which only a client that sends
  one can provoke.
* The document is treated as a compatibility contract: within a major version, changes are additive
  only. `info.version` tracks the API, not the binary.

Authoring the document is deferred to implementation; this specification fixes only that it must
exist, be authoritative for the API, and reuse the CLI JSON schemas.

### 6.2 Sufficiency for a read/write web UI

Version 1 ships a read-only UI, but the API is specified as if a read/write one existed, so that a
later version — or somebody else, now — can build one without a new server surface. The API is
therefore required to satisfy the following. Nothing here is optional for version 1: the endpoints
exist and are supported even though the bundled UI only reads.

* **Complete write coverage.** Every state change the CLI can make, an API client can make: create,
  update and delete issues; the four status transitions; labels; relations; projects; repositories.
  The only CLI commands with no API equivalent are the ones that are about the local machine rather
  than about the data — `init`, `serve` and `agent-guide`.
* **Safe concurrent editing.** A form-based UI reads an issue, the user edits it for a while, and
  the UI writes it back; a plain `PATCH` would silently overwrite whatever changed meanwhile. So
  `GET /api/issues/{id}` returns a strong `ETag` whose quoted value is the issue's `updated_at`
  string, and `PATCH` honours
  `If-Match`, answering `412` when the issue has moved on. What makes that reliable is not the
  millisecond resolution of `updated_at` but its being strictly increasing per issue (§2.3): two
  successive versions of one issue can never carry the same timestamp, whatever the host clock's
  real resolution is, so an ETag identifies a version rather than an instant. That is also what
  lets a client cache a response and a version 2 mechanism order the versions of a row. `If-Match`
  is optional — a caller that omits it, as the CLI always does, gets last-write-wins — but a UI is
  expected to send it. Every successful endpoint response whose body is one `Issue`, including a
  mutation response, carries the ETag for the returned version, so a client can make another
  conditional edit without first repeating the GET. It guards the issue's own stored fields,
  which is what a form edits; a
  relation added meanwhile does not move `updated_at` (§2.3) and so does not invalidate the ETag,
  and neither does `blocked` flipping because somebody closed a blocker. This is the same concern
  that already makes `claim` a compare-and-set and labels individually mutable.
* **Enough for the chrome, not just the content.** Editing UIs need to populate filter menus and
  autocomplete: `GET /api/labels` and `GET /api/assignees` return the distinct values in use as
  `{"value", "count"}` objects, sorted by value. Both honour the same filters as
  `GET /api/issues`, and `count` counts the issues matching those filters — so with no filters it
  counts the issues that are not closed, that being the default everywhere. `GET /api/assignees`
  has no row for the empty assignee; unassigned is a filter (`unassigned=true`), not a value. Where
  these two endpoints are paged, `limit` and `offset` page the facet rows themselves and never the
  issues behind them, so `count` is the same whatever page it appears on.
* **Paging.** Every endpoint that returns an array — `/api/issues`, `/api/ready`, `/api/blocked`,
  `/api/search`, `/api/projects`, `/api/repos`, `/api/labels`, `/api/assignees` — accepts `limit`
  and `offset` and returns the unpaged total in an `X-Total-Count` header, so a UI can show
  "1–50 of 214" and page through without loading everything. `GET /api/issues/{id}/tree` is not
  one of them: a tree is returned whole (§4.4). `limit` and `offset` must be non-negative integers;
  `limit=0` returns no rows while preserving the unpaged total in `X-Total-Count`.
* **Markdown source, never HTML.** The API returns and accepts the description exactly as stored, so
  an editor round-trips it losslessly. Rendering — and sanitising (§9) — is the UI's job. The
  derived `links` array (§2.5) stays available for clients that want the links without a parser.
* **Cross-origin access, opt in.** A UI hosted anywhere other than the server itself is a browser
  origin the API must allow explicitly, via `--cors-origin` (§4.5). The default is to allow none,
  because the API is unauthenticated and any page in the user's browser could otherwise reach it.
  For an allowed origin the server answers preflight `OPTIONS` requests, permits the methods and
  request headers the API uses (`Content-Type`, `If-Match`, `X-Awb-Identity`) and exposes `ETag`
  and `X-Total-Count` in `Access-Control-Expose-Headers` — without which a cross-origin UI could
  use neither the optimistic concurrency nor the paging above.

`offset`, `X-Total-Count`, `limit` on the array endpoints the CLI has no `--limit` for
(`/api/projects` and `/api/repos`), the two facet endpoints and the `ETag`/`If-Match` handshake are
the only places where the API is wider than the CLI. Everything else stays one-to-one, and all of
them are declared in the OpenAPI document like the rest.

Two things a write UI needs that are deliberately *not* API concerns. Repository context is resolved
by the client (§5), so a browser UI, having no working directory, simply does not have one and
filters by `repo` explicitly. And errors stay a single human-readable `Error` message (§4.6) with a
status code; there is no field-level validation report, because the vocabulary a form must respect
is fixed and published in the OpenAPI document, so a UI can validate before it submits.

### 6.3 The bundled web UI

Version 1 bundles a read-only UI, served from the binary on the paths described above, for browsing
projects, issues, search results and dependency trees. It is a client of the same HTTP API and gets
no privileged access to the database, which is what keeps that API honest: making the UI writable
later is a change to the UI alone.

### 6.4 Request bodies

Response bodies are the shapes of §4.6. Request bodies are the ones below, and none of them adds a
domain operation that the CLI lacks. They carry the corresponding CLI arguments, except that label
changes deliberately carry one label per request so concurrent edits cannot replace one another.

* `POST /api/issues` takes an `IssueCreate`: the writable fields of an `Issue` — `project`,
  `title`, `description`, `type`, `priority`, `assignee`, `repo` — plus `labels` and an initial
  `relations` array of `{"type", "other"}` pairs, read with the new issue as the subject exactly as
  `awb create`'s relation flags are. Everything but `project` and `title` may be omitted and then
  takes the default of §2.3. The whole body is applied in one transaction, so the API creates an
  issue with its labels and relations in a single call, as the CLI does.
* `PATCH /api/issues/{id}` takes only what `awb update` can change: `title`, `description`, `type`,
  `priority`, `assignee`, `repo`. `labels`, `relations`, `status` and `close_reason` may appear but
  may not change: each is ignored when it equals what is stored — the two arrays compared as the
  sorted forms of §4.6 — and rejected with `400` when it differs, because labels and relations are
  mutated individually and the transitions are their own endpoints. The derived and immutable
  fields (`id`, `project`, `created_at`, `updated_at`, `blocked`, `blockers`, `links`) are ignored
  whatever they say. Together those rules let a UI send back the object it read with only the
  fields it edited changed, while a `PATCH` that genuinely tries to close an issue or rewrite its
  labels is refused rather than silently dropped. Any unrecognised field name is rejected with
  `400`.
* `POST /api/issues/{id}/claim` takes `{"assignee", "expect_assignee", "force"}`. `assignee` is
  required — the client resolves its own identity (§6). `expect_assignee` is the compare-and-set:
  when present, the claim proceeds only if the current assignee is exactly that value, `""` meaning
  unassigned, and otherwise answers `409`. `force` defaults to `false` and does what `--force` does.
* `POST /api/issues/{id}/release` takes `{"force"}`; `POST /api/issues/{id}/reopen` takes no body.
* `POST /api/issues/{id}/close` takes `{"reason"}`, with the semantics of `--reason` (§4.3): absent
  leaves any recorded reason alone, `""` clears it.
* `POST /api/issues/{id}/labels` takes `{"label"}`, one label per call.
* `POST /api/issues/{id}/relations` takes `{"type", "other", "force"}`, read with this issue as the
  subject, where `force` replaces an existing parent as `dep add --parent --force` does.
* `POST /api/projects` takes `{"key", "name", "description"}` and `PATCH /api/projects/{key}` the
  same without `key`; `POST /api/repos` takes `{"name", "remotes", "paths", "default_project"}` and
  `PATCH /api/repos/{name}` the same without `name`. A `PATCH` replaces each field it carries and
  leaves the others alone, like `repo update`. `active_issues` is derived and ignored. The working
  tree inference of `repo add` has no API equivalent, the server having no working directory (§6.2),
  so remotes and paths are always explicit here.

Every mutating endpoint answers with the resulting object, and every `DELETE` with the object as it
was immediately before deletion, which is the `--json` behaviour of §4.1.

## 7. Configuration

Both configuration files are YAML and both are optional; `awb` runs with neither. Each is a flat
mapping of scalar keys — there is no nesting, no list and no anchor in any documented setting — and
each is parsed by a real YAML parser (§9) rather than by line matching. Only the exact file names
below are looked for; the `.yml` spelling is not searched.

An unreadable, malformed or wrongly typed configuration file fails the command with exit code 1 and
a message naming the file: silently falling back to defaults would hide the reason a command wrote
to the wrong database or picked the wrong project. Unknown keys are the one thing that is ignored
(§7.2).

A recognised configuration key whose value violates that setting's vocabulary is likewise an
exit-code-1 configuration error naming the file. An invalid value supplied by an environment
variable or command-line flag is a usage error (exit code 2). A syntactically valid project or
repository name that is selected by any source but does not exist is not found (exit code 3).

### 7.1 User configuration

`$XDG_CONFIG_HOME/awb/config.yaml`, falling back to `~/.config/awb/config.yaml`:

```yaml
db: /home/mikael/.local/share/awb/awb.db   # path or http(s) URL
identity: mikael                           # default assignee, --mine, claim --as
project: awb                               # default project for create
color: auto
```

When `identity` is unset it defaults to the OS username, lower-cased and stripped of any character
outside the assignee set of §2.3, so that a name like `Mikael` or a Windows `DOMAIN\user` still
yields a usable identity. That is a value the user never typed, which is why it is folded rather
than refused; a value given on the command line or in a configuration file is still rejected
(§2.3). If nothing is left after stripping, the commands that need one — `--mine`
and `claim` without `--as` — fail with exit code 1 and ask for `identity` to be set.

### 7.2 Repository configuration

`.awb.yaml` in the root of a Git working tree. It is meant to be committed, so that a repository
carries its own `awb` settings for everyone who checks it out, and it is a general-purpose file
rather than a single-purpose filter switch:

```yaml
repo: awb                 # bind this working tree to a registered repository by name
project: awb              # default project for issues created here
include_untagged: true    # see §5
```

A `repo` key here identifies the repository directly and takes precedence over the URL and path
matching of §5. If no repository of that name is registered, the first `awb` command run in that
working tree registers it, taking the remotes and the resolved top-level path exactly as
`awb repo add` would infer them. That is what makes registration optional for anyone who clones the
repository, and it is why an issue's `repo` field can still be required to name a registered
repository (§2.3).

That registration happens whatever the command is, a read-only one included, and is the one place
where a listing command writes; it prints nothing and is not reported. It obeys the rules of
`repo add`, so a `.awb.yaml` naming the reserved word `none` fails the command with exit code 2
(§5), and a failure to register is a failure of the command rather than a silent fallback to no
repository context.

Because this file comes from a checkout and may not have been written by the person running the
command, it may **not** set `db`, `identity` or `color`: a repository can shape what you see, but
cannot redirect where your issues are stored or claim to be you. Those keys are ignored if present,
without an error and without a warning, exactly like unknown keys — which are also ignored, so
future versions can add settings without breaking older binaries.

### 7.3 Precedence

Command line flags, then environment variables (`AWB_DB`, `AWB_IDENTITY`, `AWB_PROJECT`,
`AWB_COLOR`), then the repository configuration file, then the user configuration file, then the
built-in defaults.

The default project for `awb create` follows the same order, with the context repository's
registered `default_project` inserted between the repository and user configuration files (§5).
`project` and `AWB_PROJECT` are that default and nothing else: they never act as an implicit
`--project` filter, so a list-like command without `--project` shows every project.

## 8. Identifiers

An issue ID is `<project-key>-<hash>`, e.g. `awb-a3f9c1`. Because a project key may itself contain
hyphens, an ID is split on its *last* hyphen.

The hash follows the [Beads hash ID](https://beads.gascity.com/core-concepts/hash-ids) scheme: it
is derived by hashing the content of the issue being created together with a random salt, rather
than drawn from a sequence or from raw randomness. Concretely:

1. Build a byte sequence by concatenating, in order: the title's UTF-8 byte length as an unsigned
   64-bit big-endian integer; the title's UTF-8 bytes; the 24 ASCII bytes of the `created_at` value
   in the exact millisecond form `YYYY-MM-DDTHH:MM:SS.sssZ`; and the 16 raw random salt bytes.
   Length-prefixing the only variable-length field makes the framing unambiguous without reserving
   a character that titles could otherwise use.
2. Take the SHA-256 digest of that byte sequence.
3. The hash is the first six characters of its lowercase hexadecimal encoding.

The title and timestamp make IDs independently mintable without a counter; the salt distinguishes
otherwise identical creations. IDs are not content-addressed and must not be reconstructed.

On a same-project collision, draw a new salt and retry inside the insert transaction. The six-hex
character space has about 16 million values per project, so collision handling is required.

Unlike Beads, the hash length is fixed at six and children do not get dotted IDs; parentage is a
mutable relation (§2.4).

An ID is immutable, so an issue cannot move between projects. Deleted IDs are not reserved and may
eventually be reused.

Commands accept an unambiguous hash prefix in place of a full ID, and accept a bare hash or hash
prefix when it is unique across the database. Any non-empty prefix is allowed, and the argument is
lower-cased before matching, so an ID typed in capitals resolves. Uniqueness of a bare hash is a
property of the data at that moment, not a guarantee — as projects accumulate it can stop holding — so a bare hash that
matches more than one issue fails with exit code 2 rather than picking one.

## 9. Implementation notes

* Go, one statically linked binary, no cgo, so `go install` and cross-compilation both work.
* A pure Go SQLite driver (`modernc.org/sqlite`) provides FTS5 without a C toolchain.
* A real YAML library (`goccy/go-yaml` or `gopkg.in/yaml.v3`) parses the configuration files (§7)
  into a struct. Configuration is never read by matching lines or by regular expression.
* A CommonMark parser (e.g. `goldmark`) extracts links in the shared read/domain layer so CLI and
  API return the same derived data. Descriptions remain verbatim; the web UI renders and sanitises
  them.
* `crypto/sha256` and `crypto/rand` generate issue IDs (§8); `math/rand` is not used for the salt.
* Layering: a storage package owning the schema and queries; a domain package owning readiness,
  relation validation and repository matching; thin CLI and HTTP adapters over it, so both surfaces
  exercise the same code paths. The web UI is a client of the HTTP adapter, not a third adapter.
* All timestamps are stored in UTC.
* Concurrency is handled by SQLite itself: short transactions, WAL mode and a busy timeout. There
  are no leases, locks or claim expiry — `claim` is a single atomic update, and a crashed agent
  leaves an assigned issue that a human or another agent releases explicitly.

## 10. Delivery plan

### 10.1 Version 1 — single user, single machine

Everything specified above, with no synchronisation and no shared deployment: one person, one
machine, one database file, any number of local processes and agents against it. `awb serve` is in
scope as a local surface — it is what third-party UIs and integrations are built against — but is
bound to loopback and unauthenticated, so it serves the machine's own user. The API it serves is
complete in the sense of §6.2; only the bundled UI is limited to reading.

### 10.2 Version 2 — multi-user and multi-machine

Deferred: identity and authentication for the server, a shared instance for a team or an open source
project, and some form of synchronisation or replication between databases. Nothing about it is
designed here, and version 1 must not carry half of it.

### 10.3 Version 2 foundations

Version 1 provides independently generated hash IDs (§8), atomic domain operations, schema
migrations, UTC version timestamps, a stable OpenAPI contract and an API sufficient for a write UI.
It deliberately provides no change log, tombstones, vector clocks or other merge machinery.

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
