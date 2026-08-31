// The bundled web UI: projects, issues, search, dependency trees and editing,
// over the same HTTP API anything else would use.

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
  type DirectoryUser,
  type Issue,
  type IssueTree,
  type Project,
  type ProjectFilters,
  type User,
  type UserFilters,
  type Relation,
} from "./api.js";
import {
  emptyFacetLabel,
  filterIssues,
  filterProjects,
  filterUsers,
  nextSortValue,
  pageNumber,
  pageSizeFrom,
  pageSizes,
  pageSizeStorage,
  pageWindow,
  rememberedPageSize,
  rememberPageSize,
  sortState,
  withPage,
  withPageSize,
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
import { accountRoles } from "./profile.js";
import {
  formatUpdated,
  readUpdatedDisplay,
  rememberUpdatedDisplay,
  updatedStorage,
  type UpdatedDisplay,
} from "./updated.js";

/** One route: the fragment after "#/" split into segments and a query. */
interface Route {
  path: string[];
  query: URLSearchParams;
}

const app = document.getElementById("app") as HTMLElement;

/** identity is the caller the server attributes requests to. */
let identity = "";
let updatedDisplay: UpdatedDisplay | null = null;
let updatedControlID = 0;
const paginationStorage = pageSizeStorage(window);

function listingPageSize(query: URLSearchParams): number {
  return pageSizeFrom(query, rememberedPageSize(paginationStorage));
}

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

type IconName = "blocked" | "change" | "clock" | "info" | "issues" | "projects" | "ready" | "search" | "tag" | "users";

/** svgIcon keeps the small, decorative interface icons in the document rather
 * than adding another asset pipeline or network request. */
function svgIcon(name: IconName): SVGSVGElement {
  const paths: Record<IconName, string> = {
    blocked: '<circle cx="12" cy="12" r="9"></circle><path d="m5.7 5.7 12.6 12.6"></path>',
    change: '<path d="M7 7h11l-3-3m3 3-3 3"></path><path d="M17 17H6l3 3m-3-3 3-3"></path>',
    clock: '<circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path>',
    info: '<circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path>',
    issues: '<path d="M6 3h8l4 4v14H6z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>',
    projects: '<path d="M3 6h7l2 2h9v11H3z"></path>',
    ready: '<path d="m5.5 5.1-3.5 6.9v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z"></path><path d="M2 12h6l2 3h4l2-3h6"></path>',
    search: '<circle cx="11" cy="11" r="7"></circle><path d="m16 16 4 4"></path>',
    tag: '<path d="M20 13 13 20 4 11V4h7z"></path><circle cx="8.5" cy="8.5" r="1"></circle>',
    users: '<circle cx="9" cy="8" r="3"></circle><path d="M3 20v-2a5 5 0 0 1 5-5h2a5 5 0 0 1 5 5v2"></path><path d="M16 5a3 3 0 0 1 0 6M17 14a5 5 0 0 1 4 4v2"></path>',
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

function currentUpdatedDisplay(): UpdatedDisplay {
  if (updatedDisplay === null) updatedDisplay = readUpdatedDisplay(updatedStorage(window));
  return updatedDisplay;
}

function updatedTimeElement(timestamp: string): HTMLTimeElement {
  const time = element("time", "timestamp", formatUpdated(timestamp, currentUpdatedDisplay())) as HTMLTimeElement;
  time.dateTime = timestamp;
  time.title = timestamp;
  time.dataset.updatedTimestamp = timestamp;
  return time;
}

function refreshUpdatedPresentation(): void {
  for (const time of app.querySelectorAll<HTMLTimeElement>("time[data-updated-timestamp]")) {
    time.textContent = formatUpdated(time.dataset.updatedTimestamp ?? "", currentUpdatedDisplay());
  }
  for (const input of app.querySelectorAll<HTMLInputElement>("input[data-updated-display]")) {
    input.checked = input.dataset.updatedDisplay === currentUpdatedDisplay();
  }
  const label = updatedDisplayLabel(currentUpdatedDisplay());
  for (const button of app.querySelectorAll<HTMLButtonElement>("button[data-updated-display-button]")) {
    button.title = `Display Updated as ${label}`;
  }
}

function updatedDisplayLabel(display: UpdatedDisplay): string {
  if (display === "relative") return "time since change";
  if (display === "datetime") return "date and time";
  return "date";
}

function updatedDisplayControl(): HTMLElement {
  const id = `updated-display-${updatedControlID++}`;
  const control = element("span", "updated-display-control");
  const button = element("button", "updated-display-button") as HTMLButtonElement;
  button.type = "button";
  button.dataset.updatedDisplayButton = "";
  button.setAttribute("aria-controls", id);
  button.setAttribute("aria-expanded", "false");
  button.setAttribute("aria-haspopup", "dialog");
  button.setAttribute("aria-label", "Choose how Updated is displayed");
  button.title = `Display Updated as ${updatedDisplayLabel(currentUpdatedDisplay())}`;
  button.append(svgIcon("clock"), element("span", "updated-display-chevron", "▾"));

  const popover = element("div", "updated-display-popover");
  popover.id = id;
  popover.setAttribute("popover", "auto");
  popover.setAttribute("role", "dialog");
  popover.setAttribute("aria-label", "Updated display format");
  popover.append(element("strong", "updated-display-title", "Show as"));
  const choices: readonly [UpdatedDisplay, string, string][] = [
    ["relative", "Time since change", "2h 18m ago"],
    ["date", "Date", "2026-08-30"],
    ["datetime", "Date & time", "2026-08-30 16:42"],
  ];
  for (const [value, label, example] of choices) {
    const option = element("label", "updated-display-option");
    const input = document.createElement("input");
    input.type = "radio";
    input.name = id;
    input.value = value;
    input.dataset.updatedDisplay = value;
    input.checked = value === currentUpdatedDisplay();
    input.addEventListener("change", () => {
      if (!input.checked) return;
      updatedDisplay = value;
      rememberUpdatedDisplay(updatedStorage(window), value);
      refreshUpdatedPresentation();
      popover.hidePopover();
    });
    option.append(input, element("span", "", label), element("code", "", example));
    popover.append(option);
  }
  button.addEventListener("click", () => {
    if (popover.matches(":popover-open")) {
      popover.hidePopover();
      return;
    }
    popover.showPopover();
    const anchor = button.getBoundingClientRect();
    const bounds = popover.getBoundingClientRect();
    const gap = 6;
    popover.style.left = `${Math.max(8, Math.min(innerWidth - bounds.width - 8, anchor.right - bounds.width))}px`;
    const below = anchor.bottom + gap;
    popover.style.top = `${below + bounds.height <= innerHeight - 8 ? below : Math.max(8, anchor.top - bounds.height - gap)}px`;
  });
  popover.addEventListener("toggle", () => {
    button.setAttribute("aria-expanded", String(popover.matches(":popover-open")));
  });
  control.append(button, popover);
  return control;
}

function mobileUpdatedDisplayControl(): HTMLElement {
  const control = element("span", "mobile-updated-display");
  control.append(document.createTextNode("Updated"), updatedDisplayControl());
  return control;
}

function clear(node: HTMLElement): void {
  node.replaceChildren();
}

function button(text: string, className = "secondary-button"): HTMLButtonElement {
  const control = element("button", className, text) as HTMLButtonElement;
  control.type = "button";
  return control;
}

function mutationError(host: HTMLElement, error: unknown): void {
  host.querySelector(".edit-error")?.remove();
  const message = error instanceof ApiError ? error.message : String(error);
  host.append(element("p", "edit-error", message));
}

async function mutate(
  host: HTMLElement,
  controls: Iterable<HTMLButtonElement | HTMLInputElement | HTMLSelectElement>,
  operation: () => Promise<unknown>,
): Promise<void> {
  for (const control of controls) control.disabled = true;
  try {
    await operation();
    await render();
  } catch (error) {
    for (const control of controls) control.disabled = false;
    mutationError(host, error);
  }
}

function field(labelText: string, control: HTMLElement): HTMLLabelElement {
  const label = element("label", "edit-field") as HTMLLabelElement;
  label.append(element("span", "edit-field-label", labelText), control);
  return label;
}

function select(values: readonly string[], current = ""): HTMLSelectElement {
  const control = document.createElement("select");
  for (const value of values) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = value;
    option.selected = value === current;
    control.append(option);
  }
  return control;
}

function routeHref(route: Route, query: URLSearchParams): string {
  const suffix = query.toString();
  return `#/${route.path.join("/")}${suffix === "" ? "" : `?${suffix}`}`;
}

function facetHref(route: Route, name: string, value: string): string {
  const query = new URLSearchParams(route.query);
  query.delete("page");
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
  for (const name of issue.assignees) {
    const assignee = element("span", "assignee", `@${name}`);
    if (name === identity) assignee.classList.add("mine");
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
    render: (row) => updatedTimeElement(row.updated_at),
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
    label: "Assignees",
    render: (row) => {
      if (row.assignees.length === 0) return textCell("muted", "—");
      const cell = element("span", "badges");
      for (const name of row.assignees) {
        cell.append(badge(name === identity ? "assignee mine" : "assignee", `@${name}`));
      }
      return cell;
    },
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
    query.delete("page");
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
    query.delete("page");
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
    const controls = element("div", "column-heading");
    controls.append(sortButton(route, column, state, defaultKey, defaultDirection));
    if (column.key === "updated") controls.append(updatedDisplayControl());
    th.append(controls);
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
      : `${visible} on this page`;
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
    : `${visible} on this page`;
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

/** pagination renders links over the backend page. Sorting and selection stay
 * in the URL, while page one remains the canonical form without ?page=1. */
function pagination(route: Route, total: number): HTMLElement {
  const size = listingPageSize(route.query);
  const window = pageWindow(total, pageNumber(route.query), size);

  const nav = element("div", "pagination");
  nav.setAttribute("role", "navigation");
  nav.setAttribute("aria-label", "Pagination");

  const pageLink = (label: string, page: number, disabled: boolean): HTMLAnchorElement => {
    const anchor = link(routeHref(route, withPage(route.query, page)), label, "pagination-link");
    if (disabled) {
      anchor.removeAttribute("href");
      anchor.setAttribute("aria-disabled", "true");
    }
    return anchor;
  };
  const first = pageLink("First", 1, window.page === 1);
  const previous = pageLink("Previous", window.page - 1, window.page === 1);
  const status = element(
    "span",
    "pagination-status",
    total === 0 ? "0 of 0" : `${window.first}–${window.last} of ${total}`,
  );
  const atEnd = window.page === window.pages;
  const next = pageLink("Next", window.page + 1, atEnd);
  const last = pageLink("Last", window.pages, atEnd);

  const sizeControl = element("label", "pagination-size");
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Rows per page");
  select.title = "Rows per page";
  for (const optionSize of pageSizes) {
    const option = document.createElement("option");
    option.value = String(optionSize);
    option.textContent = String(optionSize);
    option.selected = optionSize === size;
    select.append(option);
  }
  select.addEventListener("change", () => {
    const selectedSize = Number(select.value);
    rememberPageSize(paginationStorage, selectedSize);
    location.hash = routeHref(route, withPageSize(route.query, selectedSize)).slice(1);
  });
  sizeControl.append(select);

  nav.append(first, previous, status, next, last, sizeControl);
  return nav;
}

function normalizePageRoute(route: Route, total: number): number {
  const requested = pageNumber(route.query);
  const normalized = pageWindow(total, requested, listingPageSize(route.query)).page;
  if (normalized !== requested) {
    route.query = withPage(route.query, normalized);
    history.replaceState(null, "", routeHref(route, route.query));
  }
  return normalized;
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
  if (columns.some((column) => column.key === "updated")) {
    listingActions.append(mobileUpdatedDisplayControl());
  }

  const update = (query: string): number => {
    // Ordering belongs to the backend: otherwise sorting a page locally would
    // produce a different order from sorting the complete result set.
    const rows = filterIssues(issues, query);
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
    `Filter this page of ${kind === "search" ? "results" : kind}…`,
    "issue",
    total,
    update,
    listingActions,
    kind === "issues" || kind === "search" ? includeClosedControl(route) : null,
  ));
  if (facets !== null) section.append(facets);
  section.append(pagination(route, total), tableHost);
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
  const sort = query.get("sort");
  const apiSorts: string[] = issueSortKeys
    .filter((key) => key !== "relevance")
    .flatMap((key) => [key, `-${key}`]);
  if (allowRelevance) apiSorts.push("relevance", "-relevance");
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as Filters["sort"];
  const size = listingPageSize(query);
  filters.limit = size;
  filters.offset = (pageNumber(query) - 1) * size;
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

  const load = () => kind === "ready"
    ? api.ready(readyFilters(filters))
    : kind === "blocked"
      ? api.blocked(blockedFilters(filters))
      : api.issues(filters);

  // Each listing is asked with the filters it accepts. Ready lists only
  // unassigned issues, so there is no assignee menu to offer there either.
  let [page, projects, labels, assignees] = await Promise.all([
    load(),
    api.projects(),
    api.labels(kind === "ready" ? {} : facetFilters(filters)),
    kind === "ready" ? Promise.resolve({ rows: [], total: 0 }) : api.assignees(facetFilters(filters)),
  ]);
  const normalized = normalizePageRoute(route, page.total);
  const size = listingPageSize(route.query);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await load();
  }

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
    query.delete("page");
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
    const controls = element("div", "column-heading");
    controls.append(projectSortButton(route, column.key, column.label, state));
    if (column.key === "updated") controls.append(updatedDisplayControl());
    th.append(controls);
    heading.append(th);
  }
  head.append(heading);
  table.append(head);

  const body = document.createElement("tbody");
  for (const project of projects) {
    const row = document.createElement("tr");
    const href = `#/projects/${encodeURIComponent(project.key)}`;

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
    updated.append(updatedTimeElement(project.updated_at));
    row.append(updated);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewProjects(route: Route): Promise<HTMLElement> {
  const requested = pageNumber(route.query);
  const size = listingPageSize(route.query);
  const filters: ProjectFilters = {
    limit: size,
    offset: (requested - 1) * size,
  };
  const sort = route.query.get("sort");
  const apiSorts = projectSortKeys.flatMap((key) => [key, `-${key}`]);
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as ProjectFilters["sort"];
  let page = await api.projects(filters);
  const normalized = normalizePageRoute(route, page.total);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.projects(filters);
  }

  const view = element("div");
  view.append(element("h1", "", "Projects"));

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  const state = sortState(route.query.get("sort"), projectSortKeys, "key");
  const listingActions = element("div", "listing-actions");
  listingActions.append(
    mobileSortControl(route, projectColumns, "Natural order"),
    mobileUpdatedDisplayControl(),
  );
  const update = (query: string): number => {
    const rows = filterProjects(page.rows, query);
    clear(host);
    if (rows.length === 0) {
      host.append(element("p", "empty", query.trim() === ""
        ? "No projects yet. Create one with: awb project create <key>"
        : "No projects match this filter."));
    }
    else host.append(projectTable(route, rows, state));
    return rows.length;
  };
  listing.append(listingFilter(
    route,
    "Filter this page of projects…",
    "project",
    page.total,
    update,
    listingActions,
  ));
  listing.append(pagination(route, page.total), host);
  view.append(listing);
  return view;
}

function userTable(users: DirectoryUser[]): HTMLElement {
  const table = element("table", "listing-table user-table") as HTMLTableElement;
  const head = document.createElement("thead");
  const heading = document.createElement("tr");
  for (const label of ["User", "Memberships", "Activity", "Roles"]) {
    const th = document.createElement("th");
    th.scope = "col";
    th.textContent = label;
    heading.append(th);
  }
  head.append(heading);
  table.append(head);

  const body = document.createElement("tbody");
  for (const user of users) {
    const row = document.createElement("tr");
    const userCell = document.createElement("td");
    userCell.dataset.label = "User";
    const name = element("span", "user-name");
    name.append(avatar(user.name), document.createTextNode(user.name));
    userCell.append(name);
    row.append(userCell);

    const memberships = document.createElement("td");
    memberships.dataset.label = "Memberships";
    const membershipList = element("div", "user-projects");
    for (const membership of user.projects) {
      const project = element("span", "listing-badge user-project", membership.project);
      project.title = `${membership.access} access`;
      membershipList.append(project);
    }
    if (user.projects.length === 0) membershipList.append(element("span", "muted", "—"));
    memberships.append(membershipList);
    row.append(memberships);

    const activity = document.createElement("td");
    activity.dataset.label = "Activity";
    const activityList = element("div", "user-projects");
    for (const projectName of user.activity_projects) {
      activityList.append(element("span", "listing-badge user-project", projectName));
    }
    if (user.activity_projects.length === 0) activityList.append(element("span", "muted", "—"));
    activity.append(activityList);
    row.append(activity);

    const roles = document.createElement("td");
    roles.dataset.label = "Roles";
    const roleList = element("div", "user-roles");
    if (user.project_admin) roleList.append(element("span", "listing-badge", "project admin"));
    if (user.user_admin) roleList.append(element("span", "listing-badge", "user admin"));
    if (!user.project_admin && !user.user_admin) roleList.append(element("span", "muted", "member"));
    roles.append(roleList);
    row.append(roles);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewUsers(route: Route): Promise<HTMLElement> {
  const requested = pageNumber(route.query);
  const size = listingPageSize(route.query);
  const filters: UserFilters = {
    limit: size,
    offset: (requested - 1) * size,
  };
  let page = await api.users(filters);
  const normalized = normalizePageRoute(route, page.total);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.users(filters);
  }
  const view = element("div");
  view.append(element("h1", "", "Users"));

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  const update = (query: string): number => {
    const rows = filterUsers(page.rows, query);
    clear(host);
    if (rows.length === 0) {
      host.append(element("p", "empty", query.trim() === "" ? "No users yet." : "No users match this filter."));
    } else {
      host.append(userTable(rows));
    }
    return rows.length;
  };
  listing.append(
    listingFilter(route, "Filter this page of users…", "user", page.total, update),
    pagination(route, page.total),
    host,
  );
  view.append(listing);
  return view;
}

async function viewProject(key: string): Promise<HTMLElement> {
  const project = await api.project(key);
  const view = element("div", "project-view");
  const heading = element("div", "detail-heading");
  const title = element("div");
  title.append(element("div", "issue-key", project.key), element("h1", "", project.name));
  const edit = button("Edit project");
  heading.append(title, edit);
  view.append(heading);

  const form = projectEditForm(project);
  form.hidden = true;
  edit.addEventListener("click", () => {
    form.hidden = !form.hidden;
    edit.textContent = form.hidden ? "Edit project" : "Hide editor";
    if (!form.hidden) form.querySelector<HTMLInputElement>("input")?.focus();
  });
  view.append(form);

  const description = element("section", "project-detail-description");
  description.append(element("h2", "", "Description"));
  if (project.description === "") description.append(element("p", "empty", "No description."));
  else {
    const body = element("div", "markdown");
    body.innerHTML = renderMarkdown(project.description);
    description.append(body);
  }
  view.append(description);
  const facts = element("p", "project-facts");
  facts.append(
    document.createTextNode(`${project.active_issues} open issue${project.active_issues === 1 ? "" : "s"} · Updated `),
    updatedTimeElement(project.updated_at),
  );
  view.append(facts, link(
    `#/issues?project=${encodeURIComponent(project.key)}`,
    "View this project's issues",
    "action",
  ));
  return view;
}

function projectEditForm(project: Project): HTMLFormElement {
  const form = element("form", "edit-panel") as HTMLFormElement;
  form.append(element("h2", "", "Edit project"));
  const name = document.createElement("input");
  name.value = project.name;
  name.maxLength = 500;
  const description = document.createElement("textarea");
  description.value = project.description;
  description.rows = 10;
  const save = element("button", "primary-button", "Save changes") as HTMLButtonElement;
  save.type = "submit";
  form.append(field("Name", name), field("Description (Markdown)", description), save);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [save], () => api.updateProject(project.key, {
      name: name.value,
      description: description.value,
    }));
  });
  return form;
}

async function viewIssue(id: string): Promise<HTMLElement> {
  const [issue, activity] = await Promise.all([api.issue(id), api.activity(id)]);

  const view = element("div", "issue-view");
  view.classList.toggle("sidebar-collapsed", issueSidebarCollapsed(issueSidebarStorage(window)));
  const content = element("div", "issue-content");
  const heading = element("div", "issue-heading");
  const headingText = element("div", "issue-heading-text");
  headingText.append(element("div", "issue-key", issue.id), element("h1", "", issue.title));
  const editButton = button("Edit issue");
  heading.append(headingText, editButton);
  content.append(heading);

  const editForm = issueEditForm(issue);
  editForm.hidden = true;
  editButton.addEventListener("click", () => {
    editForm.hidden = !editForm.hidden;
    editButton.textContent = editForm.hidden ? "Edit issue" : "Hide editor";
    if (!editForm.hidden) editForm.querySelector<HTMLInputElement>("input")?.focus();
  });
  content.append(editForm);

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
    for (const attachment of issue.attachments) list.append(attachmentRow(attachment, true));
    content.append(list);
  }
  content.append(attachmentEditor(issue.id));

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
      const remove = button("Remove", "inline-button danger-button");
      remove.addEventListener("click", () => {
        const addressed = relation.direction === "in" ? relation.other : issue.id;
        const addressedOther = relation.direction === "in" ? issue.id : relation.other;
        void mutate(row, [remove], () => api.removeRelation(addressed, relation.type, addressedOther));
      });
      row.append(remove);
      list.append(row);
    }
    content.append(list);
  }
  content.append(relationEditor(issue.id));

  content.append(link(`#/tree/${issue.id}`, "Show the decomposition below this issue", "action"));
  content.append(activitySection(issue.id, activity.rows));
  const [sidebar, sidebarToggle] = issueSidebar(issue, view);
  view.append(content, sidebar, sidebarToggle);
  return view;
}

function issueEditForm(issue: Issue): HTMLFormElement {
  const form = element("form", "edit-panel issue-edit-form") as HTMLFormElement;
  form.append(element("h2", "", "Edit issue"));

  const title = document.createElement("input");
  title.name = "title";
  title.value = issue.title;
  title.required = true;
  title.maxLength = 500;

  const description = document.createElement("textarea");
  description.name = "description";
  description.value = issue.description;
  description.rows = 10;

  const type = select(["epic", "feature", "bug", "task", "chore"], issue.type);
  type.name = "type";
  const priority = select(["0", "1", "2", "3", "4"], String(issue.priority));
  priority.name = "priority";

  const row = element("div", "edit-field-row");
  row.append(field("Type", type), field("Priority", priority));
  const save = element("button", "primary-button", "Save changes") as HTMLButtonElement;
  save.type = "submit";
  form.append(field("Title", title), field("Description (Markdown)", description), row, save);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [save], () => api.updateIssue(issue.id, {
      title: title.value,
      description: description.value,
      type: type.value as Issue["type"],
      priority: Number(priority.value),
    }));
  });
  return form;
}

function relationEditor(issueID: string): HTMLFormElement {
  const form = element("form", "compact-editor") as HTMLFormElement;
  const type = select(["blocked-by", "has-parent", "discovered-from", "related"]);
  type.setAttribute("aria-label", "Relation type");
  const other = document.createElement("input");
  other.placeholder = "Other issue ID";
  other.setAttribute("aria-label", "Other issue ID");
  other.required = true;
  const forceLabel = element("label", "check-field");
  const force = document.createElement("input");
  force.type = "checkbox";
  forceLabel.append(force, document.createTextNode("Replace parent"));
  const add = element("button", "primary-button", "Add relation") as HTMLButtonElement;
  add.type = "submit";
  form.append(type, other, forceLabel, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [add], () => api.addRelation(issueID, {
      type: type.value as Relation["type"],
      other: other.value,
      force: force.checked,
    }));
  });
  return form;
}

function attachmentEditor(issueID: string): HTMLFormElement {
  const form = element("form", "compact-editor attachment-editor") as HTMLFormElement;
  const file = document.createElement("input");
  file.type = "file";
  file.required = true;
  file.setAttribute("aria-label", "Attachment file");
  const upload = element("button", "primary-button", "Attach file") as HTMLButtonElement;
  upload.type = "submit";
  form.append(file, upload);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (file.files?.[0] === undefined) return;
    void mutate(form, [upload], () => api.addAttachment(issueID, file.files![0]));
  });
  return form;
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
  add("Project", link(`#/projects/${encodeURIComponent(issue.project)}`, issue.project));
  const type = select(["epic", "feature", "bug", "task", "chore"], issue.type);
  type.className = "sidebar-select";
  type.setAttribute("aria-label", "Edit type");
  type.addEventListener("change", () => {
    void mutate(aside, [type], () => api.updateIssue(issue.id, { type: type.value as Issue["type"] }));
  });
  add("Type", type);
  add("Status", badge(`status status-${issue.status}`, issue.status));
  const priority = select(["0", "1", "2", "3", "4"], String(issue.priority));
  priority.className = `sidebar-select priority p${issue.priority}`;
  priority.setAttribute("aria-label", "Edit priority");
  priority.addEventListener("change", () => {
    void mutate(aside, [priority], () => api.updateIssue(issue.id, { priority: Number(priority.value) }));
  });
  add("Priority", priority);
  if (issue.blocked) add("Readiness", badge("blocked", "blocked"));

  const labels = element("span", "sidebar-labels");
  if (issue.labels.length === 0) labels.append(text("—"));
  for (const label of issue.labels) {
    const chip = element("span", "editable-chip");
    chip.append(badge("label", label));
    const remove = button("×", "chip-remove");
    remove.title = `Remove label ${label}`;
    remove.setAttribute("aria-label", remove.title);
    remove.addEventListener("click", () => {
      void mutate(chip, [remove], () => api.removeLabel(issue.id, label));
    });
    chip.append(remove);
    labels.append(chip);
  }
  add("Labels", labels);
  add("Assignees", text(issue.assignees.map((name) => `@${name}`).join(", ")));
  add("Created", timeElement(issue.created_at));
  add("Updated", timeElement(issue.updated_at));
  aside.append(facts, labelEditor(issue.id), lifecycleEditor(issue));
  drawToggle();
  return [aside, toggle];
}

function labelEditor(issueID: string): HTMLFormElement {
  const form = element("form", "sidebar-editor") as HTMLFormElement;
  const input = document.createElement("input");
  input.placeholder = "Add label";
  input.setAttribute("aria-label", "Label");
  input.required = true;
  const add = element("button", "primary-button", "Add") as HTMLButtonElement;
  add.type = "submit";
  form.append(input, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [add], () => api.addLabel(issueID, input.value));
  });
  return form;
}

function lifecycleEditor(issue: Issue): HTMLElement {
  const section = element("section", "lifecycle-editor");
  section.append(element("h2", "", "Status and assignees"));

  const claim = element("form", "sidebar-editor") as HTMLFormElement;
  const assignee = document.createElement("input");
  assignee.placeholder = identity === "" ? "Assignee" : `Assignee (default: ${identity})`;
  assignee.setAttribute("aria-label", "Assignee");
  const forceLabel = element("label", "check-field");
  const force = document.createElement("input");
  force.type = "checkbox";
  forceLabel.append(force, document.createTextNode("Force"));
  const claimButton = element(
    "button",
    "primary-button",
    issue.assignees.length === 0 ? "Claim" : "Add assignee",
  ) as HTMLButtonElement;
  claimButton.type = "submit";
  claim.append(assignee, forceLabel, claimButton);
  claim.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(section, [claimButton], () => api.claimIssue(issue.id, {
      assignee: assignee.value === "" ? undefined : assignee.value,
      force: force.checked,
    }));
  });
  section.append(claim);

  const actions = element("div", "lifecycle-actions");
  if (issue.status === "in_progress") {
    const releaseForm = element("form", "release-editor") as HTMLFormElement;
    const releaseForceLabel = element("label", "check-field");
    const releaseForce = document.createElement("input");
    releaseForce.type = "checkbox";
    releaseForceLabel.append(releaseForce, document.createTextNode("Force release"));
    const release = element("button", "secondary-button", "Release") as HTMLButtonElement;
    release.type = "submit";
    releaseForm.append(releaseForceLabel, release);
    releaseForm.addEventListener("submit", (event) => {
      event.preventDefault();
      void mutate(section, [release], () => api.releaseIssue(issue.id, {
        assignee: identity,
        force: releaseForce.checked,
      }));
    });
    actions.append(releaseForm);
  }
  if (issue.status === "closed") {
    const reopen = button("Reopen");
    reopen.addEventListener("click", () => {
      void mutate(section, [reopen], () => api.reopenIssue(issue.id));
    });
    actions.append(reopen);
  } else {
    const close = element("form", "close-editor") as HTMLFormElement;
    const reason = document.createElement("input");
    reason.placeholder = "Close reason (optional)";
    reason.maxLength = 500;
    reason.setAttribute("aria-label", "Close reason");
    const closeButton = element("button", "danger-button", "Close") as HTMLButtonElement;
    closeButton.type = "submit";
    close.append(reason, closeButton);
    close.addEventListener("submit", (event) => {
      event.preventDefault();
      void mutate(section, [closeButton], () => api.closeIssue(
        issue.id,
        reason.value === "" ? {} : { reason: reason.value },
      ));
    });
    actions.append(close);
  }
  section.append(actions);
  return section;
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
function attachmentRow(attachment: Attachment, editable = false): HTMLElement {
  const row = element("li");
  const href =
    `api/issues/${encodeURIComponent(attachment.issue)}` +
    `/attachments/${encodeURIComponent(attachment.name)}/content`;
  row.append(link(href, attachment.name));
  row.append(element("span", "size", formatSize(attachment.size)));
  row.append(element("span", "content-type", attachment.content_type));
  if (editable) {
    const remove = button("Remove", "inline-button danger-button");
    remove.addEventListener("click", () => {
      void mutate(row, [remove], () => api.removeAttachment(attachment.issue, attachment.name));
    });
    row.append(remove);
  }
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

  const filters = { ...filtersFrom(route.query, true), q: terms };
  let [page, projects] = await Promise.all([
    api.search(filters),
    api.projects(),
  ]);
  const normalized = normalizePageRoute(route, page.total);
  const size = listingPageSize(route.query);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.search(filters);
  }
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

/** accountMenu makes the identity in the upper-right a real navigation
 * control while keeping the avatar and name as its accessible label. */
function accountMenu(): HTMLElement {
  const account = element("span", "account-menu");
  const button = element("button", "identity") as HTMLButtonElement;
  button.type = "button";
  button.setAttribute("aria-label", `Open menu for @${identity}`);
  button.setAttribute("aria-expanded", "false");
  button.setAttribute("aria-haspopup", "menu");
  button.append(avatar(identity), element("span", "identity-name", `@${identity}`));

  const menu = element("div", "account-popover");
  menu.setAttribute("popover", "auto");
  menu.setAttribute("role", "menu");
  const profile = link("#/profile", "Profile", "account-menu-item");
  profile.setAttribute("role", "menuitem");
  menu.append(profile);

  button.addEventListener("click", () => {
    if (menu.matches(":popover-open")) {
      menu.hidePopover();
      return;
    }
    menu.showPopover();
    const anchor = button.getBoundingClientRect();
    const bounds = menu.getBoundingClientRect();
    menu.style.left = `${Math.max(8, Math.min(innerWidth - bounds.width - 8, anchor.right - bounds.width))}px`;
    menu.style.top = `${anchor.bottom + 6}px`;
  });
  menu.addEventListener("toggle", () => {
    button.setAttribute("aria-expanded", String(menu.matches(":popover-open")));
  });
  account.append(button, menu);
  return account;
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
  nav.append(navLink("#/users", "Users", "users"));
  const brand = link("#/ready", "", "brand");
  const mark = document.createElement("img");
  mark.src = "awb-mark.png";
  mark.alt = "";
  mark.className = "brand-mark";
  brand.append(mark, document.createTextNode("Agent Work Board"));
  header.append(brand);
  header.append(nav);
  header.append(searchBox());
  if (identity !== "") header.append(accountMenu());
  return header;
}

function profileProjectList(user: User, projects: Project[]): HTMLElement {
  const list = element("ul", "profile-projects");
  const memberships = new Map(user.projects.map((membership) => [membership.project, membership.access]));
  for (const project of projects) {
    const item = element("li", "profile-project");
    const query = new URLSearchParams({ project: project.key });
    item.append(link(`#/issues?${query.toString()}`, project.key, "profile-project-name"));
    if (project.name !== "") item.append(element("span", "profile-project-title", project.name));
    const access = user.project_admin ? "admin" : memberships.get(project.key);
    if (access !== undefined) item.append(element("span", "listing-badge", access));
    list.append(item);
  }
  if (projects.length === 0) list.append(element("li", "empty", "No project access."));
  return list;
}

function passwordForm(user: User): HTMLFormElement {
  const form = element("form", "profile-password-form") as HTMLFormElement;
  const passwordID = "profile-new-password";
  const confirmationID = "profile-confirm-password";
  const password = document.createElement("input");
  password.id = passwordID;
  password.type = "password";
  password.name = "password";
  password.required = true;
  password.maxLength = 72;
  password.autocomplete = "new-password";
  const confirmation = password.cloneNode() as HTMLInputElement;
  confirmation.id = confirmationID;
  confirmation.name = "confirmation";
  const submit = element("button", "profile-submit", "Change password") as HTMLButtonElement;
  submit.type = "submit";
  const message = element("p", "profile-form-message");
  message.setAttribute("aria-live", "polite");
  form.append(
    element("label", "", "New password"),
    password,
    element("label", "", "Confirm new password"),
    confirmation,
    submit,
    message,
  );
  (form.children[0] as HTMLLabelElement).htmlFor = passwordID;
  (form.children[2] as HTMLLabelElement).htmlFor = confirmationID;

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    message.className = "profile-form-message";
    if (password.value !== confirmation.value) {
      message.classList.add("form-error");
      message.textContent = "The passwords do not match.";
      confirmation.focus();
      return;
    }
    submit.disabled = true;
    message.textContent = "";
    try {
      await api.updateUser(user.name, { password: password.value });
      form.reset();
      message.textContent = "Password changed.";
    } catch (error) {
      message.classList.add("form-error");
      message.textContent = error instanceof ApiError ? error.message : String(error);
    } finally {
      submit.disabled = false;
    }
  });
  return form;
}

async function viewProfile(): Promise<HTMLElement> {
  if (identity === "") throw new Error("No authenticated user is available.");
  const [user, projects] = await Promise.all([api.user(identity), api.projects()]);
  const view = element("div", "profile-view");
  const heading = element("div", "profile-heading");
  heading.append(avatar(user.name, "profile-avatar"));
  const title = element("div");
  title.append(element("h1", "", `@${user.name}`), element("p", "lede", "Your account and access"));
  heading.append(title);
  view.append(heading);

  const roles = element("div", "profile-roles");
  for (const role of accountRoles(user)) roles.append(element("span", "listing-badge", role));
  const details = element("section", "profile-card");
  details.append(element("h2", "", "Account status"), roles);
  const facts = element("dl", "profile-facts");
  const fact = (label: string, value: string): void => {
    facts.append(element("dt", "", label), element("dd", "", value));
  };
  fact("Username", user.name);
  fact("Created", user.created_at);
  fact("Updated", user.updated_at);
  details.append(facts);

  const access = element("section", "profile-card");
  access.append(element("h2", "", "Project access"), profileProjectList(user, projects.rows));
  const security = element("section", "profile-card");
  security.append(element("h2", "", "Password"), passwordForm(user));
  view.append(details, access, security);
  return view;
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
      return route.path.length > 1 ? viewProject(route.path[1]) : viewProjects(route);
    case "profile":
      return viewProfile();
    case "users":
      return viewUsers(route);
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
