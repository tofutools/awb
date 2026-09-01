import { listingFilterMaxLength } from "./listings.js";

type WorkspaceScopedView = "ready" | "issues" | "blocked" | "boards" | "workspaces";

export interface NamedDestination {
  id: string;
  label: string;
  path: string;
  keywords: string;
  workspaceScoped?: WorkspaceScopedView;
}

/** Named destinations are data rather than palette branches, so a future
 * board or view is one registry entry instead of another dialog flow. */
export const namedDestinations: readonly NamedDestination[] = [
  { id: "ready", label: "Ready", path: "#/ready", keywords: "board unassigned available", workspaceScoped: "ready" },
  { id: "issues", label: "Issues", path: "#/issues", keywords: "tickets work items", workspaceScoped: "issues" },
  { id: "blocked", label: "Blocked", path: "#/blocked", keywords: "dependencies waiting", workspaceScoped: "blocked" },
  { id: "boards", label: "Boards", path: "#/boards", keywords: "kanban scrum swimlanes views", workspaceScoped: "boards" },
  { id: "workspaces", label: "Workspaces", path: "#/workspaces", keywords: "workspaces boards", workspaceScoped: "workspaces" },
  { id: "users", label: "Users", path: "#/users", keywords: "people members accounts" },
];

/** The Issues tab supersedes the old full-text page. Keep its hashes useful by
 * carrying their selection into the tab's ID/title-capable filter. A result
 * page and relevance order belonged to the old result set, so neither can be
 * retained coherently after the migration. */
export function legacyIssueSearchHref(current: URLSearchParams): string {
  const query = new URLSearchParams(current);
  const terms = query.getAll("q").map((term) => term.trim()).filter((term) => term !== "");
  const filter = [query.get("filter")?.trim() ?? "", ...terms]
    .filter((term) => term !== "")
    .join(" ")
    .slice(0, listingFilterMaxLength);

  query.delete("q");
  query.delete("page");
  if (query.get("sort") === "relevance" || query.get("sort") === "-relevance") query.delete("sort");
  if (filter === "") query.delete("filter");
  else query.set("filter", filter);

  const suffix = query.toString();
  return `#/issues${suffix === "" ? "" : `?${suffix}`}`;
}

/** Primary tabs retain workspace scope, but not each other's other filters. */
export function workspaceScopedHref(view: WorkspaceScopedView, current: URLSearchParams): string {
  const query = new URLSearchParams();
  for (const workspace of current.getAll("workspace")) query.append("workspace", workspace);
  const suffix = query.toString();
  return `#/${view}${suffix === "" ? "" : `?${suffix}`}`;
}

/** Active navigation follows the destination path, regardless of its filters. */
export function navigationPath(href: string): string {
  return href.replace(/^#\//, "").split("?", 1)[0];
}
