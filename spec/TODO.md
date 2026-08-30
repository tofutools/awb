# TODO

What is left after version 1. `ARCHITECTURE.md` describes what exists.

## Outstanding in version 1

These are loose ends in what has shipped, not new scope.

* **Autolink boundaries can differ between the two Markdown implementations.**
  The derived link list and the web UI's rendering use different libraries, so
  at the margin they can disagree about where a bare URL ends — most visibly
  where a hand-written HTML anchor is escaped to text and then linkified by one
  and not the other. The UI renders the derived list explicitly alongside the
  prose so the authoritative answer is always on screen, but the divergence is
  real and worth closing if it ever bites.

## Version 2 — multi-user and multi-machine

Authorization is done: users are rows with their password hashes, membership of
a project is what a user may work in and all they can see, and two flags stand
outside the projects for managing projects and managing users. What is left is
below.

* **A shared instance**, for a team or an open source project, rather than a
  local surface that happens to speak HTTP. That is a deployment and operations
  question as much as a code one: TLS termination, backup, and what happens to a
  claim when the person holding it leaves.

* **Synchronisation or replication between databases.** The foundations that
  exist are independently mintable identifiers, atomic operations, schema
  migrations and version timestamps. The machinery that does not exist — and
  should not be added speculatively — is a change log, tombstones and any form
  of vector clock or merge resolution.

## Deferred features

Each of these was considered and left out. They are recorded here so the
reasoning is not lost and so they are not re-litigated by accident.

* **A fully read/write web UI.** The API already has complete write coverage,
  optimistic concurrency, paging, facet endpoints and a caller-identity
  endpoint. The bundled UI can post issue comments, but editing issue state is
  still a command-line operation.

* **An MCP server.** The command line is the agent interface, and a second one
  would be a second surface to keep in step.

* **Bulk import from stdin.** Reading a description from stdin exists; reading a
  stream of issues does not.

* **Project activity and comments.** Issues have comments and append-only change
  events. A project detail page could aggregate its issues' activity and, if a
  concrete need emerges, hold project-scoped comments.

* **Full history and compliance audit logs.** The issue activity stream is a
  work log, not reconstructable version history: hard deletion removes it and
  there are no tombstones, retention rules or redaction policy.

* **Sprints, boards, burndowns, time tracking and notifications.** Planning and
  reporting are what this tool is not.

* **Custom fields and configurable workflows.** The fixed vocabulary is what
  makes the tool teachable to an agent in a paragraph. Anything a team needs
  beyond it goes in labels. This one will be asked for repeatedly and should
  keep being declined.

* **Continuous synchronisation with external trackers.** A one-way import might
  be defensible one day; keeping two systems agreeing forever is not.
