# Shareable board views: architecture and design recommendation

## Recommendation

Add **Boards** as a primary tab beside Issues and implement one configurable
board, not separate Kanban and Scrum products. A board is another way to work
with issues, but its saved-view lifecycle, stable share URL and project
swimlanes make it a destination rather than a temporary Issues-table display
mode. Keeping Issues unchanged also preserves its dense, sortable and pageable
workflow.

The board has one swimlane per project and the three workflow columns already
enforced by the domain: **Open**, **In progress** and **Closed**. There is no
sprint, iteration or backlog model in awb, so a separate “Scrum” mode would be
only a second label for the same data. Project, label, assignee and maximum
priority filters cover focused delivery and team boards without inventing
another workflow vocabulary.

`#/boards` is the virtual default board: every otherwise-visible, non-ignored
project, with no saved configuration. A user can save it as a named view.
`#/boards/{id}` is a stable saved-view URL.

## Decisions

### Navigation: a Boards tab

Boards is a primary tab and command-palette destination. The Issues table is
optimized for scanning, exact sorting and arbitrary paging; a board adds a
view selector, share state, swimlanes, column paging and issue transitions.
Putting both behind an Issues mode switch would make unrelated route state and
controls compete and make a shared board URL look like a transient table
preference.

Project scope still travels between the primary Ready, Issues, Blocked,
Projects and Boards destinations. An Issues filter with `project=awb` therefore
opens the default board constrained to `awb`; a saved view has its own stored
project selection and does not inherit unrelated route filters.

### One configurable board

One model covers both continuous-flow and team-planning uses:

- project swimlanes are the grouping;
- status is the fixed column model;
- selected projects, labels, assignees and maximum priority are persisted;
- multiple named views provide the different working contexts.

A true Scrum board would need explicit sprint and backlog concepts. Those are
not present in the issue model and should not be simulated with UI-only state.

### Columns and moves

Status is not directly editable in awb: transitions maintain the invariant
between status and assignment. Board moves call those same transitions rather
than adding a second status-update path.

| Move | Operation | Guard |
| --- | --- | --- |
| Open → In progress | Claim as the authenticated identity | The existing claim refusal applies to blocked work. |
| Open/In progress → Closed | Close without a reason | The UI asks for confirmation before committing the drop. |
| In progress → Open | Release the authenticated identity | Offered only when that identity is the sole assignee; a board cannot silently release other people. |
| Closed → Open | Reopen | Clears the historical assignment exactly as the existing transition does. |
| Closed → In progress | Not offered | Reopen, then claim; this avoids hiding a forced reclaim inside one gesture. |

Desktop cards are draggable. Every card also has a labelled Move control,
which is the keyboard, assistive-technology and narrow-screen path. A failed
transition leaves the card in its source column and shows the server's reason.

### Saved-view lifecycle

- A view has a random stable ID, name, owner, shared flag, filters, and
  strictly increasing timestamps used as its ETag.
- The view selector lists the current user's own views. Shared views are
  deliberately unlisted: possession of the stable URL is the discovery
  mechanism, without creating a global directory of other users' workflow.
- A private view is visible only to its owner. A shared view is readable by
  anyone who can reach the server, but its board contents are always evaluated
  as the viewer.
- Only the owner can rename, change filters, toggle sharing or delete a view.
  Project and user administrators do not implicitly own personal views.
- A viewer of somebody else's shared view gets **Duplicate**, which creates a
  new private view owned by the viewer from only the configuration they are
  allowed to see. They never edit the source.
- Direct mode remains unrestricted, like every other direct-mode operation.
  On an unauthenticated server the fixed server identity owns created views;
  there is no meaningful per-request ownership boundary, matching the existing
  open-server model.

### Authorization and ignored projects

View metadata and board contents have separate safety jobs.

Creating or editing a view validates every selected project under ordinary
authorization. An owner managing their own configuration may see their ignored
projects, using the same authorization-first recovery boundary as Settings;
this allows an ignored selection to be removed. It does not make the project
appear on the board.

Opening either the default board or a saved board always uses the viewer's
normal transaction scope. It therefore removes both unauthorized and ignored
projects before lane counts, issue counts, ordering and paging. A shared view
cannot opt out. When stored selected projects are omitted, the board displays
the generic notice “Some lanes are hidden by your access or ignored-project
settings”; it never names an unavailable project or distinguishes the reason.

This means a link shared by Alice and Bob can intentionally render different
lane sets. That is preferable to bypassing Bob's preference or leaking a
project Bob cannot know exists.

### Persistence and API

Add `board_views` and normalized `board_view_projects`,
`board_view_labels` and `board_view_assignees` tables. Normalized filter rows
keep validation and cascade behavior explicit; the board row stores only the
scalar name, owner, sharing, project-scope and priority fields. A user deletion
trigger removes views they owned, while the owner column remains usable by a
database with no user rows.

The backend interface exposes view CRUD/list operations and one board read.
Both local and remote implementations use it. The HTTP API is specified first
in `openapi.yaml`:

- `GET/POST /api/board-views`
- `GET/PATCH/DELETE /api/board-views/{id}`
- `GET /api/boards/{ref}`, where `ref` is `default` or a saved-view ID

View mutations use ETags. The board read accepts lane and card offsets/limits,
plus an optional project/status narrowing used by “Load more” in one column.
It returns lane metadata, per-column unpaged totals and bounded issue arrays.

### Large-data loading

The browser never loads the whole issue collection. The initial request asks
for ten project lanes and eight cards per status column. Lane counts and column
totals are computed after authorization and saved filters in the backend.

“Load more projects” pages lanes. “Load more” within a column repeats the board
request narrowed to that project and status with the next card offset. The
sort is the existing stable priority/creation/ID order, so unchanged data pages
consistently. This is offset pagination, matching the current backend; moving a
card can shift later pages, so the affected lane is reloaded after a mutation
instead of trying to reconcile stale offsets in the browser.

## Responsive interaction

Desktop lanes show three equal columns and draggable cards. At narrow widths a
lane becomes a horizontally scrollable, snap-aligned sequence of columns, each
roughly the viewport width. The column heading and count remain visible, cards
use the explicit Move control, and project lanes stay vertically stacked.
There is no miniature three-column layout with unreadable cards.

Each swimlane is also a disclosure region. Its header keeps the project and
issue count visible while its status columns are folded, and the browser
remembers that presentation choice separately for each board. Folding is not
part of the saved or shared view, so one viewer's workspace does not change
another's.

The view/configuration controls wrap into full-width rows. Shared-state and
hidden-lane notices remain text, not icon-only affordances. Empty states exist
at three levels: no authorized projects, no projects matching the saved view,
and an empty status column.

## Mock artifacts

- `mock.html` is an interactive desktop/narrow proposal. The view picker,
  project swimlanes, responsive columns and card Move control are represented.
- `desktop.png` and `narrow.png` are captures at reviewable viewports.
