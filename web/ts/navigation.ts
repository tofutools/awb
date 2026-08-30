type IssueListing = "ready" | "issues" | "blocked";

/** Issue-listing tabs share project scope, but not each other's other filters. */
export function issueListingHref(listing: IssueListing, current: URLSearchParams): string {
  const query = new URLSearchParams();
  for (const project of current.getAll("project")) query.append("project", project);
  const suffix = query.toString();
  return `#/${listing}${suffix === "" ? "" : `?${suffix}`}`;
}

/** Active navigation follows the destination path, regardless of its filters. */
export function navigationPath(href: string): string {
  return href.replace(/^#\//, "").split("?", 1)[0];
}
