export type BoardStatus = "backlog" | "open" | "in_progress" | "closed";

/** Every workflow column is reachable. Moving to Open may clear assignees, so
 * the UI confirms that consequence before it sends the explicit move. */
export function legalBoardTargets(): BoardStatus[] {
  return ["backlog", "open", "in_progress", "closed"];
}

export function splitBoardFilter(value: string): string[] {
  return [...new Set(value.split(/[\s,]+/).map((part) => part.trim()).filter((part) => part !== ""))];
}
