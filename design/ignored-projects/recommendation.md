# Ignored projects: design recommendation

## Recommendation

Put the editor in **Settings**, not Profile, and persist the ignore set **server-side per user**.

Profile currently describes durable account identity, authorization, project access, and security. Settings is already the home for user preferences. Ignoring a project does not revoke access, so presenting it beside project access would blur a useful distinction. Unlike the current browser-local listing preferences, this choice changes every backend-driven discovery surface and must follow the user across browsers and API clients.

Use explicit **Ignore** and **Re-enable** row actions that save immediately. The wording stays clear in both states, and an ignored row remains visually present and searchable in the editor. The mock deliberately retains the existing card, typography, color, button, and responsive patterns.

## Scope and recovery model

Treat ignored projects as a second, per-user restriction within the backend scope:

1. Ordinary authorization first determines every project the caller is allowed to know about.
2. The user's ignored-project set is then subtracted for normal operations.
3. Only the dedicated current-user preferences read/write path bypasses step 2. It must still apply step 1.

This ordering is the recovery guarantee. The editor can enumerate and re-enable an ignored project, but it can never reveal a project outside ordinary authorization. Preference records should be unique by `(user, project)` and disappear when either side is deleted.

Newly authorized or newly created projects should default to active. This keeps an old preference from unexpectedly hiding a project when access is later regained; if stale rows are retained instead, that different behavior needs an explicit product decision.

## Expected behavior

| Surface | Behavior |
| --- | --- |
| Ready, Issues, Blocked, Projects, Users and tree/list tabs | Exclude ignored projects before ordering and paging. Empty states, page totals, project badges, and user/project associations are computed from the remaining scope. A URL filter naming an ignored project is not found, matching an inaccessible project. |
| Global full-text search | Search, total, sorting, paging, and project facets use the reduced scope. No result or facet names an ignored project. |
| Command palette and autocomplete | Exclude ignored issues and projects from navigation results, issue reference suggestions, relation target suggestions, assignment candidates derived from project visibility, and all other discovery lists. Named application destinations remain available. |
| Counts and facets | Calculate at query time after both authorization and ignored-project filtering. Never fetch a broad count and hide rows in the browser. Project `active_issues`, issue totals, label/assignee counts, pagination totals, and empty states must agree. |
| Direct entity URLs and mutations | Treat an ignored project and its issues as not found through ordinary endpoints. Mutations may not smuggle an ignored project or issue through a project key, issue reference, relation target, parent, or other candidate field. Re-enabling via the preferences endpoint restores ordinary access. |
| Preferences page/API | Enumerate every otherwise-authorized project, including ignored ones, with clear state and an immediate reversal action. Search here spans both states. Project administrators still see only projects allowed by ordinary authorization semantics, but the ignore filter itself is bypassed. |
| Authorization changes | Withdrawing access removes the project from the editor as well as normal surfaces. Restoring access makes it active by default under the recommended cascading-record model. |

## Graph behavior

Readiness and cycle rules must still be computed over the complete graph, as they are for inaccessible projects today. Ignoring a blocking project must not incorrectly make visible work ready. At the presentation boundary, however, the clarified "relations/connections" rule means ignored issue IDs and relation rows should not be offered as navigable discoveries.

The safest UI is to preserve the visible issue's derived **blocked** state while replacing suppressed blocker detail with neutral text such as “Blocked by hidden work.” This avoids a false readiness answer without surfacing the ignored project. This exact copy and whether to reveal the suppressed blocker ID are the one product choice that cannot be inferred cleanly from current behavior: authorization currently allows blocker/relation names from otherwise invisible projects, while the new preference asks ignored projects to behave as nonexistent.

## Direct CLI and remote parity

Remote CLI commands naturally receive the server-side filter for their authenticated user. For direct mode, use the configured AWB identity as the preference identity when it names a stored user, even though direct mode still bypasses authorization. That gives the same user the same normal results in direct and remote operation without pretending the preference is a security boundary.

If direct mode's configured identity does not name a stored user (including a database with no users), there is no per-user ignore set to apply, so direct mode remains unrestricted. This fallback should be documented and tested. A dedicated preference command would be useful only if CLI management is in scope; it should not be invented merely as an implementation convenience because the approved brief names the preferences API and Settings UI as the recovery path.

## Mock notes

- `mock.html` is interactive: filtering includes active and ignored rows, and each row can switch between Ignore and Re-enable.
- `desktop.png` shows the complete management state with one ignored project.
- `narrow.png` shows the same reversible state at a phone-sized viewport.
- The existing application stylesheet is loaded unchanged; `mock.css` contains only the proposed design additions.

## Approval questions

1. Approve Settings, server-side per-user persistence, explicit immediate row actions, and the authorization-first/preferences-bypass recovery model?
2. For a visible issue blocked by ignored work, approve preserving the blocked state while replacing the suppressed connection with “Blocked by hidden work”?
3. Approve direct CLI preference lookup by configured identity when that identity is a stored user, with unrestricted behavior when it is not?

No production implementation or pull request should begin until these mock and behavior choices are approved.
