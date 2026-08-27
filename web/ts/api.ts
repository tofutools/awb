// The client for awb's HTTP API. The UI is a client of that API and gets no
// privileged access to the database, which is what keeps the API honest:
// making this UI writable later is a change to the UI alone.

/** The one issue shape both surfaces return. */
export interface Issue {
  id: string;
  project: string;
  title: string;
  description: string;
  type: "epic" | "feature" | "bug" | "task" | "chore";
  status: "open" | "in_progress" | "closed";
  priority: number;
  labels: string[];
  assignee: string;
  close_reason: string;
  created_at: string;
  updated_at: string;
  blocked: boolean;
  blockers: string[];
  relations: Relation[];
  links: Link[];
}

export interface Relation {
  type: "blocked-by" | "has-parent" | "discovered-from" | "related";
  other: string;
  direction: "out" | "in";
}

export interface Link {
  text: string;
  url: string;
}

export interface IssueTree extends Issue {
  children: IssueTree[];
}

export interface Project {
  key: string;
  name: string;
  description: string;
  active_issues: number;
  created_at: string;
  updated_at: string;
}

export interface Facet {
  value: string;
  count: number;
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

/**
 * Filters, named exactly as the query parameters are, which are in turn named
 * exactly as the CLI flags are.
 */
export interface Filters {
  status?: string[];
  "include-closed"?: boolean;
  type?: string[];
  priority?: number[];
  "priority-max"?: number;
  label?: string[];
  assignee?: string[];
  unassigned?: boolean;
  project?: string[];
  parent?: string;
  sort?: string;
  limit?: number;
  offset?: number;
  q?: string[];
}

function toQuery(filters: Filters): string {
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

async function request(path: string): Promise<Response> {
  const resp = await fetch(path, { headers: { Accept: "application/json" } });
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

export const api = {
  issues: (filters: Filters = {}) => getPage<Issue>(`api/issues${toQuery(filters)}`),
  ready: (filters: Filters = {}) => getPage<Issue>(`api/ready${toQuery(filters)}`),
  blocked: (filters: Filters = {}) => getPage<Issue>(`api/blocked${toQuery(filters)}`),
  search: (filters: Filters) => getPage<Issue>(`api/search${toQuery(filters)}`),
  issue: (id: string) => getOne<Issue>(`api/issues/${encodeURIComponent(id)}`),
  tree: (id: string) => getOne<IssueTree>(`api/issues/${encodeURIComponent(id)}/tree`),
  projects: () => getPage<Project>("api/projects"),
  labels: (filters: Filters = {}) => getPage<Facet>(`api/labels${toQuery(filters)}`),
  assignees: (filters: Filters = {}) => getPage<Facet>(`api/assignees${toQuery(filters)}`),
  identity: () => getOne<{ identity: string }>("api/identity"),
};

export { toQuery };
