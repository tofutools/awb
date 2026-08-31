type ProjectScopedView = "ready" | "issues" | "blocked" | "projects";

export interface NamedDestination {
  id: string;
  label: string;
  path: string;
  keywords: string;
  projectScoped?: ProjectScopedView;
}

/** Named destinations are data rather than palette branches, so a future
 * board or view is one registry entry instead of another dialog flow. */
export const namedDestinations: readonly NamedDestination[] = [
  { id: "ready", label: "Ready", path: "#/ready", keywords: "board unassigned available", projectScoped: "ready" },
  { id: "issues", label: "Issues", path: "#/issues", keywords: "tickets work items", projectScoped: "issues" },
  { id: "blocked", label: "Blocked", path: "#/blocked", keywords: "dependencies waiting", projectScoped: "blocked" },
  { id: "projects", label: "Projects", path: "#/projects", keywords: "boards workspaces", projectScoped: "projects" },
  { id: "users", label: "Users", path: "#/users", keywords: "people members accounts" },
  { id: "search", label: "Issue search", path: "#/search", keywords: "full text find tickets" },
];

/** Primary tabs retain project scope, but not each other's other filters. */
export function projectScopedHref(view: ProjectScopedView, current: URLSearchParams): string {
  const query = new URLSearchParams();
  for (const project of current.getAll("project")) query.append("project", project);
  const suffix = query.toString();
  return `#/${view}${suffix === "" ? "" : `?${suffix}`}`;
}

/** Active navigation follows the destination path, regardless of its filters. */
export function navigationPath(href: string): string {
  return href.replace(/^#\//, "").split("?", 1)[0];
}
