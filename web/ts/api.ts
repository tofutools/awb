// The client for awb's HTTP API. The UI is a client of that API and gets no
// privileged access to the database, which is what keeps the API honest:
// making this UI writable later is a change to the UI alone.
//
// Every shape here comes from api-types.ts, which openapi-typescript generates
// from openapi.yaml and which is never edited by hand. Nothing in this file
// restates what an endpoint returns or which parameters it takes.
//
// Each listing takes its own filter type, so passing an object literal with a
// filter that endpoint does not accept is a compile error. TypeScript compares
// structurally, though, and only checks a literal for excess properties: a
// wider value assigned to a narrower parameter type passes. Narrowing a value
// is therefore a runtime job, which is what readyFilters is for.
//
// The generated file is a sibling rather than the two of them sitting under
// web/ts/api/, because these sources compile to web/static/ and are served
// from the paths they land on: /api/ belongs to the JSON API on the wire, and
// anything the UI loaded from under it would reach the API server instead.

import type { components, operations } from "./api-types.js";

/** The one issue shape both surfaces return. */
export type Issue = components["schemas"]["Issue"];
export type Relation = components["schemas"]["Relation"];
export type Link = components["schemas"]["Link"];
export type Attachment = components["schemas"]["Attachment"];
export type Activity = components["schemas"]["Activity"];
export type IssueTree = components["schemas"]["IssueTree"];
export type Project = components["schemas"]["Project"];
export type Facet = components["schemas"]["Facet"];

/**
 * The query parameters of one operation, named exactly as the CLI flags are.
 * Each listing takes its own set: the endpoints that fix a status set or an
 * assignee filter for themselves declare neither, and the facet endpoints
 * declare no sort.
 */
type Query<K extends keyof operations> = NonNullable<operations[K]["parameters"]["query"]>;

export type IssueFilters = Query<"listIssues">;
export type ReadyFilters = Query<"listReady">;
export type BlockedFilters = Query<"listBlocked">;
export type SearchFilters = Query<"searchIssues">;
export type FacetFilters = Query<"listLabels">;
export type ActivityFilters = Query<"listIssueActivity">;

/** Every filter any listing takes, which is what a route can carry. */
export type Filters = IssueFilters & Partial<SearchFilters>;

// The three narrowings below drop the filters an endpoint does not accept,
// which it refuses rather than ignores. One route's query string serves every
// listing on the page, so what a listing does not take has to be removed
// before it is asked, not on the way back from a 400.

/**
 * /api/ready lists the issues that are open, unblocked and unassigned, so it
 * fixes the status set and the assignee filter for itself and declares
 * neither.
 */
export function readyFilters(filters: Filters): ReadyFilters {
  const { status, "include-closed": includeClosed, assignee, unassigned, q, ...accepted } = filters;
  return accepted;
}

/** /api/blocked fixes the status set to the two that are not closed. */
export function blockedFilters(filters: Filters): BlockedFilters {
  const { status, "include-closed": includeClosed, q, ...accepted } = filters;
  return accepted;
}

/** The facet endpoints fix the row order at value ascending. */
export function facetFilters(filters: Filters): FacetFilters {
  const { sort, q, ...accepted } = filters;
  return accepted;
}

/** A listing together with the unpaged total X-Total-Count carries. */
export interface Page<T> {
  rows: T[];
  total: number;
}

/** A failure carrying the server's own human-readable message. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

function toQuery(filters: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === "") continue;
    // A repeatable filter is repeated rather than comma-separated, exactly as
    // on the command line.
    if (Array.isArray(value)) {
      for (const item of value) params.append(key, String(item));
    } else {
      params.set(key, String(value));
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  const resp = await fetch(path, { ...init, headers });
  if (!resp.ok) {
    let message = resp.statusText;
    try {
      const body = (await resp.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // A response with no JSON body keeps its status text.
    }
    throw new ApiError(resp.status, message);
  }
  return resp;
}

async function getPage<T>(path: string): Promise<Page<T>> {
  const resp = await request(path);
  const rows = (await resp.json()) as T[];
  const header = resp.headers.get("X-Total-Count");
  return { rows, total: header === null ? rows.length : Number(header) };
}

async function getOne<T>(path: string): Promise<T> {
  const resp = await request(path);
  return (await resp.json()) as T;
}

async function postOne<T>(path: string, body: unknown): Promise<T> {
  return getResponse<T>(await request(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }));
}

async function getResponse<T>(resp: Response): Promise<T> {
  return (await resp.json()) as T;
}

export const api = {
  issues: (filters: IssueFilters = {}) => getPage<Issue>(`api/issues${toQuery(filters)}`),
  ready: (filters: ReadyFilters = {}) => getPage<Issue>(`api/ready${toQuery(filters)}`),
  blocked: (filters: BlockedFilters = {}) => getPage<Issue>(`api/blocked${toQuery(filters)}`),
  search: (filters: SearchFilters) => getPage<Issue>(`api/search${toQuery(filters)}`),
  issue: (id: string) => getOne<Issue>(`api/issues/${encodeURIComponent(id)}`),
  activity: (id: string, filters: ActivityFilters = {}) =>
    getPage<Activity>(`api/issues/${encodeURIComponent(id)}/activity${toQuery(filters)}`),
  addComment: (id: string, body: string) =>
    postOne<Activity>(`api/issues/${encodeURIComponent(id)}/comments`, { body }),
  tree: (id: string) => getOne<IssueTree>(`api/issues/${encodeURIComponent(id)}/tree`),
  projects: () => getPage<Project>("api/projects"),
  labels: (filters: FacetFilters = {}) => getPage<Facet>(`api/labels${toQuery(filters)}`),
  assignees: (filters: FacetFilters = {}) => getPage<Facet>(`api/assignees${toQuery(filters)}`),
  identity: () => getOne<components["schemas"]["Identity"]>("api/identity"),
};

export { toQuery };
