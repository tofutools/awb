# Project creation and lifecycle

## Recommendation

Use one reversible lifecycle state: **archived**. Do not add a separate
"locked" state.

Both proposed states must reject work mutations. The only meaningful extra
property of retirement is removal from everyday discovery and new-work target
pickers. An archived project can still be opened through its stable URL and an
explicit Archived view, so it covers temporary freezes and long-term retention
without a second state whose intended duration users would have to guess.

The project state is therefore `active` or `archived`. Archiving and restoring
are project-administrator operations, guarded by the project's ETag and applied
in the same `BEGIN IMMEDIATE` transaction as the state change and audit entry.
Repeating a transition to the current state succeeds without creating another
audit entry. A stale ETag fails with `412`.

## Exact semantics

| Capability | Active | Archived |
| --- | --- | --- |
| Open project, issue, activity and attachment URLs | Yes | Yes |
| List/search/ready/blocked/command palette | Included | Omitted by default |
| Explicit Archived projects view | No | Included |
| Create issues or use as a new relation target | Yes | No |
| Edit, assign, transition, comment, label, attach, or delete issues | Yes | No |
| Add or remove a relation touching the project | Yes | No |
| Change project name or description | Yes | No |
| Read membership and historical relations | Yes | Yes |
| Grant/revoke membership | Admin | Admin; history access remains manageable |
| Change personal ignored-project preference | Yes | Yes |
| Archive or restore | Project administrator | Project administrator |
| Dump/restore | State and audit preserved | State and audit preserved |

Archived projects remain authorized exactly as before: members can read them,
project administrators can read all of them, and non-members learn nothing.
Existing cross-project relations remain readable from both ends. Because a
relation mutation changes both issues' history, adding or removing one is
rejected when either endpoint belongs to an archived project.

The active project key remains reserved. Creating the same key conflicts even
when its project is archived. Keys are never renamed. Restoring returns the
same project, URLs, issues, memberships, preferences, attachment metadata and
blob references; it does not recreate or rewrite any child record.

Personal ignore is independent from lifecycle. An ignored archived project is
recovered through the existing preference editor before it appears in the
Archived view. Restoring preserves the preference: a user who ignored it
before archival still ignores it afterward. A project administrator may still
restore a known key through the lifecycle operation, which bypasses only their
own presentation preference and never authorization.

## API and CLI shape

- `Project` gains `state`, `archived_at`, and `archived_by`.
- `GET /api/projects?state=active|archived|all` defaults to `active`.
- `POST /api/projects/{key}/archive` and `/restore` take no body, accept
  `If-Match`, and return the project with a fresh ETag.
- `GET /api/projects/{key}/activity` returns append-only archive/restore entries.
- `awb project archive <key>` and `awb project restore <key>` provide parity.
- `awb project list --archived` selects archived projects; `--all` selects both.

Hard deletion stays available only as the existing CLI/API compatibility
surface and is deliberately not exposed in the browser. It is outside this
design; a later change can deprecate it separately.

## Interaction design

The Projects page has Active and Archived tabs. A project administrator sees a
prominent **New project** button; the creation panel explains that the key is
permanent and previews its issue prefix. Validation errors stay next to the
field and server conflicts are announced in an `aria-live` region.

An active project's Lifecycle card explains the consequences before showing
an **Archive project** action. Confirmation requires the stable project key,
which prevents an accidental click without inventing a destructive-looking
red dialog for a reversible action. The archived detail page has a persistent
read-only banner and a **Restore project** action. Narrow layouts stack the
card and action while keeping the state message before the controls in reading
order.

Lifecycle history appears on the project page with actor and time. Successful
creation, archive, and restore operations navigate to the affected detail page
and announce the result. `409`, `412`, and authorization failures preserve the
form and display the server's exact error.
