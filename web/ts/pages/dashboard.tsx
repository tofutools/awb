import { api, type IssueSummary } from "../api.js";
import { IssueBadges } from "../components/issues.js";
import { ErrorMessage, Loading, NameLink, useResource } from "../components/ui.js";

/** The API applies the caller's access and ignored-workspace scope to both
 * listings. An omitted limit returns every row, so no workspace or issue is
 * silently missing from the dashboard. */
export function DashboardPage() {
  const resource = useResource(async () => {
    const [workspaces, issues] = await Promise.all([
      api.workspaces(),
      api.issues({ status: ["in_progress"] }),
    ]);
    const byWorkspace = new Map<string, IssueSummary[]>();
    for (const issue of issues.rows) {
      const rows = byWorkspace.get(issue.workspace) ?? [];
      rows.push(issue);
      byWorkspace.set(issue.workspace, rows);
    }
    return { workspaces: workspaces.rows, byWorkspace };
  }, []);

  return (
    <div class="dashboard">
      <h1>Dashboard</h1>
      <p class="lede">Work in progress across your visible workspaces.</p>
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
                    <IssueBadges issue={issue} />
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
