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

Nothing here is designed, and version 1 deliberately carries none of it rather
than half of it.

* **Authorization.** Per-user permissions, ownership, and anything else that
  would let two authenticated users differ in what they may do. Version 1 has
  authentication and deliberately stops there. This is the piece that makes a
  shared instance meaningful, and it is the one to design first, because the
  rest depends on what it decides.

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

* **A read/write web UI.** The API is already specified and implemented as if
  one existed: complete write coverage, optimistic concurrency, paging, facet
  endpoints and a caller-identity endpoint. Shipping a read-only UI in version 1
  was a scope decision about the UI alone, so this is a change to the frontend
  and nothing else. It is the most valuable item on this list and the cheapest.

* **An MCP server.** The command line is the agent interface, and a second one
  would be a second surface to keep in step.

* **Bulk import from stdin.** Reading a description from stdin exists; reading a
  stream of issues does not.

* **Comments, audit logs and history.** Git holds the history of the work; the
  tracker holds its state.

* **Sprints, boards, burndowns, time tracking and notifications.** Planning and
  reporting are what this tool is not.

* **Custom fields and configurable workflows.** The fixed vocabulary is what
  makes the tool teachable to an agent in a paragraph. Anything a team needs
  beyond it goes in labels. This one will be asked for repeatedly and should
  keep being declined.

* **Continuous synchronisation with external trackers.** A one-way import might
  be defensible one day; keeping two systems agreeing forever is not.
