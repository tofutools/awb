type ProjectScopedView = "ready" | "issues" | "blocked" | "projects";

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
