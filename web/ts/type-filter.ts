// Pure issue-type selection behavior shared by the listing renderer and Node
// tests.

/** Keep this in the domain vocabulary's canonical order. */
export const issueTypeVocabulary = ["epic", "feature", "bug", "task", "chore"] as const;
export type IssueTypeValue = typeof issueTypeVocabulary[number];

/** An absent type parameter includes every issue type. */
export const defaultIssueTypes: readonly IssueTypeValue[] = issueTypeVocabulary;

const typeLabels: Record<IssueTypeValue, string> = {
  epic: "Epic",
  feature: "Feature",
  bug: "Bug",
  task: "Task",
  chore: "Chore",
};

export function issueTypeLabel(type: IssueTypeValue): string {
  return typeLabels[type];
}

function canonicalTypes(values: readonly string[]): IssueTypeValue[] {
  const selected = new Set(values);
  return issueTypeVocabulary.filter((type) => selected.has(type));
}

/**
 * Reads the route selection. The one empty type value intentionally means no
 * types, since an absent parameter means every type. Unknown-only selections
 * retain that default, matching the listing parser's treatment of stale URLs.
 */
export function selectedIssueTypes(query: URLSearchParams): IssueTypeValue[] {
  if (!query.has("type")) return [...defaultIssueTypes];
  const values = query.getAll("type");
  const selected = canonicalTypes(values);
  if (selected.length > 0 || values.includes("")) return selected;
  return [...defaultIssueTypes];
}

export function hasEmptyIssueTypeSelection(query: URLSearchParams): boolean {
  return query.has("type") && selectedIssueTypes(query).length === 0;
}

/** Writes a canonical shareable selection and resets pagination. */
export function withIssueTypes(
  query: URLSearchParams,
  types: readonly string[],
): URLSearchParams {
  const next = new URLSearchParams(query);
  const selected = canonicalTypes(types);
  next.delete("page");
  next.delete("type");
  if (selected.length === defaultIssueTypes.length) return next;
  if (selected.length === 0) next.append("type", "");
  else for (const type of selected) next.append("type", type);
  return next;
}
