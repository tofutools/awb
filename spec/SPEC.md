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
* The issue database is not tied to a repository or a checkout. One database spans everything the
  user works on, and a directory narrows the view through a small local configuration file rather
  than through any knowledge of Git.
* There are no exotic modelling concepts (no formulas, molecules, wisps or gates).

### 1.1 Design goals

1. **Agent-first.** Every operation is a non-interactive command with stable, parseable output and
   meaningful exit codes. Output modes exist that minimise context consumption.
2. **Small surface.** A fixed vocabulary of types, statuses and priorities that an agent can be
   taught in a few lines of instructions.
3. **Single source of truth.** One database per user, shared by all their projects, reachable over
   HTTP by tools other than the CLI.
4. **No ceremony.** No server required, no configuration required, no version control required.

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
| `created_at` | Set automatically, in the form and with the guarantees §2.2 gives an issue's. |
| `updated_at` | Set automatically whenever a stored field of the project actually changes, with the form and the strictly-increasing-per-project guarantee §2.2 gives an issue's. A write that changes nothing leaves it alone, and creating, changing or deleting an issue the project holds does not touch it — `active_issues` is derived, not stored. |

### 2.2 Issue

| Field | Type | Description |
| --- | --- | --- |
| `id` | string | `<project-key>-<hash>`, e.g. `awb-a3f9c1`. Assigned at creation, immutable. |
| `project` | project key | Required. Immutable after creation. |
| `title` | string | Required, single line. Leading and trailing whitespace is trimmed, and a title that is empty after trimming, or that contains a line break, is rejected. |
| `description` | markdown | Optional. The only free text field on the issue. Links to pull requests, CI runs, logs and documents are written as ordinary Markdown links inside it. |
| `type` | enum | `bug`, `feature`, `task`, `chore`. Default `task`. |
| `status` | enum | `open`, `in_progress`, `closed`. Default `open`. Changed only by `create --assignee`, `claim`, `release`, `close` and `reopen`. |
| `close_reason` | string | Optional single line, trimmed and rejecting a line break as `title` does, but unlike `title` it may be empty: a value that is empty after trimming clears it (§4.1). Set by `close --reason`, and cleared by `reopen` and by any other transition out of `closed` (§4.3). Empty otherwise: a non-closed issue never carries one. |
| `priority` | integer | `0` (highest) to `3` (lowest). Default `2`. |
| `labels` | set of strings | Free-form, but restricted to lowercase ASCII letters, digits, hyphens, underscores, dots and slashes. A label outside that set is rejected rather than normalised. |
| `assignee` | string | Free-form, e.g. `mikael` or `claude-1`. Same character set as a label, and like a label rejected rather than normalised, so `claim --as Mikael` is a usage error. Empty means unassigned. Changed only by `create --assignee`, `claim`, `release` and `reopen`, so it never drifts from `status` (§4.3). |
| `created_at` | timestamp | Set automatically (UTC, RFC 3339 with millisecond precision, e.g. `2026-08-26T09:12:03.412Z`). |
| `updated_at` | timestamp | Set automatically whenever a stored field of the issue, including its labels, actually changes. A write that changes nothing leaves it alone, and adding or removing a relation does not change either endpoint. Strictly increasing per issue: if the clock yields a value that is not greater than the stored one — the system clock may have a coarser resolution than a millisecond — the stored value plus one millisecond is written instead. |

Only timestamp ordering is guaranteed. Rapid writes on a coarse clock can push `updated_at`
slightly ahead of wall time, so timestamps are reliable as versions and ordering keys, but as time
measurements only to the second.

`blocked` is **not** a stored status. It is derived: an issue is blocked when it is itself not
closed and at least one issue it is `blocked-by` is not closed. A closed issue is therefore never
blocked and its `blockers` are empty, whatever its `blocked-by` relations still say. This makes it impossible for the recorded state to disagree with the
dependency graph.

The fixed vocabulary above is not configurable. Everything a team wants to express beyond it goes
into labels.

### 2.3 Relation

A directed link between two issues. Both issues may belong to different projects.

Every relation type is named from the point of view of its subject, and reads
"*subject* — *type* — *other*". That one convention holds everywhere a relation is named: in this
table, in `awb create`, in `awb dep add` and in the API.

| Type | Meaning |
| --- | --- |
| `blocked-by` | `A blocked-by B`: A cannot start until B is closed. Drives readiness. |
| `parent` | `A parent B`: B is the parent of A, which is part of decomposing B. Decomposition only; it does not drive readiness. |
| `discovered-from` | `A discovered-from B`: A was found while working on B. Provenance only. |
| `related` | `A related B`: loose, symmetric association. No behaviour attached. |

The `blocked-by` and `parent` graphs must each remain acyclic, and are checked separately. A
command that would create a cycle fails with exit code 4, as does a relation from an issue to
itself. Adding a relation that already exists succeeds and changes nothing.

An issue also may not be `blocked-by` any ancestor or descendant in the `parent` graph. This
inverts decomposition — a child waiting for its own parent, or a parent for its own child,
describes work that cannot sensibly be ordered — even though each graph is acyclic. The rule covers
the full ancestor/descendant chain and violations exit 4 like cycles. It is about the `blocked-by`
edge itself and not about what that edge transitively reaches, so it is checked by testing the
other endpoint for membership in the issue's ancestor and descendant sets rather than by a
reachability search across both graphs. Both this rule and the two
acyclicity rules are checked on every operation that adds or replaces an edge in either graph — a
`parent` edge can violate them just as a `blocked-by` edge can.

An issue has at most one parent; `dep add` on an issue that already has one fails with exit code 4
unless `--force` is given, which replaces it. Relations are deleted with either endpoint issue.

A relation is stored once but shown on both issues; `direction` identifies the viewed endpoint
(§4.6). A symmetric `related` pair is stored canonically with the smaller ID — comparing the whole ID
string byte for byte — as subject. Adding it
from either end is therefore idempotent, removal works in either order, and both views use
`direction: out`.

### 2.4 External artifacts

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

### 2.5 Readiness

An issue is **ready** when all of the following hold:

* `status` is `open`, and
* it is not blocked (§2.2).

Only `blocked-by` drives readiness. A decomposed issue is ready alongside its children, because a
parent with open children is often exactly what somebody should pick up — and because making
decomposition block would mean `close` had to inspect other issues, or that a closed child could
hide its own open children. A team that wants a parent held back records that as `blocked-by`.

Readiness guides listings rather than enforcing workflow. A non-ready issue may still be closed,
and closing never inspects related issues. The exception is `claim`, which refuses a blocked issue
and a closed one — as it refuses one held by somebody else — unless `--force` is given (§4.3).

Readiness says nothing about the assignee, but `awb ready` lists only unassigned issues, because
"what should nobody-in-particular pick up next" is the question it exists to answer. It therefore
takes no assignee filter: `--mine`, `--assignee` and `--unassigned` are usage errors on it. "Which
issues do I hold" is `awb list --mine`, and since claiming sets `in_progress` (§4.3) an issue you
hold is never ready anyway. Directory context and the other filters of §4.3 apply to `awb ready`
exactly as they do to `awb list`.

`awb ready` is the primary agent entry point.

## 3. Storage

A single SQLite database file holds projects, issues and relations.

* Default location: `$XDG_DATA_HOME/awb/awb.db`, falling back to `~/.local/share/awb/awb.db`.
* Overridable, in increasing precedence, by the config file, the `AWB_DB` environment variable and
  the `--db` global flag.
* The value is either a filesystem path (direct mode) or an `http(s)://` URL (remote mode, §6).

There is no per-directory database. The upward directory search of §5 looks for a configuration
file and never for a database, so one user has one database unless they explicitly point at
another.

The database file is created by `awb init` and by nothing else. Any other command that finds it
missing fails with exit code 1 and a message naming the path, so that a typo in `--db` or `AWB_DB`
cannot silently produce a second, empty tracker. A zero-length file counts as missing for this
purpose, so what `touch` or an interrupted `init` leaves behind is not something another command
quietly fills in; only `init` treats it as a file to create the schema in. The stamp that
identifies the file — SQLite's `application_id` set to a fixed `awb` value — is written by the
first migration rather than as a separate step of `init`, so whatever creates the schema also
carries it. Every command, `init` included, refuses a file that exists, is not empty and does not
carry the stamp — again exit code 1 — so the same typo cannot point at somebody else's database and
have `awb`'s migrations applied to it.

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
* Global flags: `--db`, `--json`, `--compact`, `--no-context`, `--color`, `--no-color`, `--help`
  and `--version`; the last prints the binary's version and exits. `--project` is deliberately not
  among them: it is a repeatable filter on the four list-like commands and the single-valued
  creation target on `create` (§4.3), and every other command rejects it with exit code 2. The
  commands that take an issue ID address one issue by name and never filter (§5), so there is
  nothing for it to mean on them.
* Output modes, of which `--json` and `--compact` are mutually exclusive; giving both is a usage
  error:
  * default — aligned, coloured table for humans. Its columns, widths and truncation are
    deliberately unspecified and are not a compatibility surface: nothing should parse it, and it
    may change between versions. That holds for the default output of every command, `show`,
    `project ls` and `dep tree` included. `--compact` and `--json` are what a script or an agent
    reads;
  * `--compact` — one line per issue, no padding, minimal punctuation, designed to consume as
    little agent context as possible:
    `awb-5c1d84 P1 in_progress bug "Tokeniser drops the trailing newline" @claude-1 #tokeniser !blocked`
    — the same issue the JSON of §4.6 shows.
  * `--json` — stable JSON, one object or one array per invocation, suitable for `jq` (§4.6).
* The `--compact` line begins with five mandatory positional fields — id, `P<priority>`, status,
  type and the title. The title is encoded as a JSON string, including the surrounding double
  quotes and JSON escaping; it is the only field that may contain literal spaces after decoding.
  Any further fields are optional and identified by
  their prefix rather than their position, and appear in this fixed order when present:
  `@<assignee>`, one `#<label>` per label in sorted order, `!blocked` and, for `awb blocked`, one
  `blocked-by:<id>` per entry of `blockers` (§4.6), in sorted order. The character restrictions on
  labels and assignees (§2.2) keep those tokens free of spaces, so a line is parseable by splitting
  on whitespace outside the quoted title.
* `awb show --compact` prints that same one line and nothing else, losing the description,
  relations and links: `--compact` means the cheapest representation there is, and `--json` is what
  an agent uses when it needs the rest. In the default mode `awb blocked` shows the ids of
  `blockers` as well, and `awb dep tree` prints the indented tree
  described below, aligned and coloured like a table rather than as a single column.
* `--compact` for the commands that do not list issues: `project ls` prints
  `<key> <active_issues> <name>` per line, where `<name>` is a JSON string. `dep tree` prints the
  ordinary compact issue line per node, prefixed by two spaces per level of depth — the root is at
  depth zero and therefore unindented. That prefix is the one thing that may precede the id, so a
  consumer strips the leading spaces, counts them to recover the depth, and then parses the rest of
  the line exactly as above.
* Mutating commands print nothing on success in the default and `--compact` modes, except `create`,
  which prints the new ID, and the deleting commands, which print a one-line human-readable summary
  of what was removed. That summary is the one output with no guaranteed format; a script uses
  `--json`, which returns the deleted object. Under `--json` every mutating command prints the resulting object; a deleting command
  prints the object as it was immediately before deletion, which for an issue includes the
  `relations` that went with it. Only `delete` and `project rm` delete an entity that way;
  `label rm` and `dep rm` are ordinary mutations that print nothing on success and, under `--json`,
  the resulting issue — the one named first, for `dep rm`. `init`, `serve` and `agent-guide`
  produce no object and ignore both output-mode flags.
* Exit codes: `0` success · `1` runtime error · `2` usage error · `3` not found ·
  `4` constraint violation (e.g. dependency cycle).
* Invalid argument vocabulary (title, label, priority, enum, sort value or project key) exits `2`.
  Constraints that depend on database state (cycles, duplicates, claim or parent conflicts, and
  deletion without required `--cascade`) exit `4`.
* An empty string clears an optional string value: `--description ""` blanks the description and
  `--reason ""` the close reason. An empty `--project`, `--label` or `--assignee` is invalid
  everywhere, being outside those vocabularies (§2.1, §2.2) — `--unassigned` is how a listing asks
  for the empty assignee. The one exception is project `--name ""`, which restores the key as its
  name, on `PATCH /api/projects/{key}` exactly as on the command line.
* On the destructive commands, `--force` confirms deletion; `--cascade`, which only `project rm`
  has, additionally permits deleting the issues a project holds. On `claim`, `release` and
  `dep add --parent`, `--force` overrides a refusal.
* Addressing a single entity that does not exist — an issue ID, or a project key from any source
  (§7) — exits `3`, even when it appears as a listing's `--project`. An ambiguous ID exits `2` and
  creating a duplicate exits `4`.
* An empty list is success (`0`) and renders as an empty table, no compact output, or `[]`.
* Errors always go to stderr, as a single line in the default and `--compact` modes and as
  `{"error": "..."}` under `--json`. The exit code is the machine-readable classification; the
  message is human-readable text.
* Colour is used in the default output mode when stdout is a terminal. `--color`, `color` in the
  configuration file and `AWB_COLOR` accept `auto`, `always` and `never`; `--no-color` is an alias
  for `--color never`, and a `NO_COLOR` environment variable of any value is equivalent to `never`.
* Repeated values of one filter are ORed; different filters are ANDed. No filter accepts
  comma-separated lists. Only `--status`, `--type`, `--priority`, `--label`, `--assignee` and
  `--project` repeat, and only as filters: the `--project` of `awb create` names one project and
  may occur once. Other filters and context flags may occur once too. Elsewhere `...` marks a
  repeatable flag.

### 4.2 Setup

| Command | Description |
| --- | --- |
| `awb init` | Create the database if absent, together with any missing parent directory, and bring its schema up to date. The only command that creates it (§3). Takes no arguments and is idempotent. Fails with exit code 2 if the configured database location is an `http(s)` URL. |
| `awb project add <key> [--name] [--description] [--description-file FILE]` | Create a project. `--name` defaults to the key. |
| `awb project update <key> [--name] [--description] [--description-file FILE]` | Change a project's name or description. The key itself is immutable. |
| `awb project ls` | List projects with counts of issues that are not closed. |
| `awb project rm <key> --force [--cascade]` | Delete a project. Refuses while it holds any issue at all — closed ones included, so the refusal is wider than the count `project ls` shows and `--force` alone can never destroy closed history — unless `--cascade` is also given, which deletes those issues and their relations — including relations to issues in other projects, which may unblock work elsewhere. |
| `awb agent-guide [--write FILE]` | Print a compact usage block for agents to stdout. `--write` instead writes it into `FILE` (typically `AGENTS.md` or `CLAUDE.md`), creating the file if absent, delimited by the exact marker lines `<!-- awb:begin -->` and `<!-- awb:end -->`, so that a second run, by any version of the binary, replaces the existing block rather than appending a duplicate. When the file exists and holds no such block, the block is appended at the end, preceded by a blank line. A file holding only one of the two markers, or holding them in the wrong order, fails with exit code 1 rather than gaining a second block. |

### 4.3 Issues

| Command | Description |
| --- | --- |
| `awb create <title> [--type] [--priority] [--label]... [--assignee] [--project] [--description] [--description-file FILE] [--parent ID] [--blocked-by ID]... [--discovered-from ID]... [--related ID]...` | Create an issue, with its labels and relations, in one transaction. Prints the new ID. Creating with an assignee is an atomic create-and-claim: `--assignee` also sets `status=in_progress`, so a new issue is never open and assigned at once (§2.2), and creation is the one place a claim needs no separate command. |
| `awb show <id>` | Full issue, including relations, derived blocked state and the Markdown links found in the description. |
| `awb list [filters]` | List issues. |
| `awb ready [filters]` | List ready issues (§2.5), highest priority first. |
| `awb blocked [filters]` | List issues that are not closed and are blocked, each with its blockers. |
| `awb search <terms> [filters]` | Full text search over title and description. |
| `awb update <id> [--title] [--description] [--description-file FILE] [--type] [--priority]` | Change fields. It cannot change `status` or `assignee`: the four commands below are the only transitions of either, which keeps `in_progress` and an assignee from drifting apart and keeps a claim from being taken silently. Giving no field flag at all succeeds and changes nothing, exactly as an empty `PATCH` does. |
| `awb label add\|rm <id> <label>` | Manage labels, one per invocation, so the command matches the endpoint of §6.4 one for one and neither surface has to define a partial failure. Adding a label the issue already carries, or removing one it does not, succeeds and changes nothing. |
| `awb claim <id> [--as NAME] [--force]` | Atomically set assignee and `status=in_progress`. Claiming an issue you already hold succeeds; if it is already `in_progress` nothing changes. Fails with exit code 4 if it is assigned to someone else, blocked, or closed; `--force` overrides all three, and a forced claim on a closed issue clears `close_reason` along with the status, since a non-closed issue never carries one (§2.2). |
| `awb release <id> [--force]` | Clear the assignee and set status back to `open`. Releasing an issue that is already open and unassigned succeeds and changes nothing. Fails with exit code 4 on a closed issue, or on one assigned to someone else, unless `--force`; a forced release of a closed issue clears `close_reason` too, for the same reason as `claim`. |
| `awb close <id> [--reason TEXT]` | Set `status=closed` and, if `--reason` is given, `close_reason`. Closing a closed issue succeeds; omitting `--reason` leaves the recorded reason alone and `--reason ""` clears it. The assignee is left alone, since it records who did the work. |
| `awb reopen <id>` | Set `status=open`, clear `close_reason` and clear the assignee, so that the issue returns to the pool `awb ready` draws from. It acts only on a closed issue: on an issue that is not closed it succeeds and changes nothing, whatever its assignee, so it can never take a claim away from somebody who is working. Clearing the assignee of a closed issue is the point of the command and needs no `--force`, the assignee there being a record of who did the work rather than a claim on it. |
| `awb delete <id> --force` | Hard delete the issue and its relations. Not recoverable. It never refuses on account of dependents and has no `--cascade`: it orphans any children and drops every relation. Reports how many relations went with it, since removing a blocker silently makes other issues ready and orphaning children makes a decomposed parent's work top-level. |

`--description` and `--description-file` are mutually exclusive; `--description-file -` reads the
description from stdin. Both replace the description outright. The same holds for the description
of a project (§4.2). This is the only use of stdin, and is not the bulk import excluded by §1.2.

Filters accepted by `list`, `ready`, `blocked` and `search`:
`--status` and `--include-closed` (on `list` and `search` only, see below), `--type`, `--priority`, `--priority-max`, `--label`, `--assignee`,
`--mine`, `--unassigned`, `--project`, `--parent`, `--limit`, `--sort`. The global `--no-context`
(§5) applies to all four as well.

* `--status` takes the enum values of §2.2 and may be repeated. There is no magic `all` value, so
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
  mutually exclusive: giving more than one of them is a usage error, as is giving any of them to
  `awb ready` (§2.5).
* `--sort` takes one of `priority`, `created`, `updated`, `id` or, for `search` only, `relevance`,
  optionally prefixed with `-` for descending order. Every sort ends with `id` ascending as a
  final tiebreak, so the order is total and two invocations against unchanged data agree.
  `priority` inserts `created_at` ascending before that tiebreak — oldest first within a priority —
  so `--sort priority` is exactly the default order below; the other keys use the tiebreak alone.
  `relevance` is the one key whose bare form is descending, because best match first is what it
  means; `-relevance` is accepted and is worst match first. The `-` prefix reverses the named key
  only: the `created_at` and `id` tiebreaks stay ascending whatever it says.

Defaults: results are sorted as `--sort priority`, that is by priority ascending, then `created_at`
ascending, then `id` ascending — except `search`, which defaults to `relevance`. `awb ready` and
`awb blocked` fix the status set themselves — `ready` to `open` (§2.5), `blocked` to the two
statuses that are not closed — and therefore reject `--status` and `--include-closed`;
`awb ready` additionally fixes the assignee filter to unassigned and rejects `--mine`,
`--assignee` and `--unassigned`. A flag that a command rejects this way is a usage error
(exit code 2, `400`), as is `--sort relevance` outside `search`.
Nothing is ever archived or purged — closed issues remain queryable forever.

`awb search` treats its arguments as literal terms rather than as FTS5 syntax: each argument is
wrapped in double quotes, with any double quote inside it doubled, before it reaches the query, and an issue matches when the title and description together
contain all of them. No operator, wildcard or column prefix is passed through, so no user or agent
input can produce a query syntax error.

Matching is by whole token, using the FTS5 `unicode61` tokenizer with its default settings:
case- and diacritic-insensitive, splitting on non-alphanumeric characters, no stemming and no
prefix matching. `awb search parser` finds "Parser" and "parser," but neither `pars` nor `parsers`
finds "parser". Since no wildcard reaches the query, an agent that wants a wider net widens its
terms rather than its syntax. `awb search` with no terms is a usage error, and so is a term the
tokenizer reduces to nothing, such as `-` or `,`: dropping it silently would widen the search
without saying so, and passing an empty term through would either error or match everything
depending on the driver. Both exit 2, and `GET /api/search` answers `400` for the same two cases.

### 4.4 Relations

| Command | Description |
| --- | --- |
| `awb dep add <id> --blocked-by <id>` | Record that the first issue cannot start until the second is closed. |
| `awb dep add <id> --parent <id> [--force]` | Set the parent of the first issue to the second. |
| `awb dep add <id> --related <id>` | Record a loose association. |
| `awb dep add <id> --discovered-from <id>` | Record provenance. |
| `awb dep rm <id> --blocked-by\|--parent\|--related\|--discovered-from <id>` | Remove a relation, taking the same one relation flag as `dep add`. |
| `awb dep tree <id>` | Print the subtree of children rooted at that issue, with status and blocked markers. |

Every one of these reads "*first id* — *relation* — *second id*", the single convention of §2.3;
the relation flags of `awb create` read the same way about the issue being created. `dep rm` takes
the same flag and the same two ids in the same order, so removing a relation is literally the `add`
command with `rm` substituted.

`dep add` and `dep rm` take exactly one relation flag per invocation; giving two, or none, is a
usage error. `dep rm` of a relation that does not exist succeeds and changes nothing, like
`label rm`.

`dep tree` descends from the named issue to its full depth, following children across project
boundaries, and does not show ancestors. It shows the whole subtree, closed children included and
marked as such, and accepts none of the filters of §4.3 — a tree with holes in it would misrepresent
the decomposition. Directory context (§5) does not apply to it either. Siblings at every level are
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
  "status": "in_progress",
  "priority": 1,
  "labels": ["tokeniser"],
  "assignee": "claude-1",
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
  §2.3, and `direction` says which end this issue is: `out` when this issue is the subject — the one named
  first — and `in` when `other` is. So `A blocked-by B` appears on A as
  `{"blocked-by", "B", "out"}` and on B as `{"blocked-by", "A", "in"}`, and both still read
  "A blocked-by B". A `related` relation is always `out`, since it is symmetric.
* `labels` and `blockers` are sorted; `relations` is sorted by `type`, then `other`, then
  `direction`; `links` keeps the order of §2.4. Two invocations against unchanged data therefore
  produce byte-identical output. A list of projects is ordered by `key` ascending, in every output
  mode and on the corresponding endpoint, which is also what makes that endpoint pageable (§6.2).
* `Project` is
  `{"key", "name", "description", "active_issues", "created_at", "updated_at"}`, where
  `active_issues` counts the issues that are not closed and the two timestamps are §2.1's.
  `active_issues` is derived and read-only, the timestamps are set automatically, and none of the
  three can be written. An error is `{"error": "..."}` on stderr; the exit code, or the HTTP
  status, carries the classification.
* `awb dep tree --json` and `GET /api/issues/{id}/tree` return an `IssueTree`: one `Issue`
  extended with a `children` array of `IssueTree`, recursively, ordered as §4.4 orders siblings. A
  leaf carries `"children": []`, and no other surface carries `children` at all — the `Issue` that
  `show` and the list-like commands return does not have the field.

## 5. Directory context

`awb` knows nothing about Git, or about any other version control system. What a directory means is
written down in that directory, in a local configuration file, and everything else follows from it.

Resolution, performed once per invocation:

1. Start at the working directory, with symlinks resolved, and walk up towards the filesystem root
   looking for a file named `.awb.yaml` (§7.2).
2. The first one found is the local configuration file. The search stops there: files further up
   are neither read nor merged. If none is found, there is no directory context.

Two keys in that file make a context, and either may be absent:

* `project` — the issues of this directory belong to that project. It is the default project for
  `awb create` and the default `--project` filter for the list-like commands.
* `label` — the work in this directory carries that label. It is added to issues created here and
  is the default `--label` filter for the list-like commands.

Together they cover the usual arrangements without a registry of anything: a directory that is a
whole project, a directory that is one slice of a larger project, or both at once — `project: web`
with `label: frontend` scopes a directory to the frontend work of the web project.

Putting the file at the top of a Git working tree gives that checkout its own scope, since the
upward search reaches it from every subdirectory. That is the expected arrangement, and it is why
the file is meant to be committed (§7.2). But nothing in the mechanism is tied to Git: a directory
that is under no version control at all, or one that holds several checkouts, or one checkout that
holds several such files in different subdirectories, all work the same way.

How the context meets the filters of §4.3:

* The context project is a default for `--project`, and an explicit `--project` replaces it. An
  issue belongs to exactly one project, so intersecting the two could only ever yield nothing;
  the explicit flag is what the person running the command means.
* The context label is a default for `--label` and is replaced by an explicit `--label` in the
  same way. On `awb create` it is not a default but a value: the new issue carries the context
  label *in addition to* any `--label` given, so that an issue created here stays visible here
  whatever else it is labelled.
* `--no-context` ignores the `project` and `label` of the local configuration file for this
  invocation, restoring the view of the whole database. It is a global flag, it may be combined
  with `--project` and `--label`, which then stand alone, and it does not stop the file from being
  read — a malformed one still fails the command (§7).

Context applies to `list`, `ready`, `blocked`, `search` and `create`, and to nothing else. The
commands that take an issue ID address one issue by name and never filter, so `show`, `update`,
`dep tree` and the rest reach an issue whatever directory they are run from.

The project of a new issue is resolved as `--project`, else `AWB_PROJECT`, else `project` in the
local configuration file, else `project` in the user configuration file; if none of these yields a
project the command fails with exit code 2. `--no-context` removes the third of those, not the
others.

## 6. Server mode

`awb serve` is optional. It exists so that things other than the CLI can reach the database —
third-party user interfaces, dashboards and integrations today, a shared team instance later (§10).
It serves:

* a JSON HTTP API that mirrors the CLI one-to-one and is complete enough to drive a fully
  functional read/write web UI (§6.2),
* the OpenAPI document describing that API (§6.1), and
* the bundled web UI, which in version 1 is read-only (§6.3).

Setting `AWB_DB`/`--db` to the server's URL makes the CLI operate against it; every command
behaves identically to direct mode, and directory context is still resolved on the client. The
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
GET    /api/issues?status=open&label=parser&project=awb
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
GET    /api/projects        POST /api/projects
GET    /api/projects/{key}  PATCH /api/projects/{key}  DELETE /api/projects/{key}
GET    /api/labels
GET    /api/assignees
```

The status transitions are endpoints rather than `PATCH` bodies for the same reason `update` cannot
set `status` (§4.3), and because `claim` must be a compare-and-set: it may carry the assignee it
expects to replace, so two agents racing for the same issue cannot both win. A plain `PATCH` of the
assignee field would be a lost update. Labels are added and removed individually for the same
reason — a whole-set replace would silently discard a concurrent edit. The label being removed
travels as a query parameter rather than a path segment because a label may contain a slash (§2.2).

Every request may carry an `X-Awb-Identity` header holding the caller's resolved identity (§7).
Version 1 neither authenticates nor reads it — the client already sends explicit `assignee` values
— but it is the field a version 2 server would authenticate and attribute, and having it on the
wire from the first release is one of the preparations of §10.3.

An `{id}` or `{other}` path segment accepts an unambiguous prefix or bare hash exactly as a
command does (§8), answering `400` when it matches more than one issue and `404` when it matches
none, so the CLI needs no extra round trip in remote mode.

Everything under `/api/` is the JSON API and `/openapi.json` and `/openapi.yaml` are the document;
every other path belongs to the web UI.

Request and response bodies use exactly the same JSON representation as `--json` output, so agents
can move between the two transports without relearning anything. Query parameters carry the same
names as the corresponding CLI filter flags, in the same kebab-case spelling (`include-closed`,
`priority-max`); a boolean parameter is written `name=true` or `name=false` and a
repeatable filter is repeated rather than comma-separated, exactly as on the command line.
`GET /api/search` carries its terms the same way: `q` is repeated once per positional argument of
`awb search`, each value one literal term that may itself contain spaces, and a request with no `q`
is a `400`. The confirmation and override flags of the destructive commands are boolean query
parameters too:
`DELETE /api/projects/{key}?cascade=true`. There is no
`force` parameter anywhere: the HTTP method is the confirmation that `--force` supplies on the
command line, and the overrides that `--force` carries on `claim` and `release` are body fields
(§6.4). Directory context is resolved on the client, so the server never inspects the caller's
working directory and needs no parameter of its own for it: a resolved context is nothing but the
`project` and `label` parameters the client sends, exactly as if they had been typed as filters.
`--no-context` and `--mine` likewise never reach the server — the first suppresses parameters the
client would otherwise have sent, and the second is sent as `assignee=<identity>`.

### 6.1 OpenAPI

The HTTP API is specified by an OpenAPI 3.1 document, so third-party user interfaces and
integrations can be built against it and clients can be generated from it. The document lives in
the repository, is embedded in the binary, and is served at `/openapi.json` and `/openapi.yaml`.

* Its component schemas are the CLI's `--json` structures: `Issue`, `IssueTree`, `Relation`,
  `Project`, `Error`, plus `Facet` for the two endpoints the CLI has no counterpart for (§6.2) and
  the request bodies of §6.4. There is no second, HTTP-only *response* representation of anything the CLI also
  returns — if the shapes ever diverge, the JSON output is what changed and both must be corrected
  together.
* Enumerations (`type`, `status`, relation types) and the priority range are declared in the schema,
  so a generated client validates the same vocabulary the CLI enforces.
* The two creating endpoints, `POST /api/issues` and `POST /api/projects`, answer `201` with the
  new object and a `Location` header naming it. Every other successful request answers `200` with a
  body; there is no `204` anywhere, since even a `DELETE` returns an object (§6.4).
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
  update and delete issues; the four status transitions; labels; relations; projects.
  The only CLI commands with no API equivalent are the ones that are about the local machine rather
  than about the data — `init`, `serve` and `agent-guide`.
* **Safe concurrent editing.** A form-based UI reads an issue, the user edits it for a while, and
  the UI writes it back; a plain `PATCH` would silently overwrite whatever changed meanwhile. So
  `GET /api/issues/{id}` returns a weak `ETag`, `W/"<updated_at>"`, and every request that mutates
  one issue — `PATCH`, the four transitions, the label and relation endpoints and
  `DELETE /api/issues/{id}` — honours `If-Match`, answering `412` when the issue has moved on. What makes that reliable is not the
  millisecond resolution of `updated_at` but its being strictly increasing per issue (§2.2): two
  successive versions of one issue can never carry the same timestamp, whatever the host clock's
  real resolution is, so the tag identifies a version rather than an instant. That is also what
  lets a version 2 mechanism order the versions of a row. It is weak rather than strong, and no
  use as a cache validator, precisely because it does not cover the whole body: it guards the
  issue's own stored fields, and two bodies differing only in `relations`, `blocked` or `blockers`
  carry the same tag. `If-Match`
  is optional — a caller that omits it, as the CLI always does, gets last-write-wins — but a UI is
  expected to send it. Every successful endpoint response whose body is one `Issue` or one
  `Project`, including a
  mutation response, carries the ETag for the returned version, so a client can make another
  conditional edit without first repeating the GET. It guards the issue's own stored fields,
  which is what a form edits; a
  relation added meanwhile does not move `updated_at` (§2.2) and so does not invalidate the ETag,
  and neither does `blocked` flipping because somebody closed a blocker. This is the same concern
  that already makes `claim` a compare-and-set and labels individually mutable.
  A project is edited through the same form-read-write cycle and gets the same treatment.
  `GET /api/projects/{key}` returns a weak `ETag`, `W/"<updated_at>"`, built from the project's own
  `updated_at` (§2.1), and `PATCH /api/projects/{key}` and `DELETE /api/projects/{key}` honour
  `If-Match` and answer `412` when the project has moved on. It is the issue rule with the entity
  changed, down to the detail that the tag covers the project's stored fields alone: `active_issues`
  moving because somebody created or closed an issue does not invalidate it, exactly as a new
  relation does not invalidate an issue's.
* **Enough for the chrome, not just the content.** Editing UIs need to populate filter menus and
  autocomplete: `GET /api/labels` and `GET /api/assignees` return the distinct values in use as
  `{"value", "count"}` objects, sorted by value. Both honour the same filters as
  `GET /api/issues`, including the facet's own — `?label=parser` lists the labels that co-occur
  with `parser`, so a UI can narrow progressively. `count` counts the issues matching those
  filters, so with no filters it counts the issues that are not closed, that being the default
  everywhere; a value with a count of zero is not listed at all, "in use" meaning in use under the
  filters in force. `GET /api/assignees`
  has no row for the empty assignee; unassigned is a filter (`unassigned=true`), not a value. Where
  these two endpoints are paged, `limit` and `offset` page the facet rows themselves and never the
  issues behind them, so `count` is the same whatever page it appears on.
* **Paging.** Every endpoint that returns an array — `/api/issues`, `/api/ready`, `/api/blocked`,
  `/api/search`, `/api/projects`, `/api/labels`, `/api/assignees` — accepts `limit`
  and `offset` and returns the unpaged total in an `X-Total-Count` header, so a UI can show
  "1–50 of 214" and page through without loading everything. `GET /api/issues/{id}/tree` is not
  one of them: a tree is returned whole (§4.4). `limit` and `offset` must be non-negative integers;
  `limit=0` returns no rows while preserving the unpaged total in `X-Total-Count`. There is no
  default `limit`: omitting it returns every row, as the command line does (§4.3), so a remote-mode
  listing is never silently truncated.
* **Markdown source, never HTML.** The API returns and accepts the description exactly as stored, so
  an editor round-trips it losslessly. Rendering — and sanitising (§9) — is the UI's job. The
  derived `links` array (§2.4) stays available for clients that want the links without a parser.
* **Cross-origin access, opt in.** A UI hosted anywhere other than the server itself is a browser
  origin the API must allow explicitly, via `--cors-origin` (§4.5). The default is to allow none,
  because the API is unauthenticated and any page in the user's browser could otherwise reach it.
  For an allowed origin the server answers preflight `OPTIONS` requests, permits the methods and
  request headers the API uses (`Content-Type`, `If-Match`, `X-Awb-Identity`) and exposes `ETag`
  and `X-Total-Count` in `Access-Control-Expose-Headers` — without which a cross-origin UI could
  use neither the optimistic concurrency nor the paging above.

`offset`, `X-Total-Count`, `limit` on the array endpoint the CLI has no `--limit` for
(`/api/projects`), `GET /api/projects/{key}` for which the CLI has no `project show`, the two facet
endpoints, the `ETag`/`If-Match` handshake, the `expect_assignee` of §6.4 and the `X-Awb-Identity`
header are the only places where the API is wider than the CLI. Everything else stays one-to-one, and all of
them are declared in the OpenAPI document like the rest.

Two things a write UI needs that are deliberately *not* API concerns. Directory context is resolved
by the client (§5), so a browser UI, having no working directory, simply does not have one and
filters by `project` and `label` explicitly. And errors stay a single human-readable `Error`
message (§4.6) with a status code; there is no field-level validation report, because the vocabulary a form must respect
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
  `title`, `description`, `type`, `priority`, `assignee` — plus `labels` and an initial
  `relations` array of `{"type", "other"}` pairs, read with the new issue as the subject exactly as
  `awb create`'s relation flags are. Everything but `project` and `title` may be omitted and then
  takes the default of §2.2. The whole body is applied in one transaction, so the API creates an
  issue with its labels and relations in a single call, as the CLI does. A non-empty `assignee`
  sets `status` to `in_progress`, exactly as `awb create --assignee` does.
* `PATCH /api/issues/{id}` takes only what `awb update` can change: `title`, `description`, `type`
  and `priority`. `labels`, `status`, `assignee` and `close_reason` may appear but
  may not change: each is ignored when it equals what is stored — `labels` compared as the sorted
  form of §4.6 — and rejected with `400` when it differs, because labels are mutated individually
  and the transitions are their own endpoints. The derived and immutable
  fields (`id`, `project`, `created_at`, `updated_at`, `blocked`, `blockers`, `relations`, `links`)
  are ignored whatever they say — `relations` among them because a relation added meanwhile does
  not move `updated_at` (§6.2), so rejecting a stale one would fail a `PATCH` whose `If-Match` is
  still good. Together those rules let a UI send back the object it read with only the
  fields it edited changed, while a `PATCH` that genuinely tries to close an issue or rewrite its
  labels is refused rather than silently dropped. Any unrecognised field name is rejected with
  `400`; so it is in every other request body below.
* `POST /api/issues/{id}/claim` takes `{"assignee", "expect_assignee", "force"}`. `assignee` is
  required — the client resolves its own identity (§7). `expect_assignee` is the compare-and-set:
  when present, the claim proceeds only if the current assignee is exactly that value, `""` meaning
  unassigned, and otherwise answers `409`. `force` defaults to `false` and does what `--force` does.
* `POST /api/issues/{id}/release` takes `{"assignee", "force"}`. `assignee` is the caller's
  resolved identity (§7), as on `claim`, and is what the "assigned to someone else" refusal
  compares against; it may be omitted when `force` is true, that refusal being the only thing it
  serves. `POST /api/issues/{id}/reopen` takes no body.
* `POST /api/issues/{id}/close` takes `{"reason"}`, with the semantics of `--reason` (§4.3): absent
  leaves any recorded reason alone, `""` clears it.
* `POST /api/issues/{id}/labels` takes `{"label"}`, one label per call.
* `POST /api/issues/{id}/relations` takes `{"type", "other", "force"}`, read with this issue as the
  subject, where `force` replaces an existing parent as `dep add --parent --force` does.
* `POST /api/projects` takes `{"key", "name", "description"}` and `PATCH /api/projects/{key}` the
  same without `key`, replacing each field it carries and leaving the others alone, like
  `project update`. `active_issues`, `created_at` and `updated_at` are derived and are ignored
  whatever they say, so a UI can send back the object it read.

Every mutating endpoint answers with the resulting object, including the label and relation
removals — a client that renders the response must see the change. Only the two that delete the
addressed entity, `DELETE /api/issues/{id}` and `DELETE /api/projects/{key}`, answer with the
object as it was immediately before deletion, which is the `--json` behaviour of §4.1; those two
bodies carry no `ETag`, the version they describe being gone.

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
variable or command-line flag is a usage error (exit code 2). A syntactically valid project key
that is selected by any source but names no project is not found (exit code 3).

### 7.1 User configuration

`$XDG_CONFIG_HOME/awb/config.yaml`, falling back to `~/.config/awb/config.yaml`:

```yaml
db: /home/mikael/.local/share/awb/awb.db   # path or http(s) URL
identity: mikael                           # default assignee, --mine, claim --as
project: awb                               # default project for create
color: auto
```

When `identity` is unset it defaults to the OS username, lower-cased and stripped of any character
outside the assignee set of §2.2, so that a name like `Mikael` or a Windows `DOMAIN\user` still
yields a usable identity. That is a value the user never typed, which is why it is folded rather
than refused; a value given on the command line or in a configuration file is still rejected
(§2.2). If nothing is left after stripping, the commands that need one — `--mine`,
`claim` without `--as`, and `release` without `--force`, which compares the stored assignee against
it (§4.3) — fail with exit code 1 and ask for `identity` to be set.

### 7.2 Local configuration

`.awb.yaml`, found by the upward search of §5:

```yaml
project: awb              # this directory's project
label: frontend           # this directory's label
```

Both keys are optional and either may stand alone; a file with neither is legal and simply gives an
empty context. Their meaning is §5, and it is the only meaning they have — this file is what makes
a directory mean something to `awb`, and its presence is the whole of the mechanism.

A value outside the project key or label vocabulary (§2.1, §2.2) is a configuration error under the
general rule above, whatever the command; a well-formed `project` that names no project is exit
code 3, on the commands that use the context. Either way a mistyped key is reported rather than
quietly ignored.

The file is meant to be committed, so that a checkout carries its own scope for everyone who clones
it. Because it therefore may not have been written by the person running the command, it may
**not** set `db`, `identity` or `color`: a directory can shape what you see, but cannot redirect
where your issues are stored or claim to be you. Those keys are ignored if present, without an
error and without a warning, exactly like unknown keys — which are also ignored, so future versions
can add settings without breaking older binaries. Ignored means unread: their values are not
type-checked either, so only `project` and `label` can make this file fail a command.

### 7.3 Precedence

Command line flags, then environment variables (`AWB_DB`, `AWB_IDENTITY`, `AWB_PROJECT`,
`AWB_COLOR`), then the local configuration file, then the user configuration file, then the
built-in defaults. The default project for `awb create` follows exactly that order (§5).

`project` in the *user* configuration file, and `AWB_PROJECT`, are that creation default and
nothing else: they never act as an implicit `--project` filter, so a list-like command run outside
any directory context shows every project. `project` in the *local* configuration file is the one
that filters too, because scoping a directory to its own work is what that file exists for.

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
mutable relation (§2.3).

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
* Layering: a storage package owning the schema and queries; a domain package owning readiness and
  relation validation; thin CLI and HTTP adapters over it, so both surfaces exercise the same code
  paths. Directory context is resolved in the CLI, the only surface that has a working directory.
  The web UI is a client of the HTTP adapter, not a third adapter.
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
