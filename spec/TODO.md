# Future work

This file records concrete gaps and capabilities deliberately left outside the
current product. [Architecture](ARCHITECTURE.md) describes what exists.

## Known gap

- **Markdown autolink boundaries can differ.** Domain link extraction and web
  rendering use different Markdown implementations. At unusual boundaries —
  most visibly an escaped hand-written HTML anchor — they may disagree about
  where a bare URL ends. The issue page renders the derived link list explicitly
  so its authoritative value remains visible. A shared implementation is worth
  considering if this becomes a practical problem.

## Shared operation

- **Deployment guidance and lifecycle policy.** The server has authentication,
  authorization, reverse-proxy support, remote dumps, and safe defaults. A
  production team installation still needs operator-owned decisions about TLS,
  backup retention, monitoring, and claims held by people who leave.
- **Synchronization or replication.** Independent IDs, transactions, schema
  migrations, and version timestamps are foundations, not replication. Real
  synchronization would also require a durable change log, tombstones, merge
  semantics, and conflict policy; it should not be inferred from the current
  model.

## Deliberately deferred

- **An MCP server.** The non-interactive CLI is the agent interface. A second
  agent surface should be added only for a demonstrated workflow that the CLI
  cannot serve.
- **Bulk import from standard input.** File and remote snapshots exist, as does
  reading Markdown from standard input. A streaming issue-import protocol does
  not.
- **Workspace comments or aggregate activity.** Workspaces retain archive and
  restore events. Issue comments and change entries stay on issues rather than
  becoming a second workspace-wide discussion system.
- **Compliance history.** Issue activity supports collaboration, not immutable
  audit or reconstruction. There are no tombstones, retention rules, redaction
  policy, or event replay.
- **Sprints, burndowns, time tracking, and notifications.** Boards organize the
  fixed workflow; planning ceremonies, reporting, and notification delivery are
  separate product categories.
- **Custom fields and configurable workflows.** The fixed vocabulary is what
  makes awb teachable to an agent in a short instruction. Workspace-specific
  concepts belong in labels and Markdown.
- **Continuous synchronization with external trackers.** A one-way import may
  eventually be useful. Keeping two mutable issue systems consistent is not a
  current goal.
