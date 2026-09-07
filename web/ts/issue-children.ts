import type { IssueFilters } from "./api.js";

/** Child listings include closed rows only when the issue-page preference says
 * so; filtering them at the API keeps long-running parents bounded. */
export function childIssueFilters(
  parent: string,
  showClosed: boolean,
): IssueFilters {
  return {
    parent,
    "include-closed": showClosed,
    "include-archived": true,
  };
}
