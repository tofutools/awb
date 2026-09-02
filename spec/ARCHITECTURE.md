# Architecture

Agent Work Board is one Go binary over SQLite. The binary can operate directly
on a database file or serve the same operations through an HTTP API and bundled
web UI.

This document describes the system's durable boundaries and the reasoning
behind them. Behavioral detail belongs beside its implementation and in the
tests that pin it down; `AGENTS.md` maps these boundaries to packages.

## Design goals

Three assumptions shape the system.

### Agents are primary callers

An agent needs to discover work, claim it without a race, update it, and report
the outcome through interfaces that cost little context and fail predictably.
Commands are therefore non-interactive by default, output modes have explicit
contracts, and errors have one taxonomy across CLI and HTTP.

The interactive list viewer is a human affordance that must be requested and
requires a terminal on both input and output. It cannot accidentally appear in
front of an automated caller.

### The vocabulary must stay teachable

Types, statuses, priorities, and relation types are fixed. Everything specific
to a team goes in labels and Markdown. This gives agents a complete mental model
in a short instruction and prevents every workspace from becoming a different
tracker.

### Local operation should require no service

One SQLite database can span all of a user's workspaces. It needs no daemon,
version-control integration, or network configuration. Starting a server adds
sharing and authorization without creating another application model.

awb targets individuals, small teams, and open-source projects. It deliberately
does not grow a configurable workflow engine, custom-field framework, sprint
system, or reporting platform.

## System shape

Every caller reaches the same operation boundary:

```text
CLI command ─┬─ local backend ── BEGIN IMMEDIATE ── SQLite + blob directory
             │
             └─ remote backend ── HTTP ── handler ── local backend ── storage

Browser UI ─────────────────────── HTTP ── handler ── local backend ── storage
```

Commands cannot distinguish direct from remote mode. HTTP handlers do not
reimplement operations; they call the same local backend. This is what makes
CLI/API parity structural rather than a checklist.

## Domain model

### Workspaces

A workspace is the immutable boundary around a set of issues. Its key becomes
the issue ID prefix and never changes. Issues cannot transfer between
workspaces; moving them would break IDs, stable URLs, graph interpretation, and
authorization scope at once. A workspace's display name and Markdown
description remain editable.

A workspace is either active or archived. Archiving preserves the key, issues,
relations, blobs, membership, preferences, timestamps, and URLs as read-only
history. Normal discovery and creation targets omit archived work, and every
work mutation touching it is refused. Restore reactivates the same boundary.
Each actual transition records a small lifecycle entry; repeating the current
state is an idempotent no-op.

Hard deletion is separate and explicit. It refuses while issues remain unless
the caller also requests a cascade.

### Issues

An issue belongs to exactly one workspace and carries:

- a title and Markdown description;
- one fixed type and workflow status;
- priority from zero through four;
- a sparse manual order;
- labels and an ordered set of assignees;
- relations, attachment metadata, and extracted Markdown links;
- creation and update timestamps.

The complete issue representation is shared by both surfaces. Collections are
normalized and deterministic, and absent collections are empty arrays rather
than null. Blocked state, blockers, relations, links, and attachments are
read-only derived fields; their own operations change them.

IDs have the form `<workspace>-<hash>`. The hash is derived from creation data
rather than a global sequence, allowing a local caller to mint an ID without a
coordination service. Any unique full-ID prefix or bare hash prefix resolves to
the same issue. Ambiguity is a conflict, never an arbitrary choice.

### Assignment and workflow

The workflow has three states: `open`, `in_progress`, and `closed`.

Assignment and state move together where their meaning requires it. Creating
with assignees is create-and-claim. Claiming joins the assignee set and starts
the issue atomically. Releasing removes one identity and reopens the issue if
the last assignee leaves. Reopening clears all assignees. Closing retains them
as the people who completed the work.

The board's `move` operation changes status, optional epic parent, and optional
manual position in one transaction. A visible card cannot move halfway between
columns or lanes.

### Relations and readiness

Every relation reads “subject — type — other” on every surface:

- `blocked-by` drives derived readiness;
- `has-parent` expresses decomposition and board swimlanes;
- `discovered-from` records provenance;
- `related` is a symmetric association.

The three directed relation types are independently acyclic. `related` is
stored canonically and has no direction. Relations may cross workspace
boundaries because dependencies and provenance do not stop at an organizational
label.

Blocked state is computed from the live graph. It is not stored on the issue,
so it cannot drift from its blockers. An issue is ready when it is open and has
no non-closed `blocked-by` target. The ready listing additionally selects
unassigned issues because it answers what somebody new can pick up.

### Activity

Each issue has one append-only stream containing Markdown comments and
structured change entries. A successful mutation records its change in the same
transaction; a failed or no-op operation records none. Field changes carry
before and after values, while resource actions such as attaching a file have a
stable action name.

A non-empty close reason is one typed comment committed with the transition. It
therefore remains meaningful after reopen without introducing another mutable
field.

Activity is a work log, not an immutable compliance ledger or a reconstructable
entity history. Hard deletion removes it with the issue.

### Attachments

An attachment is identified by `(issue, name)`, just as a label is identified by
its issue and value. It has no synthetic ID, is unique by name within one issue,
and is immutable.

Only metadata lives in SQLite. Content lives in an attachment directory as one
file per SHA-256 digest, so equal bytes share storage. Upload and download are
streamed end to end; buffering a maximum-size file anywhere in the path is a
regression.

An upload copies into a staging file before opening its write transaction. It
inserts metadata and places the content under its digest after the transaction
has the writer lock and before commit. This ordering permits a harmless orphan
after a failed commit but never a committed row whose bytes are absent.

Deletion commits the metadata removal first, then takes a second write
transaction to unlink content only if no row still references its digest. An
unlink cannot be rolled back, so performing it before the first commit would
risk restored metadata naming a missing file. Holding the writer lock while
checking and unlinking orders deletion against a concurrent upload of the same
bytes.

### Boards, views, and preferences

The board is a projection of issues into epic lanes and status columns. “No
epic” is a derived lane rather than a special issue. Board pages and each column
are bounded independently so a large installation cannot create an unbounded
response.

Manual ordering is sparse. Moving between a predecessor and successor normally
chooses a value between them instead of rewriting every card; storage can
rebalance when no gap remains. Natural issue lists use the same order when no
explicit sort overrides it.

A saved board view belongs to one user and stores filter scope, not a materialized
issue set. It may be shared by link, but every reader's current authorization
and ignored-workspace preferences still apply. Empty selected scope and “all”
are distinct states so disappearing access cannot silently widen a saved view.
Each saved view owns its filter scope and closed-card retention window. The
default board accepts the same fields as request preferences, which the web UI
keeps locally for that browser and identity. Board-hidden issues never enter
the projection. A closed epic stops being a lane immediately, while a closed
non-epic card remains until its most recent close is older than that window.
Epic lanes may also be hidden as viewer-local presentation state for one board
without changing the epic or a saved view's shared definition. The exclusion
is applied before lane totals and pagination. Card pagination remains
independent for every epic and status column.

Ignoring a workspace is per-user presentation state. Normal discovery excludes
it, while the dedicated preference recovery path can still list it. This layer
never changes authorization or graph truth.

## Layering

### Domain: rules without I/O

`internal/domain` owns vocabulary, validation, IDs, graph rules, readiness,
permission decisions, password hashing, Markdown link extraction, filtering
semantics, and compact encoders. It does not read a database, filesystem,
request, or process environment.

When a rule needs graph knowledge, storage gathers the relevant sets and calls
a pure domain function. Keeping the rule independent of traversal makes it the
same rule for local operations, API requests, migrations, and tests.

Prose is gated on the way in rather than on the way out. Every Markdown field —
an issue's and a workspace's description, a comment body, and the close reason
that becomes one — is parsed with the one pinned dialect and refused if it
holds raw HTML or a link or image destination whose scheme the field does not
allow. Refusing at the boundary is what makes a stored description something a
renderer can trust: script, style, SVG and MathML are all raw HTML, so keeping
raw HTML out keeps all of them out, for the terminal renderer and the web UI
alike. The gate rewrites nothing, so a description and a comment body are
stored byte for byte, and a close reason after the trimming its own rule
applies.

The gate is on the operations, so it holds for what is written through them and
not for what a database already contains: rows written before the gate existed
are not revalidated, and `dump`'s restore deliberately copies prose verbatim
because a faithful local copy is the whole point of a dump. Both renderers
escape raw HTML rather than honour it, which is what keeps such rows safe to
display.

### Backend: one operation contract

`internal/backend` is the interface every command and handler uses.
`internal/local` implements it with one `BEGIN IMMEDIATE` transaction per
mutation. `internal/remote` implements it over HTTP.

The interface exposes domain operations rather than SQL-shaped primitives. For
example, claim, close, move, and create-with-relations are atomic operations,
not client-side sequences.

### Storage: persistence and scoped reads

`internal/storage` owns schema, migrations, SQL, transaction helpers, and the
blob store. Released migration batches are immutable and new versions append a
batch, preserving the ability to upgrade every released database.

SQLite runs in WAL mode. Mutations begin immediate transactions, taking the
single writer's turn before they read the state their write depends on. Readers
can continue from a stable snapshot, and two agents cannot both win a guarded
claim or graph update.

Server authorization scope is carried by the transaction and consulted by
every query. It is not a parameter a caller can forget to pass. A missing scope
check would leak rather than fail, so tests exercise listings, facets, search,
trees, navigation, boards, and direct lookup as one privacy surface.

### CLI and remote adapter

`internal/cli` owns the command tree, output modes, configuration, exit codes,
demo data, and server startup. It contains no alternate SQL path for remote
commands.

`internal/remote` maps the backend interface to HTTP, including pagination,
streaming attachments, conditional edits, and error classification. A server
response that maps to the shared taxonomy produces the same command exit class
as direct mode.

### OpenAPI and handlers

`openapi.yaml` is the HTTP source of truth. It generates Go routing, request
decoding, validation, and the TypeScript API types. Generated files are not
committed and are never edited directly.

`internal/openapi` reads each operation's declared parameters and bodies back
from the document. Handlers refuse undeclared input instead of accepting a
second accidental API contract. `internal/handler` adapts generated operations
to the backend interface and HTTP concerns such as ETags and total counts.

### Web UI

The TypeScript frontend calls only the HTTP API. Compiled assets and committed
third-party browser bundles are embedded by the Go binary. It has no privileged
database path and receives exactly the identity and authorization behavior any
other client does.

## Authentication and authorization

Authorization is a server property and is enforced in `internal/local` inside
the same transaction as the operation. Direct mode applies none: a caller who
can open and rewrite the database file already has complete access, so an
application check would be advisory rather than a security boundary.

The database records whether authentication has ever been enabled. A database
that has never held users can be served openly on loopback. Adding the first
user enables Basic Authentication on the next request. Removing the last leaves
the server locked instead of falling open. Direct access is the recovery path.

Workspace access is either regular or admin. Regular members work with issues;
admins also manage membership. Account-wide workspace administrators manage
workspace entities and implicitly administer all of them. User administrators
manage accounts and account-wide capability flags.

Unauthorized entities are normally absent: queries omit them and direct reads
answer not found. The graph deliberately reveals a related issue's ID when that
limited fact is necessary to explain a visible issue or compute truthful
readiness, while the issue itself remains unreadable.

## Configuration and directory context

User configuration chooses storage, remote credentials, identity, default
workspace, and presentation. A repository's `.awb.yaml` may choose only a
workspace and creation label. It is meant to be committed and may have been
written by somebody else, so it cannot redirect storage, select credentials,
or impersonate an identity.

The local file is found by walking upward from the working directory. This is
directory context, not Git integration: worktrees and repositories are ordinary
directory trees as far as awb is concerned.

## Deployment boundaries

The server binds loopback by default. It refuses to expose a never-authenticated
database through a public-looking configuration unless `--no-auth` makes that
choice explicit. It does not terminate TLS; a reverse proxy supplies TLS and an
optional base path, while `--public-url` tells browser security checks which
origin is real.

Cross-site writes are checked, external CORS origins are opt-in, state-changing
responses are not cached, and attachment downloads are always opaque
attachments rather than same-origin renderable content. Attachment responses
are the one type not gzipped, preserving streaming behavior and a useful
content length.

## Compatibility boundaries

During beta, CLI and API compatibility can change. Two machine surfaces still
receive special care:

- `--json` and `--compact` are explicit automation contracts; the default
  terminal presentation is not.
- database migrations move every released schema forward. A released migration
  is never rewritten.

Changes to the general system shape belong here. Exact field limits, command
flags, request schemas, and transition corner cases belong with their enforcing
code, generated contract, and tests.
