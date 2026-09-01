# Architecture

`awb` is an agent-first issue tracker: one Go binary over SQLite, offering a
command line, an HTTP API and a bundled web UI.

This document describes the shape of the system and the reasoning behind it. It
is deliberately free of implementation detail: the code and its tests are
authoritative for behaviour, and `AGENTS.md` maps the packages.

## 1. What the design is for

Three assumptions drive everything else.

**Agents are the primary caller.** Every operation is one non-interactive
command with a stable, parseable output and a meaningful exit code. Output modes
exist that minimise context consumption, because context is the scarce resource
for the tool's main user. The one interactive affordance is a human's, is asked
for explicitly, and needs a terminal on both standard input and standard output;
it therefore cannot appear in front of an agent by accident.

**The vocabulary must fit in a few lines of instruction.** A fixed set of types,
statuses, priorities and relation types, none of it configurable. Everything a
team wants to express beyond it goes into labels. This is what makes the tool
teachable in a paragraph, and it is why no amount of demand should turn any of
those enumerations into configuration.

**One database, no ceremony.** One SQLite file per user, shared by all their
projects, reachable over HTTP by things other than the CLI. No server required,
no configuration required, no version control involved.

It targets individuals, small teams and open source projects. It deliberately
does not target enterprises: there is no permission model, no configurable
workflow engine, no custom fields and no reporting suite.

## 2. The domain

### Projects and issues

A **project** (shown as a **Workspace** in user-facing surfaces) is the
top-level organising unit; every issue belongs to exactly one, and that
workspace never changes. An issue's immutable workspace key is also its ID
prefix. An **issue** carries a
title, an optional Markdown description, a type, a status, a priority, a set of
labels and an ordered set of assignees.

The description is the issue's editable free text. References to pull requests,
CI runs, logs and design documents are ordinary Markdown links inside it, so
there is no link entity and no link records to keep in step with anything.

### Comments and activity

Every issue has an append-only activity stream. A **comment** is Markdown prose
with an author and creation time. It is never edited or deleted independently:
adding revision and moderation rules before they are needed would turn one
simple operation into a second history system.

The same stream holds compact, structured **change events**. A successful issue
mutation records one event in the same `BEGIN IMMEDIATE` transaction as the
state change; a failed or no-op mutation records none. Field changes retain
their before and after JSON values, while actions such as attaching a
file are named explicitly. Entries are ordered newest first by creation time
and then by their monotonically assigned id, so the order is total even when
several writes share one millisecond.

A non-empty close reason is represented by one comment entry whose stable
action is `closed` and whose field changes include the status transition. It is
therefore both prose and an unambiguous part of the close operation, without a
second mutable field on the issue. Reopening leaves that historical entry in
the stream, and attempting to close an already closed issue records nothing.

This is a work log attached to the issue, not an immutable compliance ledger or
full entity version store. Hard-deleting an issue deletes its activity with it,
and there are no tombstones, retention rules or reconstruction operation.

### Attachments

A link cannot stand in for everything. A stack trace, a failing log or a
screenshot is evidence that has to travel with the issue, so an **attachment**
is a file attached to one, carrying a name, a content type, a size and the
SHA-256 of its content.

**An attachment is identified by its issue and its name**, and carries no
identifier of its own. That is how a label is identified too, and an attachment
is the same shape of thing: a set of values hanging off one issue rather than
an entity in its own right. A synthetic id would be a second name for something
that already has one, and one nobody would ever type — where an issue id is
minted because an issue arrives with nothing to call it, an attachment arrives
with a name.

The consequence is that a name is unique within an issue. A second file under a
name the issue already holds is refused rather than being given a name it was
not asked to have, which is also what a filesystem does. Two issues may each
hold one called the same thing.

**The content is not in the database.** It is a file in a directory of them,
which defaults to sitting beside the database and can be pointed at a
filesystem of its own — because the reason to store files is that they are
large, and a tracker that swallowed them would stop being a small file anyone
can copy, back up and read with a SQLite shell. Only the metadata is a row.

Each file is named by its own SHA-256, and that one decision settles three
things. Writing one is idempotent, because whatever is already under that name
holds the same bytes. Two attachments of the same content share one file, which
is why deleting one removes the file only once no row names that digest any
more. And the content can be put in place *before the row that names it is
committed* — the bytes are copied to a staging file before the transaction
begins, and given their final name inside it, after the row is inserted and
before the commit — so a committed row never points at a file that is not
there. The failure that is left instead — a file no row names — is unreachable,
harmless, and adopted by the next upload of the same bytes.

An attachment is immutable. Nothing changes one, which is why it carries no
update timestamp, no entity tag and no conditional edit: attach the file again
and remove the old one. Attaching or removing one does move the issue's own
timestamp, because the attachment array is part of the issue representation.
That also invalidates an issue entity tag held before the attachment change and
moves the issue in an updated-time listing.

The content type is what the caller says it is, and what the first bytes say it
is when the caller says nothing. It is sniffed from the content rather than
from the name's extension, because an extension table is a file on the machine
and would make the same upload get different answers on two of them.

**Content is streamed end to end and never held whole.** Not as an
optimisation: a server that buffered an upload would let a handful of
concurrent callers cost it the attachment maximum each, and the size limit
would become a limit on memory rather than on a file. So an upload is copied
from the request body to disk as it arrives, hashed on the way past, and a
download is copied from the file to the response the same way. What one
transfer costs the server is a copy buffer, whatever the file's size.

That is a property of the whole path rather than of one function — the client,
the transport, the middleware, the generated decoder, the handler and the blob
store all have to keep it, and any one of them could quietly stop. It is
therefore measured rather than asserted: a test moves a payload through the
real server and fails if either direction allocates anything approaching it.

### Identifiers

An issue ID is `<workspace-key>-<hash>`, where the hash is derived from the
issue's own content and a random salt. That derivation matters architecturally
for one reason: **IDs are independently mintable**. No counter, no coordination,
no central allocator — which is what would let two databases be merged, or a
second machine mint IDs, without a design change. IDs are not content-addressed
and must never be reconstructed; the salt is what makes two identical creations
distinct.

Any unambiguous prefix, or a bare hash, works wherever an ID does. Uniqueness of
a bare hash is a property of the data at a moment rather than a guarantee, so an
ambiguous reference is reported rather than resolved by guessing.

### Relations

A relation is a directed link between two issues, which may be in different
projects. There are four, and each is named from the point of view of its
subject and reads *subject — relation — other*. That single convention holds
everywhere a relation is named: in the model, on the command line and in the
API, so there is never a question of which way round an argument goes.

* `blocked-by` — the subject cannot start until the other is closed. The only
  relation that affects readiness.
* `has-parent` — decomposition. An issue has at most one parent.
* `discovered-from` — provenance.
* `related` — a loose, symmetric association with no behaviour attached.

The three directed graphs must each stay acyclic, and are checked separately:
work cannot depend on itself, decomposition cannot nest inside itself, and an
issue cannot be its own origin. One further rule crosses two graphs — an issue
may not be `blocked-by` an ancestor or descendant of its own decomposition,
because a child waiting for its parent describes work that cannot be ordered.

### Derived state, not recorded state

**`blocked` is not stored.** An issue is blocked when it is itself not closed
and something it is `blocked-by` is not closed. Closing a blocker therefore
makes other issues ready with nothing written to them, and a closed issue is
never blocked whatever its relations still say.

This is the design's most load-bearing choice: it makes it *impossible* for the
recorded state to disagree with the dependency graph. The same instinct appears
throughout — `active_issues` on a project is counted, not maintained; the links
in a description are parsed, not stored.

**Status and assignees cannot drift apart either.** Four transitions — claim,
release, close, reopen — are the only things that move either, and the general
update operation can move neither. An issue is in progress while at least one
person is assigned; claim joins the ordered set, release removes the caller,
and the last release returns it to open. Assignment rows and status move in the
same write transaction, and storage refuses a write where their emptiness and
the requested status disagree.

### Readiness

An issue is **ready** when it is open and not blocked. `ready` additionally
lists only unassigned issues, because "what should nobody in particular pick up
next" is the question it exists to answer. It is the primary agent entry point,
and the whole dependency model exists to make its answer trustworthy.

Readiness guides listings rather than enforcing workflow: a non-ready issue can
still be closed, and closing never inspects related issues. The one exception is
claiming, which refuses a blocked or closed issue unless forced.

## 3. Layering

```
                 command line          HTTP API + web UI
                       \                      /
                        \                    /
                         one backend interface
                        /                    \
                   local                    remote
                 (SQLite)                   (HTTP)
                     |
              domain + storage
```

Two structural rules hold this together, and most of the design's guarantees are
consequences of them rather than of discipline.

### One interface, two implementations

Every command is written against a single backend interface. One implementation
is the local SQLite backend; the other is an HTTP client. Pointing the tool at a
server URL swaps the implementation, and a command **cannot tell them apart**.

That is what makes "remote mode behaves identically to direct mode" structural
rather than a promise somebody has to keep. It is verified by running the same
scripted sequence against a file and against a server on that file, and
comparing output and exit codes byte for byte.

The HTTP handlers sit on the same interface, over the local implementation. So
the API and the command line are not two implementations of the same rules —
they are two adapters over one. Neither can gain a behaviour the other lacks
without the shared layer gaining it first.

### The domain layer does no I/O

Vocabulary, text validation, identifier derivation, link extraction, the graph
rules and readiness live in a layer that reads and writes nothing. Both surfaces
share it wholesale, so they cannot accept different strings or enforce different
graphs.

Where a rule needs to consult stored state — "would this edge close a cycle?" —
the rule stays in the domain layer as a function over sets, and the traversal
that gathers those sets lives in storage. The statement of the rule and the
means of answering it are kept apart on purpose: the rule is then testable
without a database, and there is exactly one place it is written down.

### Failure is classified once

Five kinds — usage, not found, conflict, forbidden, runtime — are shared by
every layer. The command line maps them to exit codes and the API to status
codes, and the HTTP client maps them back. Because the mapping is defined once,
in one place, a command's exit code is the same in both modes without either
adapter knowing about the other.

Statuses that no kind maps onto — authentication, cross-site rejection, failed
precondition, and the rest — are the ones provoked by *how* a client behaved
rather than by *what* it asked for. They collapse to the generic runtime code,
because the command line has no separate meaning for them. Authentication is
among them and authorization is not: a 403 refuses what the caller asked for and
carries its own code, while a 401 says the credentials themselves are wrong,
which is a failure to connect.

## 4. Storage

A single SQLite file holds everything but attachment content, which is a
directory of files beside it. There is no per-directory database, so a user has
one tracker unless they explicitly point at another.

**Only `init` and an explicit `dump --output-db` create one.** Every command
that opens its selected database and finds the file missing fails and names the
path, so a typo in a flag or an environment variable cannot silently produce a
second, empty tracker — a failure mode that would otherwise be discovered days
later. `dump` instead requires both of its named outputs to be absent unless
`--force` is explicit. A forced dump builds its replacement in staging
paths and leaves the existing pair untouched unless that replacement completes;
a failed download therefore does not destroy the last usable dump. The file
also carries an application stamp, so the same typo cannot point at somebody
else's database and have this one's migrations applied to it.

Schema changes are ordered migration batches recorded in SQLite's own version
counter, so the schema carries no bookkeeping table and the number is readable
by anyone with a SQLite shell. A binary refuses a database newer than it
understands rather than operating on a schema it does not know.

**Invariants live in the database where they can.** Constraints enforce the
priority range, the enumerations and the at-most-one-parent rule. The status and
assignment set span two tables, so storage validates their pairing and
transitions update both inside the same `BEGIN IMMEDIATE` transaction.

Full text search is a SQLite full-text index over titles and descriptions, kept
in sync by triggers. Search terms are always literal: no operator, wildcard or
column prefix reaches the query, so no user or agent input can produce a syntax
error rather than a result.

### Concurrency

Concurrency is SQLite's problem, deliberately. The file is opened in
write-ahead-log mode with a busy timeout, so several local processes — three
agents in three terminals — share it safely.

Every mutation is one immediate transaction, taking the write lock *before* it
reads anything it then validates against. The graph checks, the compare-and-set
of a claim, the timestamp bump and the identifier collision retry all happen
inside one writer's exclusive turn, so no concurrent commit can slip between a
check and the change it guards. A transaction that cannot take the lock fails
rather than being retried in a loop.

An attachment's content is copied into a staging file outside any transaction,
that being the slow half. The rename that gives it its final name happens
inside the transaction that writes its row, after the row and before the
commit, so a committed row never names a file that is not there.

Removing content is the mirror image, and cannot be done in the same place.
An unlink cannot be rolled back, so one performed inside the deleting
transaction and followed by a failed commit would restore the rows and leave
them naming a file that is gone — the one state this design does not tolerate.
So a delete commits first, and a second transaction, which writes nothing,
takes the write lock to decide whether anything still names the content and to
unlink it if not. Both halves holding that lock is what puts an upload and a
concurrent delete of the same bytes in one order; the second transaction having
nothing to commit is what makes its own failure cost nothing but an
unreferenced file.

There are no leases, locks or claim expiry. A claim is a single atomic update,
and a crashed agent leaves an assigned issue that a human or another agent
releases explicitly. This is a deliberate refusal to guess: an expiry mechanism
would have to decide how long is too long, and that decision cannot be made
correctly without knowing what the work was.

### Timestamps as versions

Update timestamps move only when the represented entity actually changes, and
are forced strictly upward when the clock is too coarse to distinguish two
writes. Two versions of one row can therefore never carry the same timestamp.

That property — not the resolution — is what lets a timestamp serve as a version
identifier for optimistic concurrency, and what would let a future mechanism
order the versions of a row. The cost is that timestamps are reliable as
ordering keys but, under rapid writes, only approximate as measurements of time.

## 5. Surfaces

### Command line

Three output modes, for three readers. The default table is for humans and is
explicitly **not** a compatibility surface — nothing should parse it and it may
change freely. A compact one-line-per-issue form is for agents and is designed
to cost as little context as possible. A JSON form is the stable, complete
representation. The latter two are the compatibility surface; changing either is
a breaking change.

Being for humans, the default mode draws itself to the window it is in. On a
terminal a listing is a box fitted to the width: the columns a reader can do
without are given up, rightmost first, and the free-text ones are cut, so that
what identifies a row and what a reader scans for always fit. Piped or
redirected there is no window, so the same content is laid out as plain aligned
columns at its natural width. Colour is a separate question with its own chain,
because colour is asked for whereas a window either is there or is not.

A description is Markdown, and the default mode draws it as Markdown when it has
a window to draw it in: emphasis, headings, lists, code and links the terminal
can open. It is drawn with the same pinned dialect the extracted link list and
the web UI read, so no surface can disagree with another about what a
description says. What a given terminal makes of any of it is the terminal's
own business, which is why nothing there is the only way to reach anything: the
links a description holds are still listed as plain text beside it.

The list commands take `--interactive`, which is the same listing on the
alternate screen: the reader moves through it and the entry they choose is
printed exactly as the matching `show` command would print it, the listing
itself leaving no trace on the terminal. It is drawn by the same code that
prints one, laid out once for the window rather than once per keystroke, so
nothing shifts sideways while it is scrolled. It refuses without a terminal on
both standard input and standard output, and refuses alongside `--json` and
`--compact`, because a caller who asked for a screen to scroll asked for
something those cannot give.

The normal CLI editing workflow fetches an issue or project description to a
file and writes a receipt beside it. The receipt binds that working file to the
data source, canonical entity and entity ETag; updating from the file sends the
saved tag as `If-Match` in remote mode and enforces the same precondition in
direct mode. It therefore refuses an edit made against an older entity version.
`--force` is the conspicuous escape hatch for an intentional blind replacement.
Neither ordinary display nor the Markdown itself carries or refreshes this
metadata.

In remote mode, issue and project identifiers in the human output link to the
bundled web UI. The stable JSON form carries the same destinations as explicit
`issue_link` and `project_link` fields; they are empty in direct mode, where a
database file has no associated web address. These are CLI presentation
metadata rather than stored fields.

Mutating commands print nothing on success, so a script's output is signal.
Three say something anyway, and each says it for a reason: `awb create` prints
the new ID, because minting it is the point; the deleting commands and `awb
demo` print one human-readable line, because a command whose effect is that
something is gone should say what went. That line is not a compatibility
surface — a script reads `--json`, which returns the object.

Destructive commands take a confirmation flag rather than prompting, because
prompting would make them unscriptable. That includes `awb demo`, which refuses
while its sample project exists: nothing marks that project's contents as the
command's own, so replacing it destroys whatever is stored under the key, and
the flag is what says so.

### HTTP API

The API exists so that things other than the command line can reach the
database: third-party interfaces, dashboards and integrations, and later a
shared team instance.

It is specified by an OpenAPI document that is embedded in the binary and served
from it. The document's component schemas carry the same domain fields as the
CLI's JSON structures. The remote CLI additionally derives web-navigation links
from its configured server URL; the API does not return that presentation
metadata. A test enforces the shared vocabulary by checking the document against
the code.

**The document is the source of truth, not a description written afterwards.**
The Go server — routing, parameter and body decoding, the vocabulary, the
length and range rules, the response encoding — is generated from it, and so
are the TypeScript types the bundled UI is written against. Neither output is
committed, both are regenerated by the build, and neither is edited: changing
the API means changing the document.

Generation cannot state everything the API promises, and what is left over is
deliberately small. The handler holds the translation between the API's shapes
and the domain's, the mapping of the error taxonomy onto statuses, and four
rules a generator does not enforce: a query parameter an operation does not
declare is refused rather than ignored, so is a body sent to an operation that
declares none, a body must claim a content type that operation declares, and a
text body must be well-formed UTF-8 with no unpaired surrogate escape — which
has to be checked on the bytes, because a decoder replaces one with U+FFFD and
that is indistinguishable from a U+FFFD the caller meant. The first three rules
read what an operation declares back out of the document rather than restating
it in Go, so they cannot drift from it either.

One endpoint is not JSON. Uploading an attachment carries the file's bytes as
the body and everything else about it as query parameters, which is what leaves
`Content-Type` describing the body on the wire and lets an upload stream rather
than be held whole. It is also the one endpoint whose body cap is the
attachment maximum instead of the general one: raising the general cap to make
room for files would let any caller make the server buffer that much JSON.

Downloading one is always served as an octet-stream to be saved, whatever
content type the metadata records. Uploaded content comes back from the same
origin as the UI, and a browser invited to render it there would run whatever
an uploaded HTML file said.

It is also the one response that is not compressed, and the only one that
states its own length. Those two facts are one decision: an attachment is
opaque bytes and as likely as not already compressed, so the compressor would
spend time and memory per concurrent download to make it no smaller — and not
compressing it is what leaves the length able to say anything, since a
compressed body is a different length from the recorded one. The length sent is
the recorded size rather than one measured on the way past, so a stored file
that no longer matches its metadata breaks the transfer instead of arriving as
a plausible short one.

**The API is specified for a read/write UI.** It has complete write coverage, optimistic concurrency
through entity tags and conditional requests, paging with a total count, and
endpoints for populating filter menus and for a caller's own identity. The
bundled UI uses that write surface for comments; the CLI remains the interface
for changing issue state.

Keyboard navigation uses one bounded autocomplete endpoint. It applies the
same visibility scope as ordinary listings and substring-matches the fields a
person uses to address an issue, project or user (including a user's descriptive
full name), returning a small independent
cap for each kind rather than making the browser load whole directories.

Two decisions there are worth stating. Status transitions are endpoints rather
than fields in a general update, and labels are added and removed individually,
both because a whole-object write would silently discard a concurrent edit.
Assignment is additive for the same reason: a claim joins without overwriting
the current set, and `assignees` carries that complete set.

### Web UI

The bundled UI is a client of the same HTTP API and gets no privileged access to
the database. That is what keeps the API honest: anything the UI can do, an
integration can do, because the UI is doing it the same way.

Its frontend is compiled ahead of time and embedded, so the shipped artifact
stays one file. Third-party browser code is committed pre-built; no package
manager runs at build time.

The command palette is an extensible registry of named destinations and
backend-provided record results. The browser debounces its requests and aborts
or ignores stale ones; the palette owns focus and listbox keyboard interaction,
while the existing hash routes remain the only navigation mechanism.

### Authentication and authorization

**The database decides whether the server authenticates.** Users are rows, with
their bcrypt password hashes, and a database holding none is a server that
authenticates nobody — which is what a local tracker is, and what every version
1 database still is after migrating. Adding the first user closes the door.
The question is asked per request rather than at startup, so it closes without a
restart, and it costs one indexed lookup.

**The switch is one-way.** Losing the last user leaves the server answering
nothing until one is added again, rather than reverting to the open server:
deleting a user says who may no longer act, and reading it as "everybody may
act" turns an administrative mistake into an unguarded database that nobody was
told about.

That the database has had a user is therefore a stored fact, written by the
insert that creates one and never cleared, and not something a server
remembers. Users are added and deleted by a command line holding the file,
which a running server learns of only by looking, so a server that looked
before the first was added and again after the last was deleted would see the
same empty table twice; no amount of memory can tell those two apart, and the
window is as wide as the gap between two requests.

Only saying so gets the door open again. A restart does not: a server over a
database whose users are gone starts locked, because it answers nothing to
anybody and so exposes nothing wherever it is bound, and because an operator
whose service is supervised should not have to time creating an account against
a restart — it recovers from the next one added, as a running server does. What
`awb serve` refuses is an open server that looks published: one over a database
that never held a user, and either bound off loopback or carrying
`--public-url`, `--https` or `--basic-auth-realm`. Only the binding reaches
anywhere; the three flags are statements of intent, and an intention to publish
is what the refusal is about. `--no-auth` is the operator saying it was meant,
and means it: that server consults no users at all, so adding one does not
close the door either. Its fixed identity is attribution, not a preference
owner, even when an account with the same name happens to exist.

An open server is still not *anonymous*: it resolves one identity at startup and
attributes every request to it, so the layer below never has to handle the
absence of an identity.

**Membership of a project is the whole of the read model.** A user works in the
projects they have access to and sees nothing else. That is expressed as one
condition on a project key, carried by the transaction rather than passed to
each query, because a read that forgets it does not fail — it leaks. There is
one place a transaction is restricted, one place the caller's permissions are
read, and every query consults the transaction it is running in.

A stored per-user ignore set narrows that same transaction scope after
authorization. It is a preference rather than a permission, but applying it at
the same boundary keeps listings, searches, suggestions, facets and their
counts consistent, and makes direct and remote backends agree when their
identity names the same stored user. The project-preferences operations are the
single recovery exception: they omit only the ignore condition while retaining
ordinary authorization, so an ignored project is always available to re-enable
and an inaccessible project is never disclosed by the editor.

Named board views are stored beside that preference boundary but do not alter
it. A view owns only reusable selection — projects, labels, assignees and a
maximum priority — while each board read resolves status columns and swimlanes
for visible epic issues inside the viewer's normally scoped transaction. The
one **No epic** lane contains non-epic issues without a direct, same-project
`has-parent` edge to an epic. Other decomposition parents do not silently
become epic membership, and epic issues are lane headers rather than cards. A
shared URL can therefore render fewer lanes for a viewer with less access or a broader
ignore set, and returns only those visible project keys in its response. A
boolean can say that some configured workspaces were omitted without naming one or
distinguishing authorization from preference.

Views are personal resources rather than project resources. Their owner alone
may change or delete them; administrative flags do not imply ownership. A
shared view is unlisted and readable by stable URL, while listing returns only
the current identity's views. The virtual default board is not stored. Board
reads page epic lanes and cards independently and report unpaged totals at
both levels, so the browser never has to fetch the whole issue collection. The
HTTP boundary defaults to ten lanes and fifty cards per column and refuses a
limit above fifty; direct backend callers receive the same bounds. Deleting a
selected project advances each affected view version before the foreign-key
cascade changes its filter. A browser may fold epic swimlanes locally; that
presentation state is keyed by board reference and is not part of the shared
view. One atomic move operation changes direct same-project epic membership,
status column and manual position while applying the same assignment rules as
claim, release, close and reopen. It can clear membership into No epic but can
never change the issue's project-prefixed ID or workspace. Sparse integer ranks
normally change only the dragged issue; placing
relative to an automatic anchor ranks that explicit pair. Other automatic
issues keep falling back to priority and recency, and ranked rows are
rebalanced only when an insertion gap is exhausted. A board rank is resolved
inside its workspace, epic lane and status; regular lists edit the same rank
inside one immutable workspace and reject a cross-workspace anchor. Accessible
earlier/later actions resolve their neighbor from that whole scope inside the
write transaction, so filtering and offset pagination do not turn the visible
page boundary into an ordering boundary. Swapping inside the still-automatic
tail materialises only its prefix through the pair; this is necessary to keep
earlier automatic rows in place, and later sparse moves normally touch one row.

The user directory follows the same boundary without pretending that a person
belongs to only one project. A member sees current accounts that participated
in any visible project, including its retained assignments and activity, while
memberships in other projects stay hidden. User administrators see the whole
directory. Its response keeps current memberships separate from the projects
found in assignment and activity history; the latter has no status filter, so
closing an issue does not erase the association.

An invisible project is answered *not found* rather than *forbidden*, and so is
every issue in it, and so is every spelling of an issue reference that would
resolve to one. Forbidden is reserved for what a caller can see and may not
change. Two flags stand outside the projects — one over projects, one over users
— and neither implies the other.

**The graph is not scoped, and must not be.** A visible issue's relations and
blockers may name issues the caller cannot fetch; an ignored counterpart is
suppressed from presentation because the user asked it to disappear, while the
same suppression is applied to historical relation snapshots in activity. The
derived `blocked` state, and the relation rules that refuse a cycle or an
inverted decomposition, are computed over the whole graph. A rule answered
over half a graph is not the rule, and readiness computed over half a graph is
a lie that sends somebody to start blocked work. What that costs is a name,
and a name is all it costs.

**Both live in the same transaction as the write they guard.** The permissions
are read from the user row inside the operation's own `BEGIN IMMEDIATE`, so they
are the permissions at the moment of the write and not at the moment the request
arrived; the rules themselves are pure functions in the domain layer, shared by
both surfaces as every other rule is.

**None of it applies to direct mode.** The CLI on a database file authorizes
nothing, because whoever can open the file can already read and write every byte
of it and a check there would be a suggestion rather than a control. That is
also the bootstrap — the first user is created there — and the recovery path
when an instance's last administrator is gone.

Cross-origin access and cross-site writes are both opt-in, because a browser
attaches stored credentials to cross-site requests of its own accord.

### Deployment

The server speaks plain HTTP and never terminates TLS, because a reverse proxy
in front of it does that better than a second TLS configuration would, and
because doing neither keeps the binary free of certificates and their renewal.

What it does carry is the two things a proxy cannot supply on its behalf. It has
to know the URL it is *published* under, because a browser names that origin and
not the listener, and because the UI is one page of relative URLs that must
resolve under whatever path the proxy mounts it on — one `<base href>`, rewritten
at startup, is the whole mechanism. And it has to be told when the proxy
terminates TLS, because a header that makes a browser refuse plain HTTP to a
host for a year is not something to infer.

The base path itself never reaches the router: the proxy strips it, which is
what the OpenAPI document's single `/` server URL says as well, and what lets
the CLI's remote mode point at a URL carrying a base path with nothing else
changed.

## 6. Directory context

`awb` knows nothing about Git, or about any version control system. What a
directory means is written down in that directory, in a small configuration
file, and everything follows from that: a project to scope to, a label to carry.
The file is found by searching upward, so putting it at the top of a checkout
gives that checkout its own scope from every subdirectory.

Not tying this to Git is what makes it work for a directory under no version
control, one holding several checkouts, or one checkout holding several scopes.

Context is resolved **on the client**, always. A resolved context is nothing but
ordinary filter values, so the server never inspects a caller's working
directory and needs no notion of one — which is also why a browser UI, having no
working directory, simply filters explicitly instead.

Because that file is meant to be committed, it may have been written by somebody
other than the person running the command. So it may set only scope. It cannot
redirect where issues are stored, claim to be you, or make you send a password
somewhere; those keys are not merely overridden but unread.

## 7. Constraints that shape the design

Some of these are as important as anything above, because they are what the
design is holding *out*.

**Deliberately absent:** full entity versioning, compliance audit logs, merge
and offline replication; sprints, burndowns and time tracking;
notifications; continuous synchronisation with external trackers; custom fields
and workflows; bulk import.

**Nothing is ever archived or purged.** Closed issues stay queryable forever.

**Two invocations against unchanged data produce byte-identical output.** Every
ordering is total, every derived array has a specified order, and this is
verified rather than assumed. Agents diff outputs; an unstable ordering would
make that useless.

**Text is stored as it arrived.** Input is validated and rejected, never
repaired: no normalisation, no case folding, no silently replaced invalid bytes.
Nothing is stored that the caller did not send, so what comes back out is what
went in.

## 8. What version 1 leaves for later

Version 1 is one person, one machine, one database file, any number of local
processes and agents against it. What it deliberately provides *toward* a
multi-user future is only what has no cost today: independently mintable
identifiers, atomic operations, schema migrations, version timestamps, a stable
API contract and an API sufficient for a write UI.

It provides no change log, tombstones, vector clocks or other merge machinery,
because carrying half a synchronisation design is worse than carrying none.

`TODO.md` lists what remains.
