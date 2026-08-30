// Pure listing behavior shared by the DOM renderer and its Node tests.

import type { Issue, Project } from "./api.js";

export type SortDirection = "asc" | "desc";

export interface SortState {
  key: string;
  direction: SortDirection;
  explicit: boolean;
}

/** Empty applicable facet groups advertise themselves; null means omitted. */
export function emptyFacetLabel(values: readonly unknown[] | null): string | null {
  return values !== null && values.length === 0 ? "none" : null;
}

/** withClosedIssues returns a route query widened to all statuses or narrowed
 * back to the default non-closed set, without disturbing the listing's other
 * filters and presentation choices. */
export function withClosedIssues(query: URLSearchParams, include: boolean): URLSearchParams {
  const next = new URLSearchParams(query);
  if (include) next.set("include-closed", "true");
  else next.delete("include-closed");
  return next;
}

/** sortState reads a signed sort key and falls back to the view's natural order. */
export function sortState(
  value: string | null,
  allowed: readonly string[],
  defaultKey: string,
  defaultDirection: SortDirection = "asc",
): SortState {
  if (value !== null) {
    const direction: SortDirection = value.startsWith("-") ? "desc" : "asc";
    const key = value.startsWith("-") ? value.slice(1) : value;
    if (allowed.includes(key)) return { key, direction, explicit: true };
  }
  return { key: defaultKey, direction: defaultDirection, explicit: false };
}

/**
 * nextSortValue implements the header cycle: ascending, descending, natural.
 * A default ascending column therefore goes straight to descending when first
 * clicked; removing the explicit descending value returns to the same natural
 * ascending order.
 */
export function nextSortValue(
  value: string | null,
  column: string,
  allowed: readonly string[],
  defaultKey: string,
  defaultDirection: SortDirection = "asc",
): string | null {
  const current = sortState(value, allowed, defaultKey, defaultDirection);
  if (current.key !== column) return column;
  if (current.direction === "asc") return `-${column}`;
  return null;
}

function words(value: string): string[] {
  return value.toLocaleLowerCase().split(/\s+/).filter((word) => word !== "");
}

function containsEvery(haystack: string, query: string): boolean {
  const folded = haystack.toLocaleLowerCase();
  return words(query).every((word) => folded.includes(word));
}

/** filterIssues narrows only on values represented in an issue listing. */
export function filterIssues(issues: Issue[], query: string): Issue[] {
  if (query.trim() === "") return issues;
  return issues.filter((issue) => containsEvery([
    issue.id,
    issue.project,
    issue.title,
    issue.type,
    issue.status,
    `P${issue.priority}`,
    issue.assignee,
    ...issue.labels,
    ...issue.blockers,
  ].join(" "), query));
}

/** filterProjects matches the three descriptive fields visible in the table. */
export function filterProjects(projects: Project[], query: string): Project[] {
  if (query.trim() === "") return projects;
  return projects.filter((project) => containsEvery(
    `${project.key} ${project.name} ${project.description}`,
    query,
  ));
}

type Comparable = string | number | null;

function compareValues(a: Comparable, b: Comparable, direction: SortDirection): number {
  const aBlank = a === null || a === "";
  const bBlank = b === null || b === "";
  if (aBlank || bBlank) return aBlank === bBlank ? 0 : aBlank ? 1 : -1;

  const comparison = typeof a === "number" && typeof b === "number"
    ? a - b
    : String(a).localeCompare(String(b), undefined, { sensitivity: "base", numeric: true });
  return direction === "desc" ? -comparison : comparison;
}

/** sortIssues orders a copy by every column the responsive issue tables expose. */
export function sortIssues(issues: Issue[], state: SortState): Issue[] {
  if (state.key === "relevance") return issues;

  const value = (issue: Issue): Comparable => {
    switch (state.key) {
      case "id": return issue.id;
      case "project": return issue.project;
      case "priority": return issue.priority;
      case "status": return issue.status;
      case "assignee": return issue.assignee;
      case "created": return issue.created_at;
      case "updated": return issue.updated_at;
      case "type": return issue.type;
      case "blockers": return issue.blockers.join(" ");
      default: return null;
    }
  };

  return issues.slice().sort((a, b) => {
    const compared = compareValues(value(a), value(b), state.direction);
    if (compared !== 0) return compared;
    if (state.key === "priority") {
      const created = compareValues(a.created_at, b.created_at, "asc");
      if (created !== 0) return created;
    }
    return compareValues(a.id, b.id, "asc");
  });
}

/** sortProjects orders a copy by every project table column. */
export function sortProjects(projects: Project[], state: SortState): Project[] {
  const value = (project: Project): Comparable => {
    switch (state.key) {
      case "key": return project.key;
      case "name": return project.name;
      case "active": return project.active_issues;
      case "updated": return project.updated_at;
      default: return null;
    }
  };

  return projects.slice().sort((a, b) => {
    const compared = compareValues(value(a), value(b), state.direction);
    return compared !== 0 ? compared : compareValues(a.key, b.key, "asc");
  });
}
