// The bundled read-only web UI: projects, issues, search and dependency trees,
// over the same HTTP API anything else would use.

import {
  api,
  ApiError,
  blockedFilters,
  facetFilters,
  readyFilters,
  type Attachment,
  type Facet,
  type Filters,
  type Issue,
  type IssueTree,
  type Project,
} from "./api.js";
import {
  filterIssues,
  filterProjects,
  nextSortValue,
  sortIssues,
  sortProjects,
  sortState,
  type SortDirection,
  type SortState,
} from "./listings.js";
import { renderMarkdown } from "./markdown.js";

/** One route: the fragment after "#/" split into segments and a query. */
interface Route {
  path: string[];
  query: URLSearchParams;
}

const app = document.getElementById("app") as HTMLElement;

/** identity is the caller the server attributes requests to. */
let identity = "";

function parseRoute(): Route {
  const hash = location.hash.replace(/^#\/?/, "");
  const [path, query] = hash.split("?", 2);
  return {
    path: path.split("/").filter((segment) => segment !== ""),
    query: new URLSearchParams(query ?? ""),
  };
}

function link(href: string, text: string, className = ""): HTMLAnchorElement {
  const anchor = document.createElement("a");
  anchor.href = href;
  anchor.textContent = text;
  if (className !== "") anchor.className = className;
  return anchor;
}

/**
 * nameLink renders the id and the title of a row as one link, so that the
 * title leads where the id leads instead of being the dead half of the pair.
 *
 * It is deliberately a single anchor around both rather than one around each:
 * two anchors to one destination would be two tab stops and two separate
 * announcements per row, which a long listing multiplies. A project need not
 * have a name, and the id alone then names the link.
 */
function nameLink(href: string, id: string, title: string): HTMLElement {
  const anchor = document.createElement("a");
  anchor.href = href;
  anchor.className = "name";
  anchor.append(element("span", "id", id));
  if (title !== "") anchor.append(element("span", "title", title));
  return anchor;
}

function element(tag: string, className = "", text = ""): HTMLElement {
  const node = document.createElement(tag);
  if (className !== "") node.className = className;
  if (text !== "") node.textContent = text;
  return node;
}

function clear(node: HTMLElement): void {
  node.replaceChildren();
}

function routeHref(route: Route, query: URLSearchParams): string {
  const suffix = query.toString();
  return `#/${route.path.join("/")}${suffix === "" ? "" : `?${suffix}`}`;
}

function replaceRouteQuery(route: Route, name: string, value: string): void {
  const query = new URLSearchParams(route.query);
  if (value === "") query.delete(name);
  else query.set(name, value);
  route.query = query;
  history.replaceState(null, "", routeHref(route, query));
}

/** issueBadges renders the small status markers a listing shows. */
function issueBadges(issue: Issue): HTMLElement {
  const row = element("span", "badges");
  row.append(element("span", `priority p${issue.priority}`, `P${issue.priority}`));
  row.append(element("span", `status status-${issue.status}`, issue.status));
  if (issue.blocked) row.append(element("span", "blocked", "blocked"));
  if (issue.assignee !== "") {
    const assignee = element("span", "assignee", `@${issue.assignee}`);
    if (issue.assignee === identity) assignee.classList.add("mine");
    row.append(assignee);
  }
  for (const label of issue.labels) row.append(element("span", "label", `#${label}`));
  return row;
}

type ListingKind = "issues" | "ready" | "blocked" | "search";

interface IssueColumn {
  key: string;
  label: string;
  render: (issue: Issue) => HTMLElement;
}

const issueSortKeys = [
  "id", "project", "priority", "status", "assignee", "updated", "type", "blockers", "relevance",
] as const;

function textCell(className: string, text: string): HTMLElement {
  return element("span", className, text);
}

function issueNameCell(issue: Issue): HTMLElement {
  const cell = element("span", "issue-name-cell");
  cell.append(nameLink(`#/issues/${issue.id}`, issue.id, issue.title));
  if (issue.labels.length > 0) {
    const labels = element("span", "row-labels");
    for (const label of issue.labels) labels.append(element("span", "row-label", `#${label}`));
    cell.append(labels);
  }
  return cell;
}

function badge(className: string, text: string): HTMLElement {
  return element("span", `listing-badge ${className}`, text);
}

function issueColumns(kind: ListingKind): IssueColumn[] {
  const issue: IssueColumn = { key: "id", label: "Issue", render: issueNameCell };
  const project: IssueColumn = {
    key: "project",
    label: "Project",
    render: (row) => textCell("id", row.project),
  };
  const priority: IssueColumn = {
    key: "priority",
    label: "Priority",
    render: (row) => badge(`priority p${row.priority}`, `P${row.priority}`),
  };
  const updated: IssueColumn = {
    key: "updated",
    label: "Updated",
    render: (row) => {
      const time = element("time", "timestamp", row.updated_at.slice(0, 10));
      time.setAttribute("datetime", row.updated_at);
      time.title = row.updated_at;
      return time;
    },
  };

  if (kind === "ready") {
    return [
      issue,
      project,
      {
        key: "type",
        label: "Type",
        render: (row) => badge("type", row.type),
      },
      priority,
      updated,
    ];
  }

  const assignee: IssueColumn = {
    key: "assignee",
    label: "Assignee",
    render: (row) => row.assignee === ""
      ? textCell("muted", "—")
      : badge(row.assignee === identity ? "assignee mine" : "assignee", `@${row.assignee}`),
  };
  if (kind === "blocked") {
    return [
      issue,
      project,
      priority,
      assignee,
      {
        key: "blockers",
        label: "Blocked by",
        render: (row) => textCell("blocker-list", row.blockers.join(", ")),
      },
    ];
  }

  return [
    issue,
    project,
    priority,
    {
      key: "status",
      label: "Status",
      render: (row) => badge(`status status-${row.status}`, row.status),
    },
    assignee,
    updated,
  ];
}

function sortButton(
  route: Route,
  column: IssueColumn,
  state: SortState,
  defaultKey: string,
  defaultDirection: SortDirection,
): HTMLElement {
  const button = element("button", "sort-button", column.label) as HTMLButtonElement;
  button.type = "button";
  const active = state.key === column.key;
  if (active) {
    button.classList.add("active");
    button.append(element("span", "sort-arrow", state.direction === "asc" ? "▲" : "▼"));
  } else {
    button.append(element("span", "sort-arrow sort-hint", "↕"));
  }
  button.title = active
    ? `Sorted by ${column.label}, ${state.direction === "asc" ? "ascending" : "descending"}`
    : `Sort by ${column.label}`;
  button.addEventListener("click", () => {
    const query = new URLSearchParams(route.query);
    const next = nextSortValue(
      query.get("sort"), column.key, issueSortKeys, defaultKey, defaultDirection,
    );
    if (next === null) query.delete("sort");
    else query.set("sort", next);
    location.hash = routeHref(route, query).slice(1);
  });
  return button;
}

function issueTable(
  route: Route,
  issues: Issue[],
  kind: ListingKind,
  state: SortState,
  defaultKey: string,
  defaultDirection: SortDirection,
): HTMLElement {
  const columns = issueColumns(kind);
  const table = element("table", `listing-table issue-table listing-${kind}`) as HTMLTableElement;
  const head = document.createElement("thead");
  const heading = document.createElement("tr");
  for (const column of columns) {
    const th = document.createElement("th");
    th.scope = "col";
    if (state.key === column.key) th.setAttribute("aria-sort", state.direction === "asc" ? "ascending" : "descending");
    th.append(sortButton(route, column, state, defaultKey, defaultDirection));
    heading.append(th);
  }
  head.append(heading);
  table.append(head);

  const body = document.createElement("tbody");
  for (const issue of issues) {
    const row = document.createElement("tr");
    for (const column of columns) {
      const td = document.createElement("td");
      td.dataset.label = column.label;
      td.append(column.render(issue));
      row.append(td);
    }
    body.append(row);
  }
  table.append(body);
  return table;
}

function listingFilter(
  route: Route,
  placeholder: string,
  noun: string,
  total: number,
  update: (query: string) => number,
): HTMLElement {
  const bar = element("div", "listing-tools");
  const control = element("div", "listing-filter");
  const input = document.createElement("input");
  input.type = "search";
  input.placeholder = placeholder;
  input.setAttribute("aria-label", placeholder);
  input.value = route.query.get("filter") ?? "";
  const clearButton = element("button", "clear-filter", "×") as HTMLButtonElement;
  clearButton.type = "button";
  clearButton.title = "Clear filter";
  control.append(input, clearButton);
  const count = element("span", "filter-count");
  bar.append(control, count);

  const refresh = (): void => {
    const visible = update(input.value);
    count.textContent = input.value.trim() === ""
      ? `${total} ${noun}${total === 1 ? "" : "s"}`
      : `${visible} of ${total}`;
    clearButton.hidden = input.value === "";
    replaceRouteQuery(route, "filter", input.value);
  };
  input.addEventListener("input", refresh);
  clearButton.addEventListener("click", () => {
    input.value = "";
    refresh();
    input.focus();
  });

  const visible = update(input.value);
  count.textContent = input.value.trim() === ""
    ? `${total} ${noun}${total === 1 ? "" : "s"}`
    : `${visible} of ${total}`;
  clearButton.hidden = input.value === "";
  return bar;
}

function issueList(
  route: Route,
  issues: Issue[],
  total: number,
  emptyMessage: string,
  kind: ListingKind,
  facets: HTMLElement | null,
): HTMLElement {
  const section = element("div", "listing");
  const tableHost = element("div", "listing-host");
  const defaultKey = kind === "search" ? "relevance" : "priority";
  const defaultDirection: SortDirection = kind === "search" ? "desc" : "asc";
  const state = sortState(route.query.get("sort"), issueSortKeys, defaultKey, defaultDirection);

  const update = (query: string): number => {
    const rows = sortIssues(filterIssues(issues, query), state);
    clear(tableHost);
    if (rows.length === 0) {
      tableHost.append(element("p", "empty", query.trim() === "" ? emptyMessage : "No issues match this filter."));
    } else {
      tableHost.append(issueTable(route, rows, kind, state, defaultKey, defaultDirection));
    }
    return rows.length;
  };

  section.append(listingFilter(route, `Filter ${kind === "search" ? "results" : kind}…`, "issue", total, update));
  if (facets !== null) section.append(facets);
  section.append(tableHost);
  return section;
}

/** filtersFrom reads the filter parameters a listing route carries. */
function filtersFrom(query: URLSearchParams): Filters {
  const filters: Filters = {};
  const project = query.getAll("project");
  if (project.length > 0) filters.project = project;
  const label = query.getAll("label");
  if (label.length > 0) filters.label = label;
  const assignee = query.getAll("assignee");
  if (assignee.length > 0) filters.assignee = assignee;
  if (query.get("include-closed") === "true") filters["include-closed"] = true;
  // The UI adds presentation-only orderings for columns such as project and
  // assignee. Pass through only the issue API's vocabulary; the complete,
  // unpaged response is ordered locally for the other visible columns.
  const sort = query.get("sort");
  const apiSorts = [
    "priority", "created", "updated", "id", "relevance",
    "-priority", "-created", "-updated", "-id", "-relevance",
  ];
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as Filters["sort"];
  return filters;
}

/** facetBar renders the project, label and assignee menus a UI narrows with. */
function facetBar(route: Route, projects: Project[], labels: Facet[], assignees: Facet[]): HTMLElement {
  const bar = element("div", "facets");

  if (projects.length > 0) {
    const group = element("div", "facet-group projects");
    group.append(element("span", "facet-title", "projects"));
    for (const project of projects) {
      const active = route.query.getAll("project").includes(project.key);
      const query = new URLSearchParams(route.query);
      if (active) {
        const remaining = query.getAll("project").filter((value) => value !== project.key);
        query.delete("project");
        for (const value of remaining) query.append("project", value);
      } else {
        query.append("project", project.key);
      }
      group.append(link(routeHref(route, query), project.key, active ? "facet active" : "facet"));
    }
    bar.append(group);
  }

  const build = (name: string, title: string, facets: Facet[]): HTMLElement | null => {
    if (facets.length === 0) return null;
    const group = element("div", "facet-group");
    group.append(element("span", "facet-title", title));
    for (const facet of facets) {
      const active = route.query.getAll(name).includes(facet.value);
      const query = new URLSearchParams(route.query);
      if (active) {
        const remaining = query.getAll(name).filter((v) => v !== facet.value);
        query.delete(name);
        for (const value of remaining) query.append(name, value);
      } else {
        query.append(name, facet.value);
      }
      const prefix = name === "label" ? "#" : "@";
      const anchor = link(
        routeHref(route, query),
        `${prefix}${facet.value} ${facet.count}`,
        active ? "facet active" : "facet",
      );
      group.append(anchor);
    }
    return group;
  };

  const labelGroup = build("label", "labels", labels);
  if (labelGroup !== null) bar.append(labelGroup);
  const assigneeGroup = build("assignee", "assignees", assignees);
  if (assigneeGroup !== null) bar.append(assigneeGroup);
  return bar;
}

async function viewListing(route: Route, kind: "issues" | "ready" | "blocked"): Promise<HTMLElement> {
  const filters = filtersFrom(route.query);

  // Each listing is asked with the filters it accepts. Ready lists only
  // unassigned issues, so there is no assignee menu to offer there either.
  const [page, projects, labels, assignees] = await Promise.all([
    kind === "ready"
      ? api.ready(readyFilters(filters))
      : kind === "blocked"
        ? api.blocked(blockedFilters(filters))
        : api.issues(filters),
    api.projects(),
    api.labels(kind === "ready" ? {} : facetFilters(filters)),
    kind === "ready" ? Promise.resolve({ rows: [], total: 0 }) : api.assignees(facetFilters(filters)),
  ]);

  const view = element("div");
  view.append(element("h1", "", titleFor(kind)));
  view.append(element("p", "lede", ledeFor(kind)));
  view.append(issueList(
    route,
    page.rows,
    page.total,
    emptyFor(kind),
    kind,
    facetBar(route, projects.rows, labels.rows, assignees.rows),
  ));
  return view;
}

function titleFor(kind: string): string {
  if (kind === "ready") return "Ready";
  if (kind === "blocked") return "Blocked";
  return "Issues";
}

function ledeFor(kind: string): string {
  if (kind === "ready") return "Open, unblocked and unassigned — what to pick up next.";
  if (kind === "blocked") return "Not closed, and waiting on something that is not closed.";
  return "Every issue that is not closed.";
}

function emptyFor(kind: string): string {
  if (kind === "ready") return "Nothing is ready.";
  if (kind === "blocked") return "Nothing is blocked.";
  return "No issues.";
}

const projectSortKeys = ["key", "name", "active", "updated"] as const;

function projectSortButton(route: Route, key: string, label: string, state: SortState): HTMLElement {
  const button = element("button", "sort-button", label) as HTMLButtonElement;
  button.type = "button";
  if (state.key === key) {
    button.classList.add("active");
    button.append(element("span", "sort-arrow", state.direction === "asc" ? "▲" : "▼"));
  } else {
    button.append(element("span", "sort-arrow sort-hint", "↕"));
  }
  button.addEventListener("click", () => {
    const query = new URLSearchParams(route.query);
    const next = nextSortValue(query.get("sort"), key, projectSortKeys, "key");
    if (next === null) query.delete("sort");
    else query.set("sort", next);
    location.hash = routeHref(route, query).slice(1);
  });
  return button;
}

function projectTable(route: Route, projects: Project[], state: SortState): HTMLElement {
  const columns = [
    { key: "key", label: "Key" },
    { key: "name", label: "Project" },
    { key: "active", label: "Open" },
    { key: "updated", label: "Updated" },
  ];
  const table = element("table", "listing-table project-table") as HTMLTableElement;
  const head = document.createElement("thead");
  const heading = document.createElement("tr");
  for (const column of columns) {
    const th = document.createElement("th");
    th.scope = "col";
    if (state.key === column.key) th.setAttribute("aria-sort", state.direction === "asc" ? "ascending" : "descending");
    th.append(projectSortButton(route, column.key, column.label, state));
    heading.append(th);
  }
  head.append(heading);
  table.append(head);

  const body = document.createElement("tbody");
  for (const project of projects) {
    const row = document.createElement("tr");
    const href = `#/issues?project=${encodeURIComponent(project.key)}`;

    const key = document.createElement("td");
    key.dataset.label = "Key";
    key.append(link(href, project.key, "id"));
    row.append(key);

    const name = document.createElement("td");
    name.dataset.label = "Project";
    name.append(link(href, project.name, "project-name"));
    if (project.description !== "") {
      const description = element("div", "project-description markdown");
      description.innerHTML = renderMarkdown(project.description);
      name.append(description);
    }
    row.append(name);

    const active = document.createElement("td");
    active.dataset.label = "Open";
    active.append(element("span", "open-count", String(project.active_issues)));
    row.append(active);

    const updated = document.createElement("td");
    updated.dataset.label = "Updated";
    const time = element("time", "timestamp", project.updated_at.slice(0, 10));
    time.setAttribute("datetime", project.updated_at);
    time.title = project.updated_at;
    updated.append(time);
    row.append(updated);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewProjects(route: Route): Promise<HTMLElement> {
  const page = await api.projects();

  const view = element("div");
  view.append(element("h1", "", "Projects"));
  if (page.rows.length === 0) {
    view.append(element("p", "empty", "No projects yet. Create one with: awb project create <key>"));
    return view;
  }

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  const state = sortState(route.query.get("sort"), projectSortKeys, "key");
  const update = (query: string): number => {
    const rows = sortProjects(filterProjects(page.rows, query), state);
    clear(host);
    if (rows.length === 0) host.append(element("p", "empty", "No projects match this filter."));
    else host.append(projectTable(route, rows, state));
    return rows.length;
  };
  listing.append(listingFilter(route, "Filter projects…", "project", page.total, update), host);
  view.append(listing);
  return view;
}

async function viewIssue(id: string): Promise<HTMLElement> {
  const issue = await api.issue(id);

  const view = element("div", "issue");
  view.append(element("h1", "", issue.title));

  const meta = element("div", "meta");
  meta.append(element("span", "id", issue.id));
  meta.append(link(`#/issues?project=${encodeURIComponent(issue.project)}`, issue.project, "project"));
  meta.append(element("span", "type", issue.type));
  meta.append(issueBadges(issue));
  view.append(meta);

  if (issue.close_reason !== "") {
    view.append(element("p", "close-reason", `Closed: ${issue.close_reason}`));
  }

  if (issue.description !== "") {
    const body = element("div", "markdown");
    body.innerHTML = renderMarkdown(issue.description);
    view.append(body);
  }

  // The derived links array is rendered explicitly as well as inside the
  // prose, so the authoritative list is always visible.
  if (issue.links.length > 0) {
    view.append(element("h2", "", "Links"));
    const list = element("ul", "links");
    for (const item of issue.links) {
      const row = element("li");
      const anchor = link(item.url, item.text === "" ? item.url : item.text);
      anchor.target = "_blank";
      anchor.rel = "noopener noreferrer";
      row.append(anchor);
      if (item.text !== "") row.append(element("span", "url", item.url));
      list.append(row);
    }
    view.append(list);
  }

  if (issue.attachments.length > 0) {
    view.append(element("h2", "", "Attachments"));
    const list = element("ul", "attachments");
    for (const attachment of issue.attachments) list.append(attachmentRow(attachment));
    view.append(list);
  }

  if (issue.relations.length > 0) {
    view.append(element("h2", "", "Relations"));
    const list = element("ul", "relations");
    for (const relation of issue.relations) {
      // Every relation reads "subject — type — other", whichever end is
      // viewed.
      const [subject, other] =
        relation.direction === "in" ? [relation.other, issue.id] : [issue.id, relation.other];
      const row = element("li");
      row.append(link(`#/issues/${subject}`, subject, "id"));
      row.append(element("span", "relation-type", relation.type));
      row.append(link(`#/issues/${other}`, other, "id"));
      list.append(row);
    }
    view.append(list);
  }

  const footer = element("p", "timestamps");
  footer.append(element("span", "", `created ${issue.created_at}`));
  footer.append(element("span", "", `updated ${issue.updated_at}`));
  view.append(footer);

  view.append(link(`#/tree/${issue.id}`, "Show the decomposition below this issue", "action"));
  return view;
}

/**
 * attachmentRow renders one attachment, its name linking to the content.
 *
 * The server serves that content as application/octet-stream with a
 * Content-Disposition of attachment, whatever the recorded content type says,
 * so the browser saves it rather than rendering somebody's upload on this
 * origin. The link is an ordinary one for the same reason: nothing here opens
 * the file in the page.
 */
function attachmentRow(attachment: Attachment): HTMLElement {
  const row = element("li");
  const href =
    `api/issues/${encodeURIComponent(attachment.issue)}` +
    `/attachments/${encodeURIComponent(attachment.name)}/content`;
  row.append(link(href, attachment.name));
  row.append(element("span", "size", formatSize(attachment.size)));
  row.append(element("span", "content-type", attachment.content_type));
  return row;
}

/** formatSize is a size for a human, the exact byte count being in the API. */
function formatSize(size: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = size;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${units[unit]}`;
}

async function viewTree(id: string): Promise<HTMLElement> {
  const tree = await api.tree(id);

  const view = element("div");
  view.append(element("h1", "", "Decomposition"));
  view.append(element("p", "lede", "The whole subtree, closed children included."));
  view.append(treeNode(tree, 0));
  return view;
}

function treeNode(node: IssueTree, depth: number): HTMLElement {
  const list = element("ul", depth === 0 ? "tree" : "tree-children");
  const row = element("li", "tree-row");
  row.append(nameLink(`#/issues/${node.id}`, node.id, node.title));
  row.append(issueBadges(node));
  list.append(row);

  for (const child of node.children) {
    const wrapper = element("li", "tree-child");
    wrapper.append(treeNode(child, depth + 1));
    list.append(wrapper);
  }
  return list;
}

async function viewSearch(route: Route): Promise<HTMLElement> {
  const terms = route.query.getAll("q").filter((term) => term !== "");

  const view = element("div");
  view.append(element("h1", "", "Search"));
  view.append(
    element("p", "lede", "Literal terms, whole-token matching. An issue matches when it contains all of them."),
  );

  if (terms.length === 0) {
    view.append(element("p", "empty", "Type something to search for."));
    return view;
  }

  const [page, projects] = await Promise.all([
    api.search({ ...filtersFrom(route.query), q: terms }),
    api.projects(),
  ]);
  const bestMatch = route.query.get("sort") === null || route.query.get("sort") === "relevance"
    ? element("span", "natural-order", "Best match")
    : null;
  if (bestMatch !== null) view.append(bestMatch);
  view.append(issueList(
    route,
    page.rows,
    page.total,
    `Nothing matches ${terms.join(" ")}.`,
    "search",
    facetBar(route, projects.rows, [], []),
  ));
  return view;
}

/** searchBox is the one interactive control the read-only UI has. */
function searchBox(): HTMLElement {
  const form = element("form", "search") as HTMLFormElement;
  const input = document.createElement("input");
  input.type = "search";
  input.name = "q";
  input.placeholder = "Search…";
  input.value = parseRoute().query.getAll("q").join(" ");
  form.append(input);

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const query = new URLSearchParams();
    // Each whitespace-separated word is one literal term, which is how the
    // command line treats its positional arguments.
    for (const term of input.value.split(/\s+/).filter((t) => t !== "")) {
      query.append("q", term);
    }
    location.hash = `#/search?${query.toString()}`;
  });
  return form;
}

function chrome(): HTMLElement {
  const header = element("header");
  const nav = element("nav");
  nav.append(link("#/ready", "Ready"));
  nav.append(link("#/issues", "Issues"));
  nav.append(link("#/blocked", "Blocked"));
  nav.append(link("#/projects", "Projects"));
  header.append(element("span", "brand", "Agent Work Board"));
  header.append(nav);
  header.append(searchBox());
  if (identity !== "") header.append(element("span", "identity", `@${identity}`));
  return header;
}

async function render(): Promise<void> {
  const route = parseRoute();
  clear(app);
  app.append(chrome());

  const main = element("main");
  app.append(main);

  try {
    main.append(await routeView(route));
  } catch (error) {
    const message = error instanceof ApiError ? error.message : String(error);
    const box = element("div", "error");
    box.append(element("h1", "", "Something went wrong"));
    box.append(element("p", "", message));
    main.append(box);
  }

  markActiveNav(route);
}

async function routeView(route: Route): Promise<HTMLElement> {
  switch (route.path[0]) {
    case undefined:
    case "ready":
      return viewListing(route, "ready");
    case "issues":
      return route.path.length > 1 ? viewIssue(route.path[1]) : viewListing(route, "issues");
    case "blocked":
      return viewListing(route, "blocked");
    case "projects":
      return viewProjects(route);
    case "tree":
      return viewTree(route.path[1] ?? "");
    case "search":
      return viewSearch(route);
    default: {
      const view = element("div", "error");
      view.append(element("h1", "", "No such page"));
      view.append(link("#/ready", "Go to Ready"));
      return view;
    }
  }
}

function markActiveNav(route: Route): void {
  const current = route.path[0] ?? "ready";
  for (const anchor of app.querySelectorAll("nav a")) {
    const target = anchor.getAttribute("href")?.replace("#/", "") ?? "";
    anchor.classList.toggle("active", target === current);
  }
}

async function start(): Promise<void> {
  try {
    identity = (await api.identity()).identity;
  } catch {
    // A server that cannot say who the caller is still browses fine.
  }
  window.addEventListener("hashchange", () => void render());
  await render();
}

void start();
