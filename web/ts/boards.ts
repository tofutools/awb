export type BoardStatus = "open" | "in_progress" | "closed";

interface BoardIssueLike {
  readonly status: BoardStatus;
  readonly assignees: readonly string[];
}

/** Board moves preserve the domain's assignment invariant. In particular, an
 * in-progress card can return to Open only when the viewer is its sole
 * assignee; releasing somebody else is never hidden in a drag gesture. */
export function legalBoardTargets(issue: BoardIssueLike, identity: string): BoardStatus[] {
  if (issue.status === "open") return ["open", "in_progress", "closed"];
  if (issue.status === "closed") return ["closed", "open"];
  const result: BoardStatus[] = ["in_progress"];
  if (identity !== "" && issue.assignees.length === 1 && issue.assignees[0] === identity) {
    result.unshift("open");
  }
  result.push("closed");
  return result;
}

export function splitBoardFilter(value: string): string[] {
  return [...new Set(value.split(/[\s,]+/).map((part) => part.trim()).filter((part) => part !== ""))];
}

