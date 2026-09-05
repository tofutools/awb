// Pure status-selection behavior shared by the DOM renderer, the forthcoming
// component renderer, and Node tests.

/** Keep this in the domain vocabulary's canonical order. */
export const issueStatusVocabulary = ["backlog", "open", "in_progress", "closed"] as const;
export type IssueStatusValue = typeof issueStatusVocabulary[number];

/** An absent status parameter asks the backend for its ordinary live-work set. */
export const defaultIssueStatuses: readonly IssueStatusValue[] = ["backlog", "open", "in_progress"];

const statusLabels: Record<IssueStatusValue, string> = {
  backlog: "Backlog",
  open: "Open",
  in_progress: "In progress",
  closed: "Closed",
};

export function issueStatusLabel(status: IssueStatusValue): string {
  return statusLabels[status];
}

function canonicalStatuses(values: readonly string[]): IssueStatusValue[] {
  const selected = new Set(values);
  return issueStatusVocabulary.filter((status) => selected.has(status));
}

/**
 * Reads the route selection. The one empty status value is an intentional
 * marker for selecting nothing; without the marker, no status parameters mean
 * the backend default. include-closed remains readable for old shared links.
 */
export function selectedIssueStatuses(query: URLSearchParams): IssueStatusValue[] {
  if (!query.has("status")) {
    return query.get("include-closed") === "true"
      ? [...issueStatusVocabulary]
      : [...defaultIssueStatuses];
  }
  return canonicalStatuses(query.getAll("status"));
}

export function hasEmptyStatusSelection(query: URLSearchParams): boolean {
  return query.has("status") && selectedIssueStatuses(query).length === 0;
}

function isDefaultSelection(statuses: readonly IssueStatusValue[]): boolean {
  return statuses.length === defaultIssueStatuses.length
    && defaultIssueStatuses.every((status, index) => statuses[index] === status);
}

/**
 * Writes a canonical shareable selection and resets pagination. An empty
 * selection uses status= so it stays distinct from the parameter-free default.
 */
export function withIssueStatuses(
  query: URLSearchParams,
  statuses: readonly string[],
): URLSearchParams {
  const next = new URLSearchParams(query);
  const selected = canonicalStatuses(statuses);
  next.delete("page");
  next.delete("status");
  next.delete("include-closed");
  if (isDefaultSelection(selected)) return next;
  if (selected.length === 0) next.append("status", "");
  else for (const status of selected) next.append("status", status);
  return next;
}
