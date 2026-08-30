// The bundled web UI: projects, issues, search, dependency trees and issue
// comments, over the same HTTP API anything else would use.

import {
  api,
  ApiError,
  blockedFilters,
  facetFilters,
  readyFilters,
  type Attachment,
  type Activity,
  type Facet,
  type Filters,
  type Issue,
  type IssueTree,
  type Project,
} from "./api.js";
import {
  emptyFacetLabel,
  filterIssues,
  filterProjects,
  nextSortValue,
  sortIssues,
  sortProjects,
  sortState,
  withClosedIssues,
  type SortDirection,
  type SortState,
} from "./listings.js";
import { commentSubmitShortcut } from "./keyboard.js";
import { renderMarkdown } from "./markdown.js";
import { activityValues, initialFor, relativeTime } from "./presentation.js";
import { configureSearchBox } from "./search.js";
import { issueSidebarCollapsed, issueSidebarStorage, rememberIssueSidebar } from "./sidebar.js";
import { navigationPath, projectScopedHref } from "./navigation.js";

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

type IconName = "blocked" | "change" | "info" | "issues" | "projects" | "ready" | "search" | "tag";

/** svgIcon keeps the small, decorative interface icons in the document rather
 * than adding another asset pipeline or network request. */
function svgIcon(name: IconName): SVGSVGElement {
  const paths: Record<IconName, string> = {
    blocked: '<circle cx="12" cy="12" r="9"></circle><path d="m5.7 5.7 12.6 12.6"></path>',
    change: '<path d="M7 7h11l-3-3m3 3-3 3"></path><path d="M17 17H6l3 3m-3-3 3-3"></path>',
    info: '<circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path>',
    issues: '<path d="M6 3h8l4 4v14H6z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>',
    projects: '<path d="M3 6h7l2 2h9v11H3z"></path>',
    ready: '<path d="m5.5 5.1-3.5 6.9v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z"></path><path d="M2 12h6l2 3h4l2-3h6"></path>',
    search: '<circle cx="11" cy="11" r="7"></circle><path d="m16 16 4 4"></path>',
    tag: '<path d="M20 13 13 20 4 11V4h7z"></path><circle cx="8.5" cy="8.5" r="1"></circle>',
  };
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("icon");
  svg.innerHTML = paths[name];
  return svg;
}

function avatar(name: string, className = ""): HTMLElement {
  const marker = element("span", `avatar${className === "" ? "" : ` ${className}`}`, initialFor(name));
  marker.setAttribute("aria-hidden", "true");
  return marker;
}

function timeElement(timestamp: string): HTMLTimeElement {
  const time = element("time", "timestamp", relativeTime(timestamp)) as HTMLTimeElement;
  time.dateTime = timestamp;
  time.title = timestamp;
  return time;
}

function clear(node: HTMLElement): void {
  node.replaceChildren();
}

function routeHref(route: Route, query: URLSearchParams): string {
  const suffix = query.toString();
  return `#/${route.path.join("/")}${suffix === "" ? "" : `?${suffix}`}`;
}

function facetHref(route: Route, name: string, value: string): string {
  const query = new URLSearchParams(route.query);
  if (query.getAll(name).includes(value)) {
    const remaining = query.getAll(name).filter((current) => current !== value);
    query.delete(name);
    for (const current of remaining) query.append(name, current);
  } else {
    query.append(name, value);
  }
  return routeHref(route, query);
}

function refreshFacetHrefs(route: Route): void {
  for (const anchor of app.querySelectorAll<HTMLAnchorElement>("a[data-facet-name][data-facet-value]")) {
    anchor.href = facetHref(route, anchor.dataset.facetName ?? "", anchor.dataset.facetValue ?? "");
  }
}

function replaceRouteQuery(route: Route, name: string, value: string): void {
  const query = new URLSearchParams(route.query);
  if (value === "") query.delete(name);
  else query.set(name, value);
  route.query = query;
  history.replaceState(null, "", routeHref(route, query));
  refreshFacetHrefs(route);
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

interface SortChoice {
  key: string;
  label: string;
}

interface IssueColumn extends SortChoice {
  render: (issue: Issue) => HTMLElement;
}

const issueSortKeys = [
  "id", "project", "priority", "status", "assignee", "created", "updated", "type", "blockers", "relevance",
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
  const arrow = element(
    "span",
    active ? "sort-arrow" : "sort-arrow sort-hint",
    active ? (state.direction === "asc" ? "▲" : "▼") : "↕",
  );
  arrow.setAttribute("aria-hidden", "true");
  if (active) {
    button.classList.add("active");
  }
  button.append(arrow);
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

function mobileSortControl(
  route: Route,
  columns: SortChoice[],
  defaultLabel: string,
  extraOptions: SortChoice[] = [],
): HTMLElement {
  const label = element("label", "mobile-sort-control", "Sort");
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Sort listing");

  const natural = document.createElement("option");
  natural.value = "";
  natural.textContent = defaultLabel;
  select.append(natural);
  for (const column of columns) {
    for (const [prefix, arrow] of [["", "▲"], ["-", "▼"]]) {
      const option = document.createElement("option");
      option.value = `${prefix}${column.key}`;
      option.textContent = `${column.label} ${arrow}`;
      select.append(option);
    }
  }
  for (const extra of extraOptions) {
    const option = document.createElement("option");
    option.value = extra.key;
    option.textContent = extra.label;
    select.append(option);
  }

  const value = route.query.get("sort") ?? "";
  select.value = [...select.options].some((option) => option.value === value) ? value : "";
  select.addEventListener("change", () => {
    const query = new URLSearchParams(route.query);
    if (select.value === "") query.delete("sort");
    else query.set("sort", select.value);
    location.hash = routeHref(route, query).slice(1);
  });
  label.append(select);
  return label;
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
  trailingControl: HTMLElement | null = null,
  adjacentControl: HTMLElement | null = null,
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
  clearButton.setAttribute("aria-label", "Clear filter");
  control.append(input, clearButton);
  const count = element("span", "filter-count");
  bar.append(control, count);
  if (adjacentControl !== null) bar.append(adjacentControl);
  if (trailingControl !== null) bar.append(trailingControl);

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

/** includeClosedControl widens an issue listing without losing its filters. */
function includeClosedControl(route: Route): HTMLElement {
  const label = element("label", "include-closed-control");
  const input = document.createElement("input");
  input.type = "checkbox";
  input.checked = route.query.get("include-closed") === "true";
  input.addEventListener("change", () => {
    location.hash = routeHref(route, withClosedIssues(route.query, input.checked)).slice(1);
  });
  label.append(input, document.createTextNode("Show closed"));
  return label;
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
  const columns = issueColumns(kind);
  const mobileColumns = [...columns, { key: "created", label: "Created" }];
  const listingActions = element("div", "listing-actions");
  listingActions.append(mobileSortControl(
    route,
    mobileColumns,
    kind === "search" ? "Best match" : "Natural order",
    kind === "search" ? [{ key: "-relevance", label: "Worst match" }] : [],
  ));

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

  section.append(listingFilter(
    route,
    `Filter ${kind === "search" ? "results" : kind}…`,
    "issue",
    total,
    update,
    listingActions,
    kind === "issues" || kind === "search" ? includeClosedControl(route) : null,
  ));
  if (facets !== null) section.append(facets);
  section.append(tableHost);
  return section;
}

/** filtersFrom reads the filter parameters a listing route carries. */
function filtersFrom(query: URLSearchParams, allowRelevance = false): Filters {
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
  const apiSorts = ["priority", "created", "updated", "id", "-priority", "-created", "-updated", "-id"];
  if (allowRelevance) apiSorts.push("relevance", "-relevance");
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as Filters["sort"];
  return filters;
}

/**
 * facetBar renders every applicable filter group even when it has no values,
 * so an empty database still makes the available filtering features visible.
 * A null group is not applicable to that listing and remains absent.
 */
function facetBar(
  route: Route,
  projects: Project[],
  labels: Facet[] | null,
  assignees: Facet[] | null,
): HTMLElement {
  const bar = element("div", "facets");

  const projectGroup = element("div", "facet-group projects");
  projectGroup.append(element("span", "facet-title", "projects"));
  const projectEmpty = emptyFacetLabel(projects);
  if (projectEmpty !== null) {
    projectGroup.append(element("span", "facet-empty", projectEmpty));
  } else {
    for (const project of projects) {
      const active = route.query.getAll("project").includes(project.key);
      const anchor = link(
        facetHref(route, "project", project.key),
        project.key,
        active ? "facet active" : "facet",
      );
      anchor.dataset.facetName = "project";
      anchor.dataset.facetValue = project.key;
      projectGroup.append(anchor);
    }
  }
  bar.append(projectGroup);

  const build = (name: string, title: string, facets: Facet[] | null): HTMLElement | null => {
    if (facets === null) return null;
    const group = element("div", "facet-group");
    group.append(element("span", "facet-title", title));
    const empty = emptyFacetLabel(facets);
    if (empty !== null) {
      group.append(element("span", "facet-empty", empty));
      return group;
    }
    for (const facet of facets) {
      const active = route.query.getAll(name).includes(facet.value);
      const prefix = name === "label" ? "#" : "@";
      const anchor = link(
        facetHref(route, name, facet.value),
        `${prefix}${facet.value} ${facet.count}`,
        active ? "facet active" : "facet",
      );
      anchor.dataset.facetName = name;
      anchor.dataset.facetValue = facet.value;
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
    facetBar(route, projects.rows, labels.rows, kind === "ready" ? null : assignees.rows),
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
  return "Open and in-progress issues. Show closed to list every issue.";
}

function emptyFor(kind: string): string {
  if (kind === "ready") return "Nothing is ready.";
  if (kind === "blocked") return "Nothing is blocked.";
  return "No issues.";
}

const projectSortKeys = ["key", "active", "updated"] as const;
const projectColumns: SortChoice[] = [
  { key: "key", label: "Project" },
  { key: "active", label: "Open" },
  { key: "updated", label: "Updated" },
];

function projectSortButton(route: Route, key: string, label: string, state: SortState): HTMLElement {
  const button = element("button", "sort-button", label) as HTMLButtonElement;
  button.type = "button";
  const active = state.key === key;
  const arrow = element(
    "span",
    active ? "sort-arrow" : "sort-arrow sort-hint",
    active ? (state.direction === "asc" ? "▲" : "▼") : "↕",
  );
  arrow.setAttribute("aria-hidden", "true");
  if (active) {
    button.classList.add("active");
  }
  button.append(arrow);
  button.title = active
    ? `Sorted by ${label}, ${state.direction === "asc" ? "ascending" : "descending"}`
    : `Sort by ${label}`;
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
  const table = element("table", "listing-table project-table") as HTMLTableElement;
  const head = document.createElement("thead");
  const heading = document.createElement("tr");
  for (const column of projectColumns) {
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

    const projectCell = document.createElement("td");
    projectCell.dataset.label = "Project";
    projectCell.append(nameLink(href, project.key, project.name));
    if (project.description !== "") {
      const description = element("div", "project-description markdown");
      description.innerHTML = renderMarkdown(project.description);
      projectCell.append(description);
    }
    row.append(projectCell);

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
  listing.append(listingFilter(
    route,
    "Filter projects…",
    "project",
    page.total,
    update,
    mobileSortControl(route, projectColumns, "Natural order"),
  ), host);
  view.append(listing);
  return view;
}

async function viewIssue(id: string): Promise<HTMLElement> {
  const [issue, activity] = await Promise.all([api.issue(id), api.activity(id)]);

  const view = element("div", "issue-view");
  view.classList.toggle("sidebar-collapsed", issueSidebarCollapsed(issueSidebarStorage(window)));
  const content = element("div", "issue-content");
  const heading = element("div", "issue-heading");
  heading.append(element("div", "issue-key", issue.id));
  heading.append(element("h1", "", issue.title));
  content.append(heading);

  const description = element("section", "issue-description");
  description.append(element("h2", "", "Description"));

  if (issue.description !== "") {
    const body = element("div", "issue-description-body markdown");
    body.innerHTML = renderMarkdown(issue.description);
    description.append(body);
  } else {
    description.append(element("p", "empty", "No description."));
  }
  content.append(description);

  // The derived links array is rendered explicitly as well as inside the
  // prose, so the authoritative list is always visible.
  if (issue.links.length > 0) {
    content.append(element("h2", "", "Links"));
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
    content.append(list);
  }

  if (issue.attachments.length > 0) {
    content.append(element("h2", "", "Attachments"));
    const list = element("ul", "attachments");
    for (const attachment of issue.attachments) list.append(attachmentRow(attachment));
    content.append(list);
  }

  if (issue.relations.length > 0) {
    content.append(element("h2", "", "Relations"));
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
    content.append(list);
  }

  content.append(link(`#/tree/${issue.id}`, "Show the decomposition below this issue", "action"));
  content.append(activitySection(issue.id, activity.rows));
  const [sidebar, sidebarToggle] = issueSidebar(issue, view);
  view.append(content, sidebar, sidebarToggle);
  return view;
}

function issueSidebar(issue: Issue, view: HTMLElement): [HTMLElement, HTMLButtonElement] {
  const aside = element("aside", "issue-sidebar");
  aside.id = "issue-details";
  aside.setAttribute("aria-label", "Issue details");
  const toggle = element("button", "issue-sidebar-toggle") as HTMLButtonElement;
  toggle.type = "button";
  toggle.setAttribute("aria-controls", aside.id);
  const drawToggle = (): void => {
    const collapsed = view.classList.contains("sidebar-collapsed");
    toggle.replaceChildren(svgIcon("info"));
    toggle.title = collapsed ? "Show issue details" : "Hide issue details";
    toggle.setAttribute("aria-label", toggle.title);
    toggle.setAttribute("aria-expanded", String(!collapsed));
  };
  toggle.addEventListener("click", () => {
    const collapsed = view.classList.toggle("sidebar-collapsed");
    rememberIssueSidebar(issueSidebarStorage(window), collapsed);
    drawToggle();
  });
  const facts = element("dl", "issue-facts");

  const add = (label: string, value: Node): void => {
    const row = element("div", "issue-fact");
    row.append(element("dt", "", label));
    const detail = element("dd");
    detail.append(value);
    row.append(detail);
    facts.append(row);
  };
  const text = (value: string): Text => document.createTextNode(value === "" ? "—" : value);

  add("ID", element("span", "id", issue.id));
  add("Project", link(`#/issues?project=${encodeURIComponent(issue.project)}`, issue.project));
  add("Type", badge("type", issue.type));
  add("Status", badge(`status status-${issue.status}`, issue.status));
  add("Priority", badge(`priority p${issue.priority}`, `P${issue.priority}`));
  if (issue.blocked) add("Readiness", badge("blocked", "blocked"));

  const labels = element("span", "sidebar-labels");
  if (issue.labels.length === 0) labels.append(text("—"));
  for (const label of issue.labels) labels.append(badge("label", label));
  add("Labels", labels);
  add("Assignee", text(issue.assignee === "" ? "" : `@${issue.assignee}`));
  add("Created", timeElement(issue.created_at));
  add("Updated", timeElement(issue.updated_at));
  aside.append(facts);
  drawToggle();
  return [aside, toggle];
}

function activitySection(issueID: string, entries: Activity[]): HTMLElement {
  const section = element("section", "activity-section");
  section.append(element("h2", "", "Activity"));

  const filters = element("div", "activity-filters");
  const timeline = element("div", "activity-timeline");
  let selected: "all" | Activity["kind"] = "all";
  const draw = (): void => {
    clear(timeline);
    const visible = selected === "all" ? entries : entries.filter((entry) => entry.kind === selected);
    if (visible.length === 0) {
      timeline.append(element("p", "empty", selected === "all" ? "No activity yet." : `No ${selected}s yet.`));
      return;
    }
    for (const entry of visible) timeline.append(activityEntry(entry));
  };
  for (const [value, label] of [["all", "All"], ["comment", "Comments"], ["change", "Changes"]] as const) {
    const button = element("button", value === "all" ? "active" : "", label) as HTMLButtonElement;
    button.type = "button";
    button.setAttribute("aria-pressed", String(value === "all"));
    button.addEventListener("click", () => {
      selected = value;
      for (const item of filters.querySelectorAll("button")) {
        const active = item === button;
        item.classList.toggle("active", active);
        item.setAttribute("aria-pressed", String(active));
      }
      draw();
    });
    filters.append(button);
  }
  section.append(filters, commentComposer(issueID), timeline);
  draw();
  return section;
}

function commentComposer(issueID: string): HTMLElement {
  const form = element("form", "comment-composer") as HTMLFormElement;
  form.append(avatar(identity, "composer-avatar"));
  const body = element("div", "comment-composer-body");
  const textarea = document.createElement("textarea");
  textarea.name = "body";
  textarea.placeholder = "Write a comment…";
  textarea.required = true;
  textarea.setAttribute("aria-label", "Comment");
  textarea.setAttribute("aria-keyshortcuts", "Control+Enter Meta+Enter");
  const hint = element("span", "comment-hint", "Comments use Markdown. Ctrl/⌘+Enter to submit.");
  const button = element("button", "comment-submit", "Add comment") as HTMLButtonElement;
  button.type = "submit";
  textarea.addEventListener("keydown", (event) => {
    if (!commentSubmitShortcut(event)) return;
    event.preventDefault();
    if (!button.disabled) form.requestSubmit(button);
  });
  const actions = element("div", "comment-composer-actions");
  actions.append(hint, button);
  body.append(textarea, actions);
  form.append(body);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    button.disabled = true;
    try {
      await api.addComment(issueID, textarea.value);
      await render();
    } catch (error) {
      button.disabled = false;
      const message = error instanceof ApiError ? error.message : String(error);
      body.append(element("p", "comment-error", message));
    }
  });
  return form;
}

function activityEntry(entry: Activity): HTMLElement {
  const row = element("article", `activity-entry activity-${entry.kind}`);
  const marker = element("div", `activity-marker activity-marker-${entry.kind}`);
  if (entry.kind === "comment") marker.append(avatar(entry.actor));
  else marker.append(svgIcon(entry.action.includes("label") ? "tag" : "change"));
  const card = element("div", "activity-card");
  const header = element("header", "activity-header");
  header.append(element("strong", "activity-actor", entry.actor === "" ? "system" : entry.actor));
  header.append(timeElement(entry.created_at));
  card.append(header);
  if (entry.kind === "comment") {
    if (entry.action !== "") card.append(element("div", "activity-action", activityAction(entry.action)));
    const body = element("div", "activity-comment-body markdown");
    body.innerHTML = renderMarkdown(entry.body);
    card.append(body);
  } else {
    card.append(element("div", "activity-action", activityAction(entry.action)));
  }
  if (entry.changes.length > 0) {
    const changes = element("ul", "activity-changes");
    for (const change of entry.changes) {
      const [from, to] = activityValues(change.from, change.to);
      const item = element("li");
      item.append(element("span", "activity-field", change.field));
      item.append(element("code", "", from));
      item.append(element("span", "activity-arrow", "→"));
      item.append(element("code", "", to));
      changes.append(item);
    }
    card.append(changes);
  }
  row.append(marker, card);
  return row;
}

function activityAction(action: string): string {
  return action.replaceAll("_", " ");
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
    api.search({ ...filtersFrom(route.query, true), q: terms }),
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
    facetBar(route, projects.rows, null, null),
  ));
  return view;
}

/** searchBox is the global navigation control. */
function searchBox(): HTMLElement {
  const form = element("form", "search") as HTMLFormElement;
  const input = document.createElement("input");
  configureSearchBox(form, input);
  input.value = parseRoute().query.getAll("q").join(" ");
  form.append(svgIcon("search"), input);

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
  const header = element("header", "app-header");
  const nav = element("nav");
  const route = parseRoute();
  const navLink = (href: string, label: string, icon: IconName): HTMLAnchorElement => {
    const anchor = link(href, "");
    anchor.append(svgIcon(icon), document.createTextNode(label));
    return anchor;
  };
  nav.append(navLink(projectScopedHref("ready", route.query), "Ready", "ready"));
  nav.append(navLink(projectScopedHref("issues", route.query), "Issues", "issues"));
  nav.append(navLink(projectScopedHref("blocked", route.query), "Blocked", "blocked"));
  nav.append(navLink(projectScopedHref("projects", route.query), "Projects", "projects"));
  const brand = link("#/ready", "", "brand");
  const mark = document.createElement("img");
  mark.src = "awb-mark.png";
  mark.alt = "";
  mark.className = "brand-mark";
  brand.append(mark, document.createTextNode("Agent Work Board"));
  header.append(brand);
  header.append(nav);
  header.append(searchBox());
  if (identity !== "") {
    const account = element("span", "identity");
    account.append(avatar(identity), element("span", "identity-name", `@${identity}`));
    header.append(account);
  }
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
    const target = navigationPath(anchor.getAttribute("href") ?? "");
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
