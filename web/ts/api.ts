// The client for awb's HTTP API. The UI is a client of that API and gets no
// privileged access to the database, which keeps the API honest: every edit
// below is an ordinary request any other client could make.
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
export type Workspace = components["schemas"]["Workspace"];
export type WorkspaceCreate = components["schemas"]["WorkspaceCreate"];
export type WorkspaceActivity = components["schemas"]["WorkspaceActivity"];
export type WorkspacePreference = components["schemas"]["WorkspacePreference"];
export type Facet = components["schemas"]["Facet"];
export type User = components["schemas"]["User"];
export type DirectoryUser = components["schemas"]["UserDirectoryEntry"];
export type UserCreate = components["schemas"]["UserCreate"];
export type Membership = components["schemas"]["Membership"];
export type MembershipAccess = Membership["access"];
export type NavigationResults = components["schemas"]["NavigationResults"];
export type UserPatch = components["schemas"]["UserPatch"];
export type IssuePatch = components["schemas"]["IssuePatch"];
export type IssueCreate = components["schemas"]["IssueCreate"];
export type IssueMove = components["schemas"]["IssueMove"];
export type ClaimRequest = components["schemas"]["ClaimRequest"];
export type ReleaseRequest = components["schemas"]["ReleaseRequest"];
export type CloseRequest = components["schemas"]["CloseRequest"];
export type RelationRequest = components["schemas"]["RelationRequest"];
export type WorkspacePatch = components["schemas"]["WorkspacePatch"];
export type Board = components["schemas"]["Board"];
export type BoardView = components["schemas"]["BoardView"];
export type BoardViewCreate = components["schemas"]["BoardViewCreate"];
export type BoardViewPatch = components["schemas"]["BoardViewPatch"];

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
export type WorkspaceFilters = Query<"listWorkspaces">;
export type UserFilters = Query<"listUsers">;
export type FacetFilters = Query<"listLabels">;
export type ActivityFilters = Query<"listIssueActivity">;
export type BoardFilters = Query<"getBoard">;

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
  const { status, "include-closed": includeClosed, "include-archived": includeArchived, assignee, unassigned, q, ...accepted } = filters;
  return accepted;
}

/** /api/blocked fixes the status set to the two that are not closed. */
export function blockedFilters(filters: Filters): BlockedFilters {
  const { status, "include-closed": includeClosed, "include-archived": includeArchived, q, ...accepted } = filters;
  return accepted;
}

/** The facet endpoints fix the row order at value ascending. */
export function facetFilters(filters: Filters): FacetFilters {
  const { sort, q, limit, offset, ...accepted } = filters;
  return accepted;
}

/** Ready's label facets accept its ordinary selection plus the status and
 * empty-assignee constraints the listing fixes for itself. Derived readiness
 * has no facet parameter, but the shared text filter must still narrow them. */
export function readyFacetFilters(filters: Filters): FacetFilters {
  return {
    ...facetFilters(readyFilters(filters)),
    status: ["open"],
    unassigned: true,
    readiness: "ready",
  };
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

async function getPage<T>(path: string, init: RequestInit = {}): Promise<Page<T>> {
  const resp = await request(path, init);
  const rows = (await resp.json()) as T[];
  const header = resp.headers.get("X-Total-Count");
  return { rows, total: header === null ? rows.length : Number(header) };
}

const etags = new Map<string, string>();

async function getOne<T>(path: string): Promise<T> {
  const resp = await request(path);
  return entityResponse<T>(path, resp);
}

/** Decode one versioned entity and remember the version mutations must guard.
 * PATCH responses replace that version just as GET responses establish it,
 * which lets two profile forms update the same user in sequence. */
async function entityResponse<T>(path: string, resp: Response): Promise<T> {
  const etag = resp.headers.get("ETag");
  if (etag !== null) etags.set(path, etag);
  return (await resp.json()) as T;
}

function entityHeaders(path: string, headers: HeadersInit = {}): Headers {
  const result = new Headers(headers);
  const etag = etags.get(path);
  if (etag !== undefined) result.set("If-Match", etag);
  return result;
}

async function postOne<T>(path: string, body: unknown): Promise<T> {
  return getResponse<T>(await request(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }));
}

async function patchOne<T>(path: string, body: unknown): Promise<T> {
  const resp = await request(path, {
    method: "PATCH",
    headers: entityHeaders(path, { "Content-Type": "application/json" }),
    body: JSON.stringify(body),
  });
  return entityResponse<T>(path, resp);
}

async function workspaceLifecycle(key: string, action: "archive" | "restore"): Promise<Workspace> {
  const path = `api/workspaces/${encodeURIComponent(key)}`;
  const resp = await request(`${path}/${action}`, { method: "POST", headers: entityHeaders(path) });
  return entityResponse<Workspace>(path, resp);
}

async function deleteEntity<T>(path: string): Promise<T> {
  return getResponse<T>(await request(path, { method: "DELETE", headers: entityHeaders(path) }));
}

async function issueMutation<T>(id: string, suffix: string, method: "POST" | "DELETE", body?: unknown): Promise<T> {
  const issuePath = `api/issues/${encodeURIComponent(id)}`;
  const headers = entityHeaders(issuePath);
  let encoded: string | undefined;
  if (body !== undefined) {
    headers.set("Content-Type", "application/json");
    encoded = JSON.stringify(body);
  }
  return getResponse<T>(await request(`${issuePath}${suffix}`, { method, headers, body: encoded }));
}

async function deleteOne<T>(path: string): Promise<T> {
  return getResponse<T>(await request(path, { method: "DELETE" }));
}

async function getResponse<T>(resp: Response): Promise<T> {
  return (await resp.json()) as T;
}

export const api = {
  boardViews: async () => getResponse<BoardView[]>(await request("api/board-views")),
  boardView: (id: string) => getOne<BoardView>(`api/board-views/${encodeURIComponent(id)}`),
  createBoardView: (body: BoardViewCreate) => postOne<BoardView>("api/board-views", body),
  updateBoardView: (id: string, patch: BoardViewPatch) =>
    patchOne<BoardView>(`api/board-views/${encodeURIComponent(id)}`, patch),
  deleteBoardView: (id: string) => deleteEntity<BoardView>(`api/board-views/${encodeURIComponent(id)}`),
  board: async (ref: string, filters: BoardFilters = {}, signal?: AbortSignal) =>
    getResponse<Board>(await request(`api/boards/${encodeURIComponent(ref)}${toQuery(filters)}`, { signal })),
  issues: (filters: IssueFilters = {}, signal?: AbortSignal) =>
    getPage<Issue>(`api/issues${toQuery(filters)}`, { signal }),
  ready: (filters: ReadyFilters = {}, signal?: AbortSignal) =>
    getPage<Issue>(`api/ready${toQuery(filters)}`, { signal }),
  blocked: (filters: BlockedFilters = {}, signal?: AbortSignal) =>
    getPage<Issue>(`api/blocked${toQuery(filters)}`, { signal }),
  search: (filters: SearchFilters, signal?: AbortSignal) =>
    getPage<Issue>(`api/search${toQuery(filters)}`, { signal }),
  issueSuggestions: (query: string, signal?: AbortSignal) =>
    getPage<Issue>(`api/issues/suggestions${toQuery({ q: query, limit: 8 })}`, { signal }),
  navigation: async (query: string, signal?: AbortSignal) =>
    getResponse<NavigationResults>(await request(`api/navigation${toQuery({ q: query, limit: 6 })}`, { signal })),
  workspacePreferences: async () =>
    getResponse<WorkspacePreference[]>(await request("api/preferences/workspaces")),
  setWorkspaceIgnored: async (key: string, ignored: boolean) =>
    getResponse<WorkspacePreference>(await request(`api/preferences/workspaces/${encodeURIComponent(key)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ignored }),
    })),
  issue: (id: string) => getOne<Issue>(`api/issues/${encodeURIComponent(id)}`),
  createIssue: (body: IssueCreate) => postOne<Issue>("api/issues", body),
  updateIssue: (id: string, patch: IssuePatch) =>
    patchOne<Issue>(`api/issues/${encodeURIComponent(id)}`, patch),
  moveIssue: (id: string, body: IssueMove) =>
    issueMutation<Issue>(id, "/move", "POST", body),
  claimIssue: (id: string, body: ClaimRequest = { force: false }) =>
    issueMutation<Issue>(id, "/claim", "POST", body),
  releaseIssue: (id: string, body: ReleaseRequest = { force: false }) =>
    issueMutation<Issue>(id, "/release", "POST", body),
  closeIssue: (id: string, body: CloseRequest = {}) =>
    issueMutation<Issue>(id, "/close", "POST", body),
  reopenIssue: (id: string) =>
    issueMutation<Issue>(id, "/reopen", "POST"),
  addLabel: (id: string, label: string) =>
    issueMutation<Issue>(id, "/labels", "POST", { label }),
  removeLabel: (id: string, label: string) =>
    issueMutation<Issue>(id, `/labels${toQuery({ label })}`, "DELETE"),
  addRelation: (id: string, body: RelationRequest) =>
    issueMutation<Issue>(id, "/relations", "POST", body),
  removeRelation: (id: string, type: Relation["type"], other: string) =>
    issueMutation<Issue>(
      id,
      `/relations/${encodeURIComponent(type)}/${encodeURIComponent(other)}`,
      "DELETE",
    ),
  addAttachment: async (id: string, file: File) => {
    const query = toQuery({ name: file.name, "content-type": file.type });
    return getResponse<Attachment>(await request(
      `api/issues/${encodeURIComponent(id)}/attachments${query}`,
      { method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: file },
    ));
  },
  removeAttachment: (id: string, name: string) =>
    deleteOne<Attachment>(
      `api/issues/${encodeURIComponent(id)}/attachments/${encodeURIComponent(name)}`,
    ),
  activity: (id: string, filters: ActivityFilters = {}) =>
    getPage<Activity>(`api/issues/${encodeURIComponent(id)}/activity${toQuery(filters)}`),
  addComment: (id: string, body: string) =>
    postOne<Activity>(`api/issues/${encodeURIComponent(id)}/comments`, { body }),
  tree: (id: string) => getOne<IssueTree>(`api/issues/${encodeURIComponent(id)}/tree`),
  workspaces: (filters: WorkspaceFilters = {}, signal?: AbortSignal) =>
    getPage<Workspace>(`api/workspaces${toQuery(filters)}`, { signal }),
  createWorkspace: async (body: WorkspaceCreate) => {
    const resp = await request("api/workspaces", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return entityResponse<Workspace>(`api/workspaces/${encodeURIComponent(body.key)}`, resp);
  },
  workspaceMembers: (key: string, signal?: AbortSignal) =>
    getPage<Membership>(`api/workspaces/${encodeURIComponent(key)}/members`, { signal }),
  addWorkspaceMember: (key: string, user: string, access: MembershipAccess) =>
    postOne<Membership>(`api/workspaces/${encodeURIComponent(key)}/members`, { user, access }),
  setWorkspaceMember: async (key: string, user: string, access: MembershipAccess) =>
    getResponse<Membership>(await request(
      `api/workspaces/${encodeURIComponent(key)}/members/${encodeURIComponent(user)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ access }),
      },
    )),
  removeWorkspaceMember: (key: string, user: string) =>
    deleteOne<Membership>(
      `api/workspaces/${encodeURIComponent(key)}/members/${encodeURIComponent(user)}`,
    ),
  users: (filters: UserFilters = {}, signal?: AbortSignal) =>
    getPage<DirectoryUser>(`api/users${toQuery(filters)}`, { signal }),
  workspace: (key: string) => getOne<Workspace>(`api/workspaces/${encodeURIComponent(key)}`),
  updateWorkspace: (key: string, patch: WorkspacePatch) =>
    patchOne<Workspace>(`api/workspaces/${encodeURIComponent(key)}`, patch),
  archiveWorkspace: (key: string) => workspaceLifecycle(key, "archive"),
  restoreWorkspace: (key: string) => workspaceLifecycle(key, "restore"),
  workspaceActivity: (key: string) =>
    getPage<WorkspaceActivity>(`api/workspaces/${encodeURIComponent(key)}/activity`),
  labels: (filters: FacetFilters = {}, signal?: AbortSignal) =>
    getPage<Facet>(`api/labels${toQuery(filters)}`, { signal }),
  assignees: (filters: FacetFilters = {}, signal?: AbortSignal) =>
    getPage<Facet>(`api/assignees${toQuery(filters)}`, { signal }),
  identity: () => getOne<components["schemas"]["Identity"]>("api/identity"),
  user: (name: string) => getOne<User>(`api/users/${encodeURIComponent(name)}`),
  createUser: (body: UserCreate) => postOne<User>("api/users", body),
  updateUser: (name: string, patch: UserPatch) =>
    patchOne<User>(`api/users/${encodeURIComponent(name)}`, patch),
  deleteUser: (name: string) => deleteEntity<User>(`api/users/${encodeURIComponent(name)}`),
};

export { toQuery };
