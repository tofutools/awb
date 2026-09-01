// The bundled web UI: workspaces, issues, dependency trees and editing,
// over the same HTTP API anything else would use.

import {
  api,
  ApiError,
  blockedFilters,
  facetFilters,
  readyFacetFilters,
  readyFilters,
  type Attachment,
  type Activity,
  type Facet,
  type Filters,
  type DirectoryUser,
  type Issue,
  type IssueTree,
  type Membership,
  type Project,
  type ProjectActivity,
  type ProjectPreference,
  type ProjectFilters,
  type User,
  type UserFilters,
  type Relation,
} from "./api.js";
import {
  BackendListingFilter,
  emptyFacetLabel,
  lowestFacetGroup,
  listingFilterMaxLength,
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
import { commentSubmitShortcut, issueEditorShortcut } from "./keyboard.js";
import {
  CommandPalette,
  CommandRegistry,
  paletteShortcutHint,
  paletteTrigger,
  type PaletteCommand,
} from "./command-palette.js";
import { renderMarkdown } from "./markdown.js";
import { activityValues, initialFor, relativeTime } from "./presentation.js";
import { issueSidebarCollapsed, issueSidebarStorage, rememberIssueSidebar } from "./sidebar.js";
import {
  legacyIssueSearchHref,
  namedDestinations,
  navigationPath,
  projectScopedHref,
} from "./navigation.js";
import { accountRoles, profileIdentity, saveProfileFullName } from "./profile.js";
import { attachAutocomplete, type Suggestion } from "./autocomplete.js";
import {
  mayManageProjectMembership,
  membershipAdditionError,
  membershipChangeConfirmation,
  membershipSuggestions,
} from "./membership.js";
import { inspectorPopoverPosition, inspectorStatusAction } from "./inspector.js";
import { attachSearchClear } from "./search-control.js";
import {
  accountMenuItems,
  preferenceStorage,
  readPaginationAutoHide,
  rememberPaginationAutoHide,
  filterProjectPreferences,
  showPagination,
  projectPreferenceSummary,
} from "./preferences.js";
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
let inspectorPopoverID = 0;
const preferences = preferenceStorage(window);
let paginationAutoHide = readPaginationAutoHide(preferences);
const paginationStorage = pageSizeStorage(window);
let commandPalette: CommandPalette | null = null;
let activeListingFilter: BackendListingFilter<HTMLElement> | null = null;
const listingFilterOwners = new WeakMap<HTMLElement, BackendListingFilter<HTMLElement>>();
let activeRenderRequest: AbortController | null = null;
let renderGeneration = 0;
let projectManager: boolean | null = null;

async function mayManageProjects(): Promise<boolean> {
  if (projectManager !== null) return projectManager;
  if (identity === "") return true;
  try {
    projectManager = (await api.user(identity)).project_admin;
  } catch (error) {
    // A server with no account rows is unrestricted even though it still has
    // an attribution identity for audit entries.
    if (error instanceof ApiError && error.status === 404) projectManager = true;
    else throw error;
  }
  return projectManager;
}
let pendingNotice: { message: string; error: boolean } | null = null;

function listingPageSize(query: URLSearchParams): number {
  return pageSizeFrom(query, rememberedPageSize(paginationStorage));
}

interface IssueEditDraft {
  title: string;
  description: string;
}

// Sidebar and resource edits save immediately and therefore rerender the
// route. Keep the main form's unsaved text through that rerender instead of
// silently replacing it with the last stored issue.
const issueEditDrafts = new Map<string, IssueEditDraft>();

function parseRoute(): Route {
  const hash = location.hash.replace(/^#\/?/, "");
  const [path, query] = hash.split("?", 2);
  const route = {
    path: path.split("/").filter((segment) => segment !== ""),
    query: new URLSearchParams(query ?? ""),
  };
  const filter = route.query.get("filter");
  if (filter !== null && filter.length > listingFilterMaxLength) {
    route.query.set("filter", filter.slice(0, listingFilterMaxLength));
    history.replaceState(null, "", routeHref(route, route.query));
  }
  return route;
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

type IconName = "attachment" | "blocked" | "change" | "clock" | "info" | "issues" | "projects" | "ready" | "relation" | "search" | "tag" | "users";

/** svgIcon keeps the small, decorative interface icons in the document rather
 * than adding another asset pipeline or network request. */
function svgIcon(name: IconName): SVGSVGElement {
  const paths: Record<IconName, string> = {
    attachment: '<path d="m21.4 11.6-8.5 8.5a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5"></path>',
    blocked: '<circle cx="12" cy="12" r="9"></circle><path d="m5.7 5.7 12.6 12.6"></path>',
    change: '<path d="M7 7h11l-3-3m3 3-3 3"></path><path d="M17 17H6l3 3m-3-3 3-3"></path>',
    clock: '<circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path>',
    info: '<circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path>',
    issues: '<path d="M6 3h8l4 4v14H6z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>',
    projects: '<path d="M3 6h7l2 2h9v11H3z"></path>',
    ready: '<path d="m5.5 5.1-3.5 6.9v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z"></path><path d="M2 12h6l2 3h4l2-3h6"></path>',
    relation: '<path d="M10 13a5 5 0 0 0 7.1 0l2-2A5 5 0 0 0 12 3.9L10.9 5"></path><path d="M14 11a5 5 0 0 0-7.1 0l-2 2A5 5 0 0 0 12 20.1l1.1-1.1"></path>',
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
  const notice = element("p", "edit-error", message);
  notice.setAttribute("role", "alert");
  notice.setAttribute("aria-live", "assertive");
  host.append(notice);
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

type ListingKind = "issues" | "ready" | "blocked";

interface SortChoice {
  key: string;
  label: string;
}

interface IssueColumn extends SortChoice {
  render: (issue: Issue) => HTMLElement;
}

const issueSortKeys = [
  "id", "project", "priority", "status", "assignee", "created", "updated", "type", "blockers",
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
    label: "Workspace",
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
        render: (row) => textCell(
          "blocker-list",
          row.blockers.length === 0 && row.blocked ? "hidden work" : row.blockers.join(", "),
        ),
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
  trailingControl: HTMLElement | null = null,
  adjacentControl: HTMLElement | null = null,
): HTMLElement {
  const bar = element("div", "listing-tools");
  const control = element("div", "search-control listing-filter");
  const input = document.createElement("input");
  input.maxLength = listingFilterMaxLength;
  input.placeholder = placeholder;
  input.setAttribute("aria-label", placeholder);
  input.value = route.query.get("filter") ?? "";
  const count = element("span", "filter-count");
  bar.append(control, count);
  if (adjacentControl !== null) bar.append(adjacentControl);
  if (trailingControl !== null) bar.append(trailingControl);

  const search = new BackendListingFilter(
    async (_query, signal) => routeView(route, signal),
    (view) => {
      const main = app.querySelector("main");
      if (main === null) return;
      clear(main);
      main.append(view);
      activateListingFilter(view);
      markActiveNav(route);
      const next = main.querySelector<HTMLInputElement>(".listing-filter input");
      next?.focus();
      next?.setSelectionRange(next.value.length, next.value.length);
    },
    (error) => showRouteError(error),
  );
  listingFilterOwners.set(bar, search);

  const refresh = (immediate = false): void => {
    const query = new URLSearchParams(route.query);
    query.delete("page");
    if (route.path[0] === "users") query.delete("user");
    if (input.value === "") query.delete("filter");
    else query.set("filter", input.value);
    route.query = query;
    history.replaceState(null, "", routeHref(route, query));
    refreshFacetHrefs(route);
    count.textContent = "Filtering…";
    search.query(input.value, immediate);
  };
  input.addEventListener("input", () => refresh());
  const clearControl = attachSearchClear(input, () => {
    refresh(true);
  });
  clearControl.button.title = "Clear filter";
  clearControl.button.setAttribute("aria-label", "Clear filter");
  control.append(input, clearControl.button);

  count.textContent = `${total} ${noun}${total === 1 ? "" : "s"}`;
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
  nav.hidden = !showPagination(total, paginationAutoHide);

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
  const defaultKey = "priority";
  const defaultDirection: SortDirection = "asc";
  const state = sortState(route.query.get("sort"), issueSortKeys, defaultKey, defaultDirection);
  const columns = issueColumns(kind);
  const mobileColumns = [...columns, { key: "created", label: "Created" }];
  const listingActions = element("div", "listing-actions");
  listingActions.append(mobileSortControl(
    route,
    mobileColumns,
    "Natural order",
  ));
  if (columns.some((column) => column.key === "updated")) {
    listingActions.append(mobileUpdatedDisplayControl());
  }
  if (issues.length === 0) {
    tableHost.append(element("p", "empty", route.query.get("filter") === null
      ? emptyMessage : "No issues match this filter."));
  } else {
    tableHost.append(issueTable(route, issues, kind, state, defaultKey, defaultDirection));
  }

  section.append(listingFilter(
    route,
    `Filter all ${kind}…`,
    "issue",
    total,
    listingActions,
    kind === "issues" ? includeClosedControl(route) : null,
  ));
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
  if (query.get("include-archived") === "true") filters["include-archived"] = true;
  const listingFilter = query.get("filter");
  if (listingFilter !== null && listingFilter !== "") filters.filter = listingFilter;
  const sort = query.get("sort");
  const apiSorts: string[] = issueSortKeys.flatMap((key) => [key, `-${key}`]);
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
  paginationControl: HTMLElement,
): HTMLElement {
  const bar = element("div", "facets");
  const paginationGroup = lowestFacetGroup(labels, assignees);

  const projectGroup = element("div", "facet-group projects");
  const projectValues = element("span", "facet-values");
  projectGroup.append(element("span", "facet-title", "workspaces"), projectValues);
  const projectEmpty = emptyFacetLabel(projects);
  if (projectEmpty !== null) {
    projectValues.append(element("span", "facet-empty", projectEmpty));
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
      projectValues.append(anchor);
    }
  }
  if (paginationGroup === "project") {
    projectGroup.classList.add("with-pagination");
    projectGroup.append(paginationControl);
  }
  bar.append(projectGroup);

  const build = (name: string, title: string, facets: Facet[] | null): HTMLElement | null => {
    if (facets === null) return null;
    const group = element("div", "facet-group");
    const values = element("span", "facet-values");
    group.append(element("span", "facet-title", title), values);
    const empty = emptyFacetLabel(facets);
    if (empty !== null) {
      values.append(element("span", "facet-empty", empty));
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
      values.append(anchor);
    }
    return group;
  };

  const labelGroup = build("label", "labels", labels);
  if (labelGroup !== null) {
    if (paginationGroup === "label") {
      labelGroup.classList.add("with-pagination");
      labelGroup.append(paginationControl);
    }
    bar.append(labelGroup);
  }
  const assigneeGroup = build("assignee", "assignees", assignees);
  if (assigneeGroup !== null) {
    if (paginationGroup === "assignee") {
      assigneeGroup.classList.add("with-pagination");
      assigneeGroup.append(paginationControl);
    }
    bar.append(assigneeGroup);
  }
  return bar;
}

async function viewListing(
  route: Route,
  kind: "issues" | "ready" | "blocked",
  signal?: AbortSignal,
): Promise<HTMLElement> {
  const filters = filtersFrom(route.query);

  const load = () => kind === "ready"
    ? api.ready(readyFilters(filters), signal)
    : kind === "blocked"
      ? api.blocked(blockedFilters(filters), signal)
      : api.issues(filters, signal);

  // Each listing is asked with the filters it accepts. Ready lists only
  // unassigned issues, so there is no assignee menu to offer there either.
  let [page, projects, labels, assignees] = await Promise.all([
    load(),
    api.projects(filters["include-archived"] ? { state: "all" } : {}, signal),
    api.labels(kind === "ready" ? readyFacetFilters(filters) : facetFilters(filters), signal),
    kind === "ready" ? Promise.resolve({ rows: [], total: 0 }) : api.assignees(facetFilters(filters), signal),
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
    facetBar(
      route,
      projects.rows,
      labels.rows,
      kind === "ready" ? null : assignees.rows,
      pagination(route, page.total),
    ),
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
  { key: "key", label: "Workspace" },
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
    const href = `#/workspaces/${encodeURIComponent(project.key)}`;

    const projectCell = document.createElement("td");
    projectCell.dataset.label = "Workspace";
    projectCell.append(nameLink(href, project.key, project.name));
    if (project.state === "archived") projectCell.append(element("span", "listing-badge archived-badge", "Archived"));
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

async function viewProjects(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  const requested = pageNumber(route.query);
  const size = listingPageSize(route.query);
  const filters: ProjectFilters = {
    limit: size,
    offset: (requested - 1) * size,
  };
  const lifecycle = route.query.get("state") === "archived" ? "archived" : "active";
  filters.state = lifecycle;
  const filterText = route.query.get("filter");
  if (filterText !== null && filterText !== "") filters.filter = filterText;
  const sort = route.query.get("sort");
  const apiSorts = projectSortKeys.flatMap((key) => [key, `-${key}`]);
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as ProjectFilters["sort"];
  let page = await api.projects(filters, signal);
  const normalized = normalizePageRoute(route, page.total);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.projects(filters, signal);
  }

  const view = element("div");
  const heading = element("div", "projects-heading");
  heading.append(element("h1", "", "Workspaces"));
  const create = button("New workspace", "primary-button") as HTMLButtonElement;
  const createForm = projectCreateForm();
  createForm.id = "project-creator";
  createForm.hidden = true;
  create.setAttribute("aria-controls", createForm.id);
  create.setAttribute("aria-expanded", "false");
  if (await mayManageProjects()) {
    heading.append(create);
    create.addEventListener("click", () => {
      createForm.hidden = !createForm.hidden;
      create.setAttribute("aria-expanded", String(!createForm.hidden));
      create.textContent = createForm.hidden ? "New workspace" : "Hide creator";
      if (!createForm.hidden) createForm.querySelector<HTMLInputElement>("input")?.focus();
    });
  }
  view.append(heading, createForm);

  const tabs = element("div", "project-state-tabs");
  const tabHref = (state: "active" | "archived"): string => {
    const query = new URLSearchParams(route.query);
    query.delete("page");
    if (state === "active") query.delete("state"); else query.set("state", state);
    return `#/workspaces${query.toString() === "" ? "" : `?${query.toString()}`}`;
  };
  const activeTab = link(tabHref("active"), "Active", lifecycle === "active" ? "active" : "");
  const archivedTab = link(tabHref("archived"), "Archived", lifecycle === "archived" ? "active" : "");
  if (lifecycle === "active") activeTab.setAttribute("aria-current", "page");
  else archivedTab.setAttribute("aria-current", "page");
  tabs.append(activeTab, archivedTab);
  view.append(tabs);

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  const state = sortState(route.query.get("sort"), projectSortKeys, "key");
  const listingActions = element("div", "listing-actions");
  listingActions.append(
    mobileSortControl(route, projectColumns, "Natural order"),
    mobileUpdatedDisplayControl(),
    pagination(route, page.total),
  );
  if (page.rows.length === 0) {
    host.append(element("p", "empty", filterText === null
      ? lifecycle === "archived" ? "No archived workspaces." : "No workspaces yet. Create one above or with: awb workspace create <key>"
      : "No workspaces match this filter."));
  } else host.append(projectTable(route, page.rows, state));
  listing.append(listingFilter(
    route,
    "Filter all workspaces…",
    "workspace",
    page.total,
    listingActions,
  ));
  listing.append(host);
  view.append(listing);
  return view;
}

function projectCreateForm(): HTMLFormElement {
  const form = element("form", "edit-panel project-create-panel") as HTMLFormElement;
  form.append(element("h2", "", "Create workspace"), element("p", "muted", "The key becomes every issue ID prefix in this workspace and cannot be changed. Issues cannot move between workspaces."));
  const key = document.createElement("input");
  key.name = "key";
  key.required = true;
  key.maxLength = 16;
  key.pattern = "[a-z][a-z0-9-]*";
  const name = document.createElement("input");
  name.name = "name";
  name.maxLength = 500;
  const description = document.createElement("textarea");
  description.name = "description";
  description.rows = 5;
  const preview = element("p", "project-key-preview muted", "Issue IDs will use this key as their prefix.");
  key.addEventListener("input", () => {
    preview.textContent = key.value === "" ? "Issue IDs will use this key as their prefix." : `Issue IDs will start with ${key.value}-.`;
  });
  const submit = element("button", "primary-button", "Create workspace") as HTMLButtonElement;
  submit.type = "submit";
  form.append(field("Key", key), preview, field("Name (optional)", name), field("Description (Markdown)", description), submit);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    submit.disabled = true;
    try {
      const project = await api.createProject({ key: key.value, name: name.value, description: description.value });
      location.hash = `#/workspaces/${encodeURIComponent(project.key)}`;
    } catch (error) {
      submit.disabled = false;
      mutationError(form, error);
    }
  });
  return form;
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
    const identityText = element("span", "user-identity");
    identityText.append(element("span", "user-full-name", user.full_name || user.name));
    if (user.full_name !== "") identityText.append(element("span", "muted", `@${user.name}`));
    name.append(avatar(user.name), identityText);
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
    if (user.project_admin) roleList.append(element("span", "listing-badge", "workspace admin"));
    if (user.user_admin) roleList.append(element("span", "listing-badge", "user admin"));
    if (!user.project_admin && !user.user_admin) roleList.append(element("span", "muted", "member"));
    roles.append(roleList);
    row.append(roles);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewUsers(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  const requested = pageNumber(route.query);
  const size = listingPageSize(route.query);
  const filters: UserFilters = {
    limit: size,
    offset: (requested - 1) * size,
  };
  const filterText = route.query.get("filter");
  if (filterText !== null && filterText !== "") filters.filter = filterText;
  const focusedUser = route.query.get("user");
  let page = focusedUser === null
    ? await api.users(filters, signal)
    : await api.navigation(focusedUser, signal).then((results) => {
      const exact = results.users.filter((user) => user.name === focusedUser);
      return { rows: exact, total: exact.length };
    });
  const normalized = normalizePageRoute(route, page.total);
  if (focusedUser === null && (filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.users(filters, signal);
  }
  const view = element("div");
  view.append(element("h1", "", "Users"));

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  if (page.rows.length === 0) {
    host.append(element("p", "empty", filterText === null ? "No users yet." : "No users match this filter."));
  } else {
    host.append(userTable(page.rows));
  }
  listing.append(
    listingFilter(route, "Filter all users…", "user", page.total, pagination(route, page.total)),
    host,
  );
  view.append(listing);
  return view;
}

async function viewProject(key: string, signal?: AbortSignal): Promise<HTMLElement> {
  const [project, activity, canManage, memberPage, currentUser] = await Promise.all([
    api.project(key),
    api.projectActivity(key),
    mayManageProjects(),
    api.projectMembers(key, signal),
    identity === "" ? Promise.resolve(null) : api.user(identity),
  ]);
  const view = element("div", "project-view");
  if (project.state === "archived") {
    const banner = element("div", "project-archive-banner");
    banner.setAttribute("role", "status");
    banner.append(
      element("strong", "", "Archived"),
      document.createTextNode("This workspace is retained as read-only history. Restore it to resume work inside the same workspace boundary."),
    );
    view.append(banner);
  }
  const heading = element("div", "detail-heading");
  const title = element("div");
  title.append(element("div", "issue-key", project.key), element("h1", "", project.name));
  const edit = button("Edit workspace");
  heading.append(title);
  if (canManage && project.state === "active") heading.append(edit);
  view.append(heading);

  const form = projectEditForm(project);
  form.hidden = true;
  edit.addEventListener("click", () => {
    form.hidden = !form.hidden;
    edit.textContent = form.hidden ? "Edit workspace" : "Hide editor";
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
    `#/issues?project=${encodeURIComponent(project.key)}${project.state === "archived" ? "&include-archived=true&include-closed=true" : ""}`,
    project.state === "archived" ? "View this workspace's historical issues" : "View this workspace's issues",
    "action",
  ));
  view.append(projectLifecycleCard(project, activity.rows, canManage));
  view.append(projectMembershipSection(project, memberPage.rows, currentUser));
  return view;
}

async function changeProjectMembership(
  host: HTMLElement,
  controls: Iterable<HTMLButtonElement | HTMLInputElement | HTMLSelectElement>,
  operation: () => Promise<unknown>,
  success: string,
  refreshNotFound = false,
  redirect = false,
  refreshConflict = false,
): Promise<void> {
  const section = host.closest<HTMLElement>(".membership-card") ?? host;
  section.setAttribute("aria-busy", "true");
  for (const control of controls) control.disabled = true;
  try {
    await operation();
    pendingNotice = { message: success, error: false };
    if (redirect) {
      location.hash = "#/projects";
      return;
    }
    await render();
  } catch (error) {
    if (refreshNotFound && error instanceof ApiError && error.status === 404) {
      pendingNotice = {
        message: "Membership changed elsewhere. The current member list has been reloaded.",
        error: true,
      };
      await render();
      return;
    }
    if (refreshConflict && error instanceof ApiError && error.status === 409) {
      pendingNotice = {
        message: "That user was added elsewhere. The current member list has been reloaded.",
        error: true,
      };
      await render();
      return;
    }
    for (const control of controls) control.disabled = false;
    mutationError(host, error);
  } finally {
    section.removeAttribute("aria-busy");
  }
}

function projectMembershipSection(project: Project, members: Membership[], currentUser: User | null): HTMLElement {
  const section = element("section", "profile-card membership-card");
  const heading = element("div", "membership-heading");
  const title = element("div");
  title.append(
    element("h2", "", "Project members"),
    element(
      "p",
      "membership-help",
      "Membership grants access to this project. It is separate from each user's ignored-project preference.",
    ),
  );
  heading.append(title, element("span", "membership-count", String(members.length)));
  section.append(heading);

  const manageable = mayManageProjectMembership(identity, currentUser, project.key, members);
  if (manageable) section.append(projectMembershipEditor(project, members));
  else section.append(element("p", "membership-help", "Project administrators can change membership and access."));

  if (members.length === 0) {
    section.append(element("p", "empty", "No stored members. Global project administrators still have access."));
    return section;
  }

  const table = element("table", "listing-table membership-table") as HTMLTableElement;
  const head = document.createElement("thead");
  const headingRow = document.createElement("tr");
  for (const label of ["User", "Access", "Actions"]) {
    const cell = document.createElement("th");
    cell.scope = "col";
    cell.textContent = label;
    headingRow.append(cell);
  }
  head.append(headingRow);
  const body = document.createElement("tbody");
  for (const member of members) {
    const row = document.createElement("tr");
    const userCell = document.createElement("td");
    userCell.dataset.label = "User";
    const user = element("span", "user-name");
    user.append(avatar(member.user), element("span", "user-identity", `@${member.user}`));
    userCell.append(user);
    const rowControls: Array<HTMLButtonElement | HTMLSelectElement> = [];

    const accessCell = document.createElement("td");
    accessCell.dataset.label = "Access";
    if (manageable) {
      const access = select(["regular", "admin"], member.access);
      access.setAttribute("aria-label", `Access for @${member.user}`);
      for (const option of access.options) {
        option.textContent = option.value === "admin" ? "Administrator" : "Regular access";
      }
      rowControls.push(access);
      access.addEventListener("change", () => {
        const next = access.value as Membership["access"];
        if (next === member.access) return;
        if (!window.confirm(membershipChangeConfirmation(member, members, identity, next))) {
          access.value = member.access;
          return;
        }
        void changeProjectMembership(
          accessCell,
          rowControls,
          () => api.setProjectMember(project.key, member.user, next),
          `@${member.user} now has ${next} access to ${project.key}.`,
        ).then(() => {
          // On failure the row remains mounted, so restore what the server
          // still holds. A successful render has already detached this row.
          if (access.isConnected) access.value = member.access;
        });
      });
      accessCell.append(access);
    } else accessCell.append(element("span", "listing-badge", member.access));

    const actions = document.createElement("td");
    actions.dataset.label = "Actions";
    if (manageable) {
      const remove = button("Remove", "danger-button membership-remove");
      remove.setAttribute("aria-label", `Remove @${member.user} from ${project.key}`);
      rowControls.push(remove);
      remove.addEventListener("click", () => {
        if (!window.confirm(membershipChangeConfirmation(member, members, identity, null))) return;
        const losesAccess = member.user === identity && currentUser?.project_admin !== true;
        void changeProjectMembership(
          actions,
          rowControls,
          () => api.removeProjectMember(project.key, member.user),
          `@${member.user} was removed from ${project.key}.`,
          true,
          losesAccess,
        );
      });
      actions.append(remove);
    } else actions.append(element("span", "muted", "—"));
    row.append(userCell, accessCell, actions);
    body.append(row);
  }
  table.append(head, body);
  section.append(table);
  return section;
}

function projectMembershipEditor(project: Project, members: Membership[]): HTMLFormElement {
  const form = element("form", "compact-editor membership-editor") as HTMLFormElement;
  const input = document.createElement("input");
  input.required = true;
  input.maxLength = 64;
  input.placeholder = "Search users…";
  input.setAttribute("aria-label", "User to add");
  const autocomplete = attachAutocomplete(input, async (query, signal) => {
    const page = await api.users({ filter: query, limit: 8 }, signal);
    return membershipSuggestions(page.rows, members);
  });
  const access = select(["regular", "admin"], "regular");
  access.setAttribute("aria-label", "Access to grant");
  for (const option of access.options) {
    option.textContent = option.value === "admin" ? "Administrator" : "Regular access";
  }
  const add = element("button", "primary-button", "Add member") as HTMLButtonElement;
  add.type = "submit";
  form.append(autocomplete, access, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const user = input.value.trim();
    const next = access.value as Membership["access"];
    const duplicate = membershipAdditionError(user, members);
    if (duplicate !== null) {
      mutationError(form, new Error(duplicate));
      return;
    }
    void changeProjectMembership(
      form,
      [input, access, add],
      () => api.addProjectMember(project.key, user, next),
      `@${user} was added with ${next} access to ${project.key}.`,
      false,
      false,
      true,
    );
  });
  return form;
}

async function viewProjectMembership(key: string, signal?: AbortSignal): Promise<HTMLElement> {
  if (identity === "") throw new Error("No authenticated user is available.");
  const [preferences, memberPage, currentUser] = await Promise.all([
    api.projectPreferences(),
    api.projectMembers(key, signal),
    api.user(identity),
  ]);
  const preference = preferences.find((candidate) => candidate.project.key === key);
  if (preference === undefined) throw new ApiError(404, `no such project: ${key}`);

  const view = element("div", "project-view membership-admin-view");
  const heading = element("div", "detail-heading");
  const title = element("div");
  title.append(
    element("div", "issue-key", preference.project.key),
    element("h1", "", preference.project.name),
    element("p", "lede", preference.ignored ? "Ignored project administration" : "Project administration"),
  );
  heading.append(title, link("#/settings", "Back to settings", "secondary-button"));
  view.append(heading, projectMembershipSection(preference.project, memberPage.rows, currentUser));
  return view;
}

function projectLifecycleCard(project: Project, activity: ProjectActivity[], canManage: boolean): HTMLElement {
  const card = element("section", "project-lifecycle-card");
  card.append(element("h2", "", "Lifecycle"));
  if (project.state === "archived") {
    card.append(element("p", "", "Issues, comments, attachments, transitions and relations are read-only while this workspace is archived. Issues remain in this workspace and cannot be transferred elsewhere."));
    if (project.archived_at !== "") {
      const meta = element("p", "muted", `Archived${project.archived_by === "" ? "" : ` by @${project.archived_by}`} · `);
      meta.append(updatedTimeElement(project.archived_at));
      card.append(meta);
    }
    if (canManage) {
      const restore = element("button", "primary-button", "Restore workspace") as HTMLButtonElement;
      restore.type = "button";
      restore.addEventListener("click", () => void mutate(card, [restore], () => api.restoreProject(project.key)));
      card.append(restore);
    }
  } else {
    card.append(element("p", "", "Archive this workspace to remove it from everyday discovery and make its retained work read-only. Its issues keep their stable workspace-prefixed IDs."));
    if (canManage) card.append(projectArchiveConfirmation(project, card));
  }
  if (activity.length > 0) {
    card.append(element("h3", "", "Lifecycle history"));
    const list = element("ol", "project-lifecycle-history");
    for (const entry of activity) {
      const item = document.createElement("li");
      item.append(document.createTextNode(`${entry.action === "archived" ? "Archived" : "Restored"}${entry.actor === "" ? "" : ` by @${entry.actor}`} · `), updatedTimeElement(entry.created_at));
      list.append(item);
    }
    card.append(list);
  }
  return card;
}

function projectArchiveConfirmation(project: Project, host: HTMLElement): HTMLElement {
  const form = element("form", "project-archive-form") as HTMLFormElement;
  const input = document.createElement("input");
  input.placeholder = project.key;
  input.setAttribute("aria-label", `Type ${project.key} to confirm`);
  const archive = element("button", "archive-button", "Archive workspace") as HTMLButtonElement;
  archive.type = "submit";
  archive.disabled = true;
  input.addEventListener("input", () => { archive.disabled = input.value !== project.key; });
  form.append(element("label", "", `Type ${project.key} to confirm`), input, archive);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (input.value !== project.key) return;
    void mutate(host, [input, archive], () => api.archiveProject(project.key));
  });
  return form;
}

function projectEditForm(project: Project): HTMLFormElement {
  const form = element("form", "edit-panel") as HTMLFormElement;
  form.append(element("h2", "", "Edit workspace"));
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
  const issue = await api.issue(id);
  const [activity, project] = await Promise.all([api.activity(id), api.project(issue.project)]);

  const view = element("div", "issue-view");
  view.classList.toggle("sidebar-collapsed", issueSidebarCollapsed(issueSidebarStorage(window)));
  const content = element("div", "issue-content");
  const heading = element("div", "issue-heading");
  const headingText = element("div", "issue-heading-text");
  headingText.append(element("div", "issue-key", issue.id), element("h1", "", issue.title));
  const editButton = button("Edit issue");
  heading.append(headingText, editButton);
  content.append(heading);

  const existingDraft = issueEditDrafts.get(issue.id);
  const editForm = issueEditForm(issue, existingDraft);
  const editTitle = editForm.elements.namedItem("title") as HTMLInputElement;
  const editDescription = editForm.elements.namedItem("description") as HTMLTextAreaElement;
  editForm.hidden = true;
  const attachmentSection = issueAttachmentSection(issue);
  const relationSection = issueRelationSection(issue);
  const showEditor = (show: boolean) => {
    if (show) {
      issueEditDrafts.set(issue.id, {
        title: editTitle.value,
        description: editDescription.value,
      });
    } else {
      issueEditDrafts.delete(issue.id);
      attachmentSection.editor.reset();
      relationSection.editor.reset();
    }
    view.classList.toggle("issue-editing", show);
    editForm.hidden = !show;
    attachmentSection.editor.hidden = !show;
    attachmentSection.section.hidden = !show && issue.attachments.length === 0;
    relationSection.editor.hidden = !show;
    relationSection.section.hidden = !show && issue.relations.length === 0;
    editButton.textContent = show ? "Hide editor" : "Edit issue";
  };
  editButton.addEventListener("click", () => {
    showEditor(editForm.hidden === true);
    if (!editForm.hidden) editForm.querySelector<HTMLInputElement>("input")?.focus();
  });
  editForm.addEventListener("keydown", (event) => {
    const shortcut = issueEditorShortcut(event);
    if (shortcut !== "save") return;
    event.preventDefault();
    editForm.requestSubmit();
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

  // These are deliberately compact, content-height lists directly below the
  // description. Mutation controls only appear while the issue editor is
  // open, so an issue with one resource does not reserve room for a form.
  content.append(attachmentSection.section, relationSection.section);

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

  content.append(link(`#/tree/${issue.id}`, "Show the decomposition below this issue", "action"));
  content.append(activitySection(issue.id, activity.rows));
  const [sidebar, sidebarToggle] = issueSidebar(issue, view);
  view.append(content, sidebar, sidebarToggle);
  if (project.state === "archived") {
    view.classList.add("archived-project-issue");
    const banner = element("div", "project-archive-banner issue-archive-banner");
    banner.setAttribute("role", "status");
    banner.append(
      element("strong", "", "Read-only"),
      document.createTextNode(`Workspace ${project.key} is archived.`),
    );
    content.prepend(banner);
    editButton.remove();
    for (const control of view.querySelectorAll<HTMLButtonElement | HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(
      "form button, form input, form select, form textarea, .issue-sidebar button, .issue-sidebar input, .issue-sidebar select",
    )) control.disabled = true;
  }
  view.addEventListener("keydown", (event) => {
    if (event.defaultPrevented ||
        !view.classList.contains("issue-editing") || issueEditorShortcut(event) !== "hide") return;
    event.preventDefault();
    showEditor(false);
    editButton.focus();
  });
  if (existingDraft !== undefined && project.state === "active") showEditor(true);
  return view;
}

interface IssueResourceSection {
  section: HTMLElement;
  editor: HTMLFormElement;
}

function issueAttachmentSection(issue: Issue): IssueResourceSection {
  const section = element("section", "issue-resource-section attachment-section");
  section.append(element("h2", "", "Attachments"));
  if (issue.attachments.length > 0) {
    const list = element("ul", "attachments resource-list");
    for (const attachment of issue.attachments) list.append(attachmentRow(attachment, true));
    section.append(list);
  }
  const editor = attachmentEditor(issue.id);
  editor.hidden = true;
  section.append(editor);
  section.hidden = issue.attachments.length === 0;
  return { section, editor };
}

function issueRelationSection(issue: Issue): IssueResourceSection {
  const section = element("section", "issue-resource-section relation-section");
  section.append(element("h2", "", "Relations"));
  if (issue.relations.length > 0) {
    const list = element("ul", "relations resource-list");
    for (const relation of issue.relations) {
      // Every relation reads "subject — type — other", whichever end is
      // viewed.
      const [subject, other] =
        relation.direction === "in" ? [relation.other, issue.id] : [issue.id, relation.other];
      const row = element("li");
      row.append(link(`#/issues/${subject}`, subject, "id"));
      row.append(element("span", "relation-type", relation.type));
      row.append(link(`#/issues/${other}`, other, "id"));
      const remove = button("Remove", "inline-button danger-button resource-remove");
      remove.addEventListener("click", () => {
        const addressed = relation.direction === "in" ? relation.other : issue.id;
        const addressedOther = relation.direction === "in" ? issue.id : relation.other;
        void mutate(row, [remove], () => api.removeRelation(addressed, relation.type, addressedOther));
      });
      row.append(remove);
      list.append(row);
    }
    section.append(list);
  }
  const editor = relationEditor(issue.id);
  editor.hidden = true;
  section.append(editor);
  section.hidden = issue.relations.length === 0;
  return { section, editor };
}

function issueEditForm(issue: Issue, draft?: IssueEditDraft): HTMLFormElement {
  const form = element("form", "edit-panel issue-edit-form") as HTMLFormElement;
  form.append(element("h2", "", "Edit issue"));

  const title = document.createElement("input");
  title.name = "title";
  title.value = draft?.title ?? issue.title;
  title.required = true;
  title.maxLength = 500;

  const description = document.createElement("textarea");
  description.name = "description";
  description.value = draft?.description ?? issue.description;
  description.rows = 10;

  const save = element("button", "primary-button", "Save changes") as HTMLButtonElement;
  save.type = "submit";
  const actions = element("div", "edit-actions");
  actions.append(
    element("span", "edit-shortcut-hint", "Esc to hide · Ctrl/⌘+Enter to save"),
    save,
  );
  form.append(field("Title", title), field("Description (Markdown)", description), actions);
  const rememberDraft = (): void => {
    issueEditDrafts.set(issue.id, { title: title.value, description: description.value });
  };
  title.addEventListener("input", rememberDraft);
  description.addEventListener("input", rememberDraft);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [save], async () => {
      const updated = await api.updateIssue(issue.id, {
        title: title.value,
        description: description.value,
      });
      issueEditDrafts.delete(issue.id);
      return updated;
    });
  });
  return form;
}

function relationEditor(issueID: string): HTMLFormElement {
  const form = element("form", "compact-editor relation-editor") as HTMLFormElement;
  const disclosureRow = element("div", "relation-disclosure-row");
  const disclose = button("", "relation-disclosure");
  disclose.append(svgIcon("relation"), document.createTextNode("+ Add relation"));
  disclosureRow.append(
    disclose,
    element("span", "relation-hint", "Create a dependency or association"),
  );
  const fields = element("div", "relation-fields");
  fields.hidden = true;
  const type = select(["blocked-by", "has-parent", "discovered-from", "related"]);
  type.setAttribute("aria-label", "Relation type");
  const other = document.createElement("input");
  other.placeholder = "Other issue ID";
  other.setAttribute("aria-label", "Other issue ID");
  other.required = true;
  const otherAutocomplete = attachAutocomplete(other, async (query, signal) => {
    const page = await api.issueSuggestions(query, signal);
    return page.rows.filter((candidate) => candidate.id !== issueID).map((candidate) => ({
      value: candidate.id,
      label: candidate.id,
      detail: candidate.title,
    }));
  });
  const forceLabel = element("label", "check-field");
  const force = document.createElement("input");
  force.type = "checkbox";
  forceLabel.append(force, document.createTextNode("Replace parent"));
  const add = element("button", "quiet-action", "Add relation") as HTMLButtonElement;
  add.type = "submit";
  fields.append(type, otherAutocomplete, forceLabel, add);
  form.append(disclosureRow, fields);
  const expand = (): void => {
    disclosureRow.hidden = true;
    fields.hidden = false;
    other.focus();
  };
  const collapse = (): void => {
    disclosureRow.hidden = false;
    fields.hidden = true;
  };
  disclose.addEventListener("click", expand);
  form.addEventListener("reset", collapse);
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
  file.className = "attachment-file-input";
  file.setAttribute("aria-label", "Attachment file");
  const picker = element("label", "attachment-picker") as HTMLLabelElement;
  picker.append(
    svgIcon("attachment"),
    document.createTextNode("Drop a file here or "),
    element("span", "attachment-browse", "browse"),
    file,
  );
  form.append(picker);
  const upload = (selected: File): void => {
    void mutate(form, [file], () => api.addAttachment(issueID, selected));
  };
  file.addEventListener("change", () => {
    const selected = file.files?.[0];
    if (selected !== undefined) upload(selected);
  });
  form.addEventListener("submit", (event) => {
    event.preventDefault();
  });
  form.addEventListener("dragover", (event) => {
    event.preventDefault();
    form.classList.add("drag-active");
  });
  form.addEventListener("dragleave", () => form.classList.remove("drag-active"));
  form.addEventListener("drop", (event) => {
    event.preventDefault();
    form.classList.remove("drag-active");
    const selected = event.dataTransfer?.files[0];
    if (selected !== undefined) upload(selected);
  });
  form.addEventListener("reset", () => form.classList.remove("drag-active"));
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
  aside.append(element("h2", "issue-sidebar-title", "Details"));

  const add = (label: string, value: Node, popover?: HTMLElement): void => {
    const row = element("div", "issue-fact");
    row.append(element("dt", "", label));
    const detail = element("dd");
    detail.append(value);
    row.append(detail);
    if (popover !== undefined) row.append(popover);
    facts.append(row);
  };
  add("ID", element("span", "id", issue.id));
  add("Workspace", link(`#/workspaces/${encodeURIComponent(issue.project)}`, issue.project));
  const type = select(["epic", "feature", "bug", "task", "chore"], issue.type);
  type.className = "sidebar-select";
  type.setAttribute("aria-label", "Type");
  type.addEventListener("change", () => {
    mutateInspectorSelect(aside, type, issue.type, () =>
      api.updateIssue(issue.id, { type: type.value as Issue["type"] }));
  });
  add("Type", type);

  const status = select(["open", "in_progress", "closed"], issue.status);
  status.className = "sidebar-select";
  status.setAttribute("aria-label", "Status");
  const closeEditor = statusEditor(issue);
  const openCloseEditor = configureInspectorPopover(status, closeEditor, "Close issue", false);
  status.addEventListener("change", () => {
    const target = status.value as Issue["status"];
    const action = inspectorStatusAction(issue.status, target);
    if (action === "none") return;
    if (action === "close") {
      status.value = issue.status;
      openCloseEditor();
      return;
    }
    const operation = action === "claim"
      ? () => api.claimIssue(issue.id, { force: issue.status === "closed" })
      : action === "release"
        ? () => api.releaseIssue(issue.id, { force: true })
        : () => api.reopenIssue(issue.id);
    mutateInspectorSelect(aside, status, issue.status, operation);
  });
  add("Status", status, closeEditor);

  const priority = select(["0", "1", "2", "3", "4"], String(issue.priority));
  priority.className = `sidebar-select priority p${issue.priority}`;
  priority.setAttribute("aria-label", "Priority");
  for (const option of priority.options) option.textContent = `P${option.value}`;
  priority.addEventListener("change", () => {
    mutateInspectorSelect(aside, priority, String(issue.priority), () =>
      api.updateIssue(issue.id, { priority: Number(priority.value) }));
  });
  add("Priority", priority);
  if (issue.blocked) add("Readiness", badge("blocked", "blocked"));

  const [labels, labelsPopover] = labelInspector(issue);
  add("Labels", labels, labelsPopover);
  const [assignees, assigneesPopover] = assigneeInspector(issue);
  add("Assignees", assignees, assigneesPopover);
  add("Created", timeElement(issue.created_at));
  add("Updated", timeElement(issue.updated_at));
  aside.append(facts);
  drawToggle();
  return [aside, toggle];
}

function mutateInspectorSelect(
  host: HTMLElement,
  control: HTMLSelectElement,
  storedValue: string,
  operation: () => Promise<unknown>,
): void {
  void mutateInspector(host, operation).then(() => {
    // A successful mutation rerenders and detaches this control. If it is still
    // present, the request failed and the visible value must remain truthful.
    if (control.isConnected) control.value = storedValue;
  });
}

function mutateInspector(host: HTMLElement, operation: () => Promise<unknown>): Promise<void> {
  const inspector = host.closest<HTMLElement>(".issue-sidebar") ?? host;
  inspector.setAttribute("aria-busy", "true");
  const controls = inspector.querySelectorAll<HTMLButtonElement | HTMLInputElement | HTMLSelectElement>(
    "button, input, select",
  );
  return mutate(host, controls, operation).finally(() => {
    inspector.removeAttribute("aria-busy");
  });
}

function configureInspectorPopover(
  trigger: HTMLElement,
  popover: HTMLElement,
  label: string,
  toggleOnClick = true,
): () => void {
  popover.id = `inspector-popover-${++inspectorPopoverID}`;
  popover.classList.add("inspector-popover");
  popover.setAttribute("popover", "auto");
  popover.setAttribute("role", "dialog");
  popover.setAttribute("aria-label", label);
  if (toggleOnClick) {
    trigger.setAttribute("aria-controls", popover.id);
    trigger.setAttribute("aria-expanded", "false");
    trigger.setAttribute("aria-haspopup", "dialog");
  }

  const reposition = (): void => {
    if (!popover.matches(":popover-open")) return;
    const anchor = trigger.getBoundingClientRect();
    const bounds = popover.getBoundingClientRect();
    const viewport = visualViewport;
    const viewportBounds = viewport === null
      ? { width: innerWidth, height: innerHeight }
      : { width: viewport.width, height: viewport.height, left: viewport.offsetLeft, top: viewport.offsetTop };
    popover.style.maxHeight = `${Math.max(0, viewportBounds.height - 16)}px`;
    const position = inspectorPopoverPosition(anchor, bounds, viewportBounds);
    popover.style.left = `${position.left}px`;
    popover.style.top = `${position.top}px`;
  };

  const open = (): void => {
    if (popover.matches(":popover-open")) {
      popover.hidePopover();
      return;
    }
    popover.showPopover();
    reposition();
    (popover.querySelector<HTMLElement>("input, select") ??
      popover.querySelector<HTMLElement>("button"))?.focus();
  };
  if (toggleOnClick) trigger.addEventListener("click", open);
  let listeners: AbortController | undefined;
  const resizeObserver = new ResizeObserver(reposition);
  popover.addEventListener("toggle", () => {
    const opened = popover.matches(":popover-open");
    if (toggleOnClick) trigger.setAttribute("aria-expanded", String(opened));
    listeners?.abort();
    resizeObserver.disconnect();
    if (!opened) return;
    listeners = new AbortController();
    const options = { capture: true, signal: listeners.signal };
    addEventListener("scroll", reposition, options);
    addEventListener("resize", reposition, { signal: listeners.signal });
    visualViewport?.addEventListener("scroll", reposition, { signal: listeners.signal });
    visualViewport?.addEventListener("resize", reposition, { signal: listeners.signal });
    resizeObserver.observe(popover);
  });
  return open;
}

function matchingValues(values: string[], query: string, excluded: string[] = []): Suggestion[] {
  const needle = query.trim().toLocaleLowerCase();
  const hidden = new Set(excluded);
  return values.filter((value) => !hidden.has(value) && value.toLocaleLowerCase().includes(needle))
    .slice(0, 8)
    .map((value) => ({ value, label: value }));
}

function labelEditor(issue: Issue): HTMLFormElement {
  const form = element("form", "sidebar-editor label-editor") as HTMLFormElement;
  const input = document.createElement("input");
  input.placeholder = "Add label";
  input.setAttribute("aria-label", "Label");
  input.required = true;
  const autocomplete = attachAutocomplete(input, async (query, signal) => {
    const page = await api.labels({}, signal);
    return matchingValues(page.rows.map((facet) => facet.value), query, issue.labels);
  });
  const add = element("button", "quiet-action", "Add") as HTMLButtonElement;
  add.type = "submit";
  form.append(autocomplete, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutateInspector(form, () => api.addLabel(issue.id, input.value));
  });
  return form;
}

function labelInspector(issue: Issue): [HTMLElement, HTMLElement] {
  const panel = element("div");
  const labels = element("span", "inspector-values");
  if (issue.labels.length === 0) labels.append(document.createTextNode("—"));
  for (const label of issue.labels) {
    const chip = element("span", "editable-chip");
    chip.append(badge("label", label));
    const remove = button("×", "chip-remove");
    remove.title = `Remove label ${label}`;
    remove.setAttribute("aria-label", remove.title);
    remove.addEventListener("click", () => {
      void mutateInspector(labels, () => api.removeLabel(issue.id, label));
    });
    chip.append(remove);
    labels.append(chip);
  }
  const add = button("+", "inspector-add");
  add.setAttribute("aria-label", "Add label");
  labels.append(add);
  panel.append(labelEditor(issue));
  configureInspectorPopover(add, panel, "Edit labels");
  return [labels, panel];
}

function statusEditor(issue: Issue): HTMLElement {
  const panel = element("div");
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
    void mutateInspector(panel, () => api.closeIssue(
      issue.id,
      reason.value === "" ? {} : { reason: reason.value },
    ));
  });
  panel.append(close);
  return panel;
}

function assigneeInspector(issue: Issue): [HTMLElement, HTMLElement] {
  const panel = element("div");
  const assignees = element("span", "inspector-values");
  if (issue.assignees.length === 0) assignees.append(document.createTextNode("—"));
  for (const name of issue.assignees) {
    const chip = element("span", "editable-chip");
    chip.append(badge(name === identity ? "assignee mine" : "assignee", `@${name}`));
    if (issue.status === "in_progress") {
      const remove = button("×", "chip-remove");
      remove.title = `Release ${name}`;
      remove.setAttribute("aria-label", remove.title);
      remove.addEventListener("click", () => {
        void mutateInspector(assignees, () => api.releaseIssue(issue.id, {
          assignee: name,
          force: false,
        }));
      });
      chip.append(remove);
    }
    assignees.append(chip);
  }
  const add = button("+", "inspector-add");
  add.setAttribute("aria-label", issue.assignees.length === 0 ? "Claim issue" : "Add assignee");
  assignees.append(add);

  const claim = element("form", "sidebar-editor") as HTMLFormElement;
  const assignee = document.createElement("input");
  assignee.placeholder = identity === "" ? "Assignee" : `Assignee (default: ${identity})`;
  assignee.setAttribute("aria-label", "Assignee");
  const assigneeAutocomplete = attachAutocomplete(assignee, async (query, signal) => {
    const [members, users] = await Promise.all([
      api.projectMembers(issue.project, signal),
      api.users({}, signal),
    ]);
    const memberNames = new Set(members.rows.map((member) => member.user));
    const assigned = new Set(issue.assignees);
    const needle = query.trim().toLocaleLowerCase();
    return users.rows.filter((user) => memberNames.has(user.name) && !assigned.has(user.name)
      && `${user.name} ${user.full_name}`.toLocaleLowerCase().includes(needle))
      .slice(0, 8)
      .map((user) => ({
        value: user.name,
        label: user.name,
        detail: user.full_name === "" ? undefined : user.full_name,
      }));
  });
  const forceLabel = element("label", "check-field");
  const force = document.createElement("input");
  force.type = "checkbox";
  forceLabel.append(force, document.createTextNode("Force"));
  const claimButton = element(
    "button",
    "quiet-action",
    issue.assignees.length === 0 ? "Claim" : "Add",
  ) as HTMLButtonElement;
  claimButton.type = "submit";
  claim.append(assigneeAutocomplete, forceLabel, claimButton);
  claim.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutateInspector(panel, () => api.claimIssue(issue.id, {
      assignee: assignee.value === "" ? undefined : assignee.value,
      force: force.checked,
    }));
  });
  panel.append(claim);

  const actions = element("div", "assignee-actions");
  if (issue.status === "in_progress") {
    const release = button("Release all", "quiet-action inspector-release-all");
    release.addEventListener("click", () => {
      void mutateInspector(panel, () => api.releaseIssue(issue.id, { force: true }));
    });
    actions.append(release);
  }
  panel.append(actions);
  configureInspectorPopover(add, panel, "Manage assignees");
  return [assignees, panel];
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
    const remove = button("Remove", "inline-button danger-button resource-remove");
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
  for (const item of accountMenuItems) {
    const anchor = link(item.href, item.label, "account-menu-item");
    anchor.setAttribute("role", "menuitem");
    menu.append(anchor);
  }

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
  nav.append(navLink(projectScopedHref("workspaces", route.query), "Workspaces", "projects"));
  nav.append(navLink("#/users", "Users", "users"));
  const brand = link("#/ready", "", "brand");
  const mark = document.createElement("img");
  mark.src = "awb-mark.png";
  mark.alt = "";
  mark.className = "brand-mark";
  brand.append(mark, document.createTextNode("Agent Work Board"));
  header.append(brand);
  header.append(nav);
  const commands = button(paletteTrigger.label, "command-palette-button");
  commands.setAttribute("aria-keyshortcuts", paletteTrigger.keyShortcuts);
  commands.title = paletteTrigger.title;
  const shortcut = element("kbd", "", paletteShortcutHint());
  commands.append(shortcut);
  commands.addEventListener("click", () => commandPalette?.open());
  header.append(commands);
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
  if (projects.length === 0) list.append(element("li", "empty", "No workspace access."));
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

function fullNameForm(user: User, onUpdated: (updated: User) => void): HTMLFormElement {
  const form = element("form", "profile-name-form") as HTMLFormElement;
  const inputID = "profile-full-name";
  const input = document.createElement("input");
  input.id = inputID;
  input.name = "full_name";
  input.value = user.full_name;
  input.maxLength = 500;
  input.autocomplete = "name";
  const submit = element("button", "profile-submit", "Save full name") as HTMLButtonElement;
  submit.type = "submit";
  const message = element("p", "profile-form-message");
  message.setAttribute("aria-live", "polite");
  const help = element("p", "profile-form-help", `Optional. Shown with @${user.name} in the user directory.`);
  const label = element("label", "", "Full name") as HTMLLabelElement;
  label.htmlFor = inputID;
  form.append(label, input, help, submit, message);

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    message.className = "profile-form-message";
    submit.disabled = true;
    message.textContent = "";
    const result = await saveProfileFullName(user, input.value, api.updateUser);
    if (result.ok) {
      Object.assign(user, result.user);
      input.value = result.user.full_name;
      onUpdated(result.user);
      message.textContent = result.message;
    } else {
      message.classList.add("form-error");
      message.textContent = result.message;
    }
    submit.disabled = false;
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
  const titleName = element("h1");
  const titleDetail = element("p", "lede");
  const showIdentity = (current: User): void => {
    const shown = profileIdentity(current);
    titleName.textContent = shown.heading;
    titleDetail.textContent = shown.detail;
  };
  showIdentity(user);
  title.append(titleName, titleDetail);
  heading.append(title);
  view.append(heading);

  const roles = element("div", "profile-roles");
  for (const role of accountRoles(user)) roles.append(element("span", "listing-badge", role));
  const details = element("section", "profile-card");
  details.append(element("h2", "", "Account status"), roles);
  const facts = element("dl", "profile-facts");
  let updatedValue: HTMLElement | undefined;
  const fact = (label: string, value: string): void => {
    const definition = element("dd", "", value);
    facts.append(element("dt", "", label), definition);
    if (label === "Updated") updatedValue = definition;
  };
  fact("Username", user.name);
  fact("Created", user.created_at);
  fact("Updated", user.updated_at);
  details.append(facts);

  const profile = element("section", "profile-card");
  profile.append(element("h2", "", "Profile"), fullNameForm(user, (updated) => {
    showIdentity(updated);
    if (updatedValue !== undefined) updatedValue.textContent = profileIdentity(updated).updated;
  }));
  const access = element("section", "profile-card");
  access.append(element("h2", "", "Workspace access"), profileProjectList(user, projects.rows));
  const security = element("section", "profile-card");
  security.append(element("h2", "", "Password"), passwordForm(user));
  view.append(details, profile, access, security);
  return view;
}

function ignoredProjectsSettingsCard(projects: ProjectPreference[]): HTMLElement {
  const card = element("section", "profile-card ignored-projects-card");
  const heading = element("div", "ignored-projects-heading");
  const copy = element("div");
  copy.append(
    element("h2", "", "Ignored workspaces"),
    element(
      "p",
      "",
      "Ignored workspaces are hidden from listings, search, counts, and navigation. " +
        "They always remain available here so you can re-enable them.",
    ),
  );
  const summary = element("span", "ignored-summary", projectPreferenceSummary(projects));
  heading.append(copy, summary);

  const filterLabel = element("label", "project-preference-filter");
  filterLabel.append(element("span", "visually-hidden", "Find a workspace"));
  const filter = document.createElement("input");
  filter.type = "search";
  filter.placeholder = "Find a workspace by name or key";
  const filterControl = element("span", "search-control project-preference-search");
  const filterClear = attachSearchClear(filter);
  filterControl.append(filter, filterClear.button);
  filterLabel.append(filterControl);

  const list = element("ul", "project-preference-list");
  const rows = new Map<ProjectPreference, HTMLElement>();
  for (const preference of projects) {
    const row = element("li", `project-preference-row${preference.ignored ? " ignored" : ""}`);
    const name = element("span", "project-preference-identity");
    name.append(
      element("code", "", preference.project.key),
      element("span", "", preference.project.name),
    );
    const state = element(
      "span",
      `project-preference-state ${preference.ignored ? "ignored-state" : "active-state"}`,
      preference.ignored ? "Ignored" : "Active",
    );
    const action = element(
      "button",
      "secondary-button project-preference-action",
      preference.ignored ? "Re-enable" : "Ignore",
    ) as HTMLButtonElement;
    action.type = "button";
    action.addEventListener("click", () => {
      void mutate(row, [action], () => api.setProjectIgnored(
        preference.project.key, !preference.ignored,
      ));
    });
    const actions = element("span", "project-preference-actions");
    actions.append(link(
      `#/projects/${encodeURIComponent(preference.project.key)}/members`,
      "Members",
      "secondary-button project-preference-members",
    ));
    actions.append(action);
    row.append(name, state, actions);
    rows.set(preference, row);
    list.append(row);
  }
  const empty = element("p", "project-preference-empty empty", "No authorized workspaces match your search.");
  empty.hidden = projects.length !== 0;
  const refresh = (): void => {
    const visible = new Set(filterProjectPreferences(projects, filter.value));
    for (const [preference, row] of rows) row.hidden = !visible.has(preference);
    empty.hidden = visible.size !== 0;
  };
  filter.addEventListener("input", refresh);
  card.append(heading, filterLabel, list, empty);
  return card;
}

async function viewSettings(): Promise<HTMLElement> {
  if (identity === "") throw new Error("No authenticated user is available.");
  let projectPreferences: ProjectPreference[] | null = null;
  try {
    projectPreferences = await api.projectPreferences();
  } catch (error) {
    // An open/no-auth server has an attribution identity but no account row,
    // and therefore no per-user preference owner. Its existing browser-local
    // settings remain available without pretending the identity is a user.
    if (!(error instanceof ApiError) || error.status !== 404) throw error;
  }
  const view = element("div", "profile-view settings-view");
  const heading = element("div", "settings-heading");
  heading.append(
    element("h1", "", "Settings"),
    element("p", "lede", "Your preferences across Agent Work Board"),
  );
  view.append(heading);

  const card = element("section", "profile-card");
  card.append(element("h2", "", "Listings"));
  const preference = element("label", "settings-preference");
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.checked = paginationAutoHide;
  const copy = element("span", "settings-preference-copy");
  copy.append(
    element("span", "settings-preference-title", "Automatically hide pagination"),
    element(
      "span",
      "settings-preference-description",
      "Hide pagination controls when a listing has fewer than 10 entries. Saved in this browser.",
    ),
  );
  preference.append(checkbox, copy);
  checkbox.addEventListener("change", () => {
    paginationAutoHide = checkbox.checked;
    rememberPaginationAutoHide(preferences, paginationAutoHide);
  });
  card.append(preference);
  view.append(card);
  if (projectPreferences !== null) view.append(ignoredProjectsSettingsCard(projectPreferences));
  return view;
}

async function render(): Promise<void> {
  const generation = ++renderGeneration;
  activeRenderRequest?.abort();
  const request = new AbortController();
  activeRenderRequest = request;
  activeListingFilter?.close();
  activeListingFilter = null;
  let route = parseRoute();
  if (route.path[0] === "search") {
    history.replaceState(null, "", legacyIssueSearchHref(route.query));
    route = parseRoute();
  }
  for (const popover of app.querySelectorAll<HTMLElement>(":popover-open")) popover.hidePopover();
  clear(app);
  app.append(chrome());

  const main = element("main");
  app.append(main);

  try {
    const view = await routeView(route, request.signal);
    if (generation !== renderGeneration || request.signal.aborted) return;
    const notice = pendingNotice;
    if (notice !== null) {
      const message = element("p", notice.error ? "app-notice app-notice-error" : "app-notice", notice.message);
      message.setAttribute("role", notice.error ? "alert" : "status");
      message.setAttribute("aria-live", notice.error ? "assertive" : "polite");
      main.append(message);
      pendingNotice = null;
    }
    main.append(view);
    activateListingFilter(view);
  } catch (error) {
    if (generation !== renderGeneration || request.signal.aborted) return;
    const notice = pendingNotice;
    if (notice !== null) {
      const message = element("p", notice.error ? "app-notice app-notice-error" : "app-notice", notice.message);
      message.setAttribute("role", notice.error ? "alert" : "status");
      main.append(message);
      pendingNotice = null;
    }
    showRouteError(error, main, false);
  }

  markActiveNav(route);
  if (activeRenderRequest === request) activeRenderRequest = null;
}

// View construction can finish after its request was aborted. Controller
// ownership changes only when a generation-guarded view is actually mounted,
// so a detached stale view cannot steal cancellation from the live route.
function activateListingFilter(view: HTMLElement): void {
  const tools = view.querySelector<HTMLElement>(".listing-tools");
  const next = tools === null ? undefined : listingFilterOwners.get(tools);
  activeListingFilter?.close();
  activeListingFilter = next ?? null;
}

function showRouteError(error: unknown, host = app.querySelector("main"), replace = true): void {
  if (host === null) return;
  if (replace) clear(host);
  const message = error instanceof ApiError ? error.message : String(error);
  const box = element("div", "error");
  box.append(element("h1", "", "Something went wrong"));
  box.append(element("p", "", message));
  host.append(box);
}

async function routeView(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  switch (route.path[0]) {
    case undefined:
    case "ready":
      return viewListing(route, "ready", signal);
    case "issues":
      return route.path.length > 1 ? viewIssue(route.path[1]) : viewListing(route, "issues", signal);
    case "blocked":
      return viewListing(route, "blocked", signal);
    case "workspaces":
    case "projects": // Legacy browser route; links emitted by this UI use /workspaces.
      if (route.path.length > 2 && route.path[2] === "members") {
        return viewProjectMembership(route.path[1], signal);
      }
      return route.path.length > 1 ? viewProject(route.path[1], signal) : viewProjects(route, signal);
    case "profile":
      return viewProfile();
    case "settings":
      return viewSettings();
    case "users":
      return viewUsers(route, signal);
    case "tree":
      return viewTree(route.path[1] ?? "");
    default: {
      const view = element("div", "error");
      view.append(element("h1", "", "No such page"));
      view.append(link("#/ready", "Go to Ready"));
      return view;
    }
  }
}

function markActiveNav(route: Route): void {
  const rawCurrent = route.path[0] ?? "ready";
  const current = rawCurrent === "projects" ? "workspaces" : rawCurrent;
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
  const registry = new CommandRegistry();
  registry.register("navigation", () => {
    const route = parseRoute();
    const commands: PaletteCommand[] = namedDestinations.map((destination) => ({
      id: `view:${destination.id}`,
      label: `Go to ${destination.label}`,
      hint: "View",
      keywords: destination.keywords,
      group: "Navigation",
      run: () => {
        location.hash = destination.projectScoped === undefined
          ? destination.path
          : projectScopedHref(destination.projectScoped, route.query);
      },
    }));
    if (identity !== "") commands.push({
      id: "view:profile",
      label: "Go to profile",
      hint: `@${identity}`,
      keywords: "account settings password",
      group: "Navigation",
      run: () => { location.hash = "#/profile"; },
    });
    return commands;
  });
  commandPalette = new CommandPalette(
    registry,
    (query, signal) => api.navigation(query, signal),
    (href) => { location.hash = href; },
  );
  window.addEventListener("hashchange", () => void render());
  await render();
}

void start();
