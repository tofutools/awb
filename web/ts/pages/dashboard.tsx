import { api, type IssueSummary } from "../api.js";
import { IssueBadges } from "../components/issues.js";
import { DynamicFilterRow } from "../components/listing-filters.js";
import { ErrorMessage, Loading, NameLink, useResource } from "../components/ui.js";
import { rankCountedFilterSuggestions } from "../listings.js";
import type { Route } from "../routing/route.js";

/** The API applies the caller's access and ignored-workspace scope to both
 * listings. An omitted limit returns every row, so no workspace or issue is
 * silently missing from the dashboard. */
export function DashboardPage({ route }: { route: Route }) {
  const assignees = route.query.getAll("assignee");
  const resource = useResource(async () => {
    const [workspaces, issues] = await Promise.all([
      api.workspaces(),
      api.issues({
        status: ["in_progress"],
        ...(assignees.length ? { assignee: assignees } : {}),
      }),
    ]);
    const byWorkspace = new Map<string, IssueSummary[]>();
    for (const issue of issues.rows) {
      const rows = byWorkspace.get(issue.workspace) ?? [];
      rows.push(issue);
      byWorkspace.set(issue.workspace, rows);
    }
    return { workspaces: workspaces.rows, byWorkspace };
  }, [route.query.toString()]);

  return (
    <div class="dashboard">
      <h1>Dashboard</h1>
      <p class="lede">Work in progress across your visible workspaces.</p>
      <DynamicFilterRow
        route={route}
        title="assignees"
        name="assignee"
        selected={assignees}
        choices={[]}
        loadChoices={async () => {
          const page = await api.assignees({ status: ["in_progress"] });
          return rankCountedFilterSuggestions(page.rows).map((assignee) => ({
            value: assignee.value,
            label: `@${assignee.value}`,
            detail: `${assignee.count} issue${assignee.count === 1 ? "" : "s"}`,
          }));
        }}
      />
      <ErrorMessage error={resource.error} />
      {!resource.data && !resource.error && <Loading />}
      {resource.data?.workspaces.length === 0 && (
        <p class="muted">No visible workspaces.</p>
      )}
      {resource.data?.workspaces.map((workspace) => {
        const issues = resource.data!.byWorkspace.get(workspace.key) ?? [];
        return (
          <section class="dashboard-workspace" key={workspace.key}>
            <h2>
              <a href={`#/workspaces/${encodeURIComponent(workspace.key)}`}>
                {workspace.name}
              </a>
              <span class="dashboard-workspace-key">{workspace.key}</span>
              <span class="dashboard-count">{issues.length}</span>
            </h2>
            {issues.length ? (
              <ul class="dashboard-issues">
                {issues.map((issue) => (
                  <li key={issue.id}>
                    <NameLink href={`#/issues/${issue.id}`} id={issue.id} title={issue.title} />
                    {issue.pull_request_url && (
                      <a class="dashboard-pr" href={issue.pull_request_url} target="_blank" rel="noopener noreferrer">
                        {issue.pull_request_url}
                      </a>
                    )}
                    <IssueBadges issue={issue} showStatus={false} />
                  </li>
                ))}
              </ul>
            ) : (
              <p class="dashboard-empty">No issues in progress.</p>
            )}
          </section>
        );
      })}
    </div>
  );
}
