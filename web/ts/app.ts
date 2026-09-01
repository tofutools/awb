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
  type Board,
  type BoardView,
  type BoardViewCreate,
  type BoardViewPatch,
  type Facet,
  type Filters,
  type DirectoryUser,
  type Issue,
  type IssueCreate,
  type IssueTree,
  type Membership,
  type Workspace,
  type WorkspaceActivity,
  type WorkspacePreference,
  type WorkspaceFilters,
  type User,
  type UserCreate,
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
import { commentSubmitShortcut, confirmationDecision, issueEditorShortcut } from "./keyboard.js";
import {
  CommandPalette,
  CommandRegistry,
  paletteShortcutHint,
  paletteTrigger,
  type PaletteCommand,
} from "./command-palette.js";
import { renderMarkdown } from "./markdown.js";
import {
  activateMarkdownEditors,
  createMarkdownEditor,
  destroyMarkdownEditors,
  type MarkdownEditor,
} from "./markdown-editor.js";
import { activityValues, initialFor, relativeTime } from "./presentation.js";
import { issueSidebarCollapsed, issueSidebarStorage, rememberIssueSidebar } from "./sidebar.js";
import {
  legacyIssueSearchHref,
  namedDestinations,
  navigationPath,
  workspaceScopedHref,
} from "./navigation.js";
import { accountRoles, profileIdentity, saveProfileFullName } from "./profile.js";
import {
  userCreateHref,
  userDeletionImpact,
  userDeletionWarning,
  userEditorHref,
  userNameFromRouteSegment,
} from "./user-admin.js";
import { attachAutocomplete, type Suggestion } from "./autocomplete.js";
import {
  mayManageWorkspaceMembership,
  membershipAdditionError,
  membershipChangeConfirmation,
  membershipSuggestions,
} from "./membership.js";
import { inspectorPopoverPosition, inspectorStatusAction } from "./inspector.js";
import { legalBoardTargets, splitBoardFilter, type BoardStatus } from "./boards.js";
import { attachSearchClear } from "./search-control.js";
import {
  accountMenuItems,
  preferenceStorage,
  readPaginationAutoHide,
  rememberPaginationAutoHide,
  filterWorkspacePreferences,
  showPagination,
  workspacePreferenceSummary,
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
let mayManageUsers = false;
let updatedDisplay: UpdatedDisplay | null = null;
let updatedControlID = 0;
let inspectorPopoverID = 0;
let confirmationDialogID = 0;
const preferences = preferenceStorage(window);
let paginationAutoHide = readPaginationAutoHide(preferences);
const paginationStorage = pageSizeStorage(window);
let commandPalette: CommandPalette | null = null;
let activeListingFilter: BackendListingFilter<HTMLElement> | null = null;
const listingFilterOwners = new WeakMap<HTMLElement, BackendListingFilter<HTMLElement>>();
let activeRenderRequest: AbortController | null = null;
let renderGeneration = 0;
let workspaceManager: boolean | null = null;

async function mayManageWorkspaces(): Promise<boolean> {
  if (workspaceManager !== null) return workspaceManager;
  if (identity === "") return true;
  try {
    workspaceManager = (await api.user(identity)).workspace_admin;
  } catch (error) {
    // A server with no account rows is unrestricted even though it still has
    // an attribution identity for audit entries.
    if (error instanceof ApiError && error.status === 404) workspaceManager = true;
    else throw error;
  }
  return workspaceManager;
}
let pendingNotice: { message: string; error: boolean } | null = null;

function listingPageSize(query: URLSearchParams): number {
  return pageSizeFrom(query, rememberedPageSize(paginationStorage));
}

interface IssueEditDraft {
  title: string;
  description: string;
}

interface IssueForm {
  form: HTMLFormElement;
  title: HTMLInputElement;
  description: MarkdownEditor;
  submit: HTMLButtonElement;
  actions: HTMLElement;
}

interface IssueCreateDefaults {
  workspace?: string;
  epic?: Issue;
  assignToMe?: boolean;
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
 * announcements per row, which a long listing multiplies. A workspace need not
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

type IconName = "attachment" | "blocked" | "boards" | "change" | "clock" | "info" | "issues" | "workspaces" | "ready" | "relation" | "search" | "tag" | "users";

/** svgIcon keeps the small, decorative interface icons in the document rather
 * than adding another asset pipeline or network request. */
function svgIcon(name: IconName): SVGSVGElement {
  const paths: Record<IconName, string> = {
    attachment: '<path d="m21.4 11.6-8.5 8.5a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5"></path>',
    blocked: '<circle cx="12" cy="12" r="9"></circle><path d="m5.7 5.7 12.6 12.6"></path>',
    boards: '<rect x="3" y="4" width="5" height="16" rx="1"></rect><rect x="10" y="4" width="5" height="11" rx="1"></rect><rect x="17" y="4" width="4" height="14" rx="1"></rect>',
    change: '<path d="M7 7h11l-3-3m3 3-3 3"></path><path d="M17 17H6l3 3m-3-3 3-3"></path>',
    clock: '<circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path>',
    info: '<circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path>',
    issues: '<path d="M6 3h8l4 4v14H6z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>',
    workspaces: '<path d="M3 6h7l2 2h9v11H3z"></path>',
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
  destroyMarkdownEditors(node);
  node.replaceChildren();
}

function button(text: string, className = "secondary-button"): HTMLButtonElement {
  const control = element("button", className, text) as HTMLButtonElement;
  control.type = "button";
  return control;
}

/** Ask before a relation or attachment mutation. A fresh dialog per decision
 * keeps its lifetime tied to the action and lets focus return to its trigger. */
function confirmMutation(
  title: string,
  description: string,
  trigger: HTMLElement,
  destructive = false,
): Promise<boolean> {
  const active = document.activeElement;
  const restoreFocus = active instanceof HTMLElement && active !== document.body ? active : trigger;
  const dialog = element("dialog", "confirmation-dialog") as HTMLDialogElement;
  const id = confirmationDialogID++;
  const titleID = `confirmation-title-${id}`;
  const descriptionID = `confirmation-description-${id}`;
  dialog.setAttribute("aria-labelledby", titleID);
  dialog.setAttribute("aria-describedby", descriptionID);

  const heading = element("h2", "", title);
  heading.id = titleID;
  const detail = element("p", "confirmation-description", description);
  detail.id = descriptionID;
  const hint = element("span", "confirmation-shortcut-hint", "Enter: Yes · Esc: No");
  const no = button("No", "secondary-button");
  const yes = button("Yes", destructive ? "danger-button" : "primary-button");
  const actions = element("div", "confirmation-actions");
  actions.append(hint, no, yes);
  dialog.append(heading, detail, actions);
  document.body.append(dialog);

  return new Promise((resolve) => {
    no.addEventListener("click", () => dialog.close("no"));
    yes.addEventListener("click", () => dialog.close("yes"));
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      dialog.close("no");
    });
    dialog.addEventListener("keydown", (event) => {
      const decision = confirmationDecision(event);
      if (decision === undefined) return;
      event.preventDefault();
      dialog.close(decision === "confirm" ? "yes" : "no");
    });
    dialog.addEventListener("close", () => {
      const confirmed = dialog.returnValue === "yes";
      dialog.remove();
      if (restoreFocus.isConnected) restoreFocus.focus();
      resolve(confirmed);
    }, { once: true });
    dialog.showModal();
    no.focus();
  });
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

function markdownField(labelText: string, editor: MarkdownEditor): HTMLElement {
  const wrapper = element("div", "edit-field");
  wrapper.append(element("span", "edit-field-label", labelText), editor.element);
  return wrapper;
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

/** Build the title and Markdown fields shared by issue creation and editing.
 * Each caller owns its mutation behavior and any creation-only metadata. */
function issueForm(
  heading: string,
  submitLabel: string,
  titleValue: string,
  descriptionValue: string,
  className = "edit-panel",
): IssueForm {
  const form = element("form", className) as HTMLFormElement;
  form.append(element("h2", "", heading));
  const title = document.createElement("input");
  title.name = "title";
  title.value = titleValue;
  title.required = true;
  title.maxLength = 500;
  const description = createMarkdownEditor(descriptionValue, "description", "Issue description (Markdown)");
  const submit = element("button", "primary-button", submitLabel) as HTMLButtonElement;
  submit.type = "submit";
  const actions = element("div", "edit-actions");
  form.append(field("Title", title), markdownField("Description (Markdown)", description), actions);
  return { form, title, description, submit, actions };
}

async function openIssueCreateDialog(defaults: IssueCreateDefaults = {}): Promise<void> {
  const page = await api.workspaces();
  const workspaces = page.rows.filter((workspace) => workspace.state !== "archived");
  if (workspaces.length === 0) throw new Error("Create a workspace before creating an issue.");

  const dialog = element("dialog", "issue-create-dialog") as HTMLDialogElement;
  dialog.setAttribute("aria-labelledby", "issue-create-heading");
  const editor = issueForm("New issue", "Create issue", "", "", "issue-create-form");
  editor.form.querySelector("h2")!.id = "issue-create-heading";

  const workspace = select(workspaces.map((item) => item.key), defaults.workspace ?? defaults.epic?.workspace ?? workspaces[0].key);
  workspace.name = "workspace";
  const type = select(["task", "feature", "bug", "epic", "chore"], "task");
  type.name = "type";
  const priority = select(["0", "1", "2", "3", "4"], "2");
  priority.name = "priority";
  for (const option of priority.options) option.textContent = `P${option.value}`;
  const metadata = element("div", "edit-field-row");
  metadata.append(field("Workspace", workspace), field("Type", type), field("Priority", priority));
  editor.form.insertBefore(metadata, editor.form.children[1]);

  if (defaults.epic !== undefined) {
    workspace.value = defaults.epic.workspace;
    workspace.disabled = true;
    editor.form.insertBefore(
      element("p", "issue-create-context", `Epic: ${defaults.epic.id} · ${defaults.epic.title}`),
      editor.form.children[1],
    );
  }

  const assignLabel = element("label", "issue-create-assign");
  const assign = document.createElement("input");
  assign.type = "checkbox";
  assign.checked = defaults.assignToMe === true;
  assignLabel.append(assign, document.createTextNode(`Assign to me${identity === "" ? "" : ` (@${identity})`}`));
  const cancel = button("Cancel");
  editor.actions.append(assignLabel, cancel, editor.submit);
  dialog.append(editor.form);
  document.body.append(dialog);

  cancel.addEventListener("click", () => dialog.close());
  dialog.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); });
  dialog.addEventListener("close", () => {
    destroyMarkdownEditors(dialog);
    dialog.remove();
  }, { once: true });
  editor.form.addEventListener("keydown", (event) => {
    if (issueEditorShortcut(event) !== "save") return;
    event.preventDefault();
    editor.form.requestSubmit();
  });
  editor.form.addEventListener("submit", (event) => {
    event.preventDefault();
    editor.submit.disabled = true;
    const body: IssueCreate = {
      workspace: workspace.value,
      title: editor.title.value,
      description: editor.description.textarea.value,
      type: type.value as IssueCreate["type"],
      priority: Number(priority.value) as IssueCreate["priority"],
      ...(assign.checked && identity !== "" ? { assignees: [identity] } : {}),
      ...(defaults.epic === undefined ? {} : { relations: [{ type: "has-parent", other: defaults.epic.id }] }),
    };
    void api.createIssue(body).then((created) => {
      dialog.close();
      location.hash = `#/issues/${created.id}`;
    }).catch((error) => {
      editor.submit.disabled = false;
      mutationError(editor.form, error);
    });
  });
  dialog.showModal();
  activateMarkdownEditors(dialog);
  editor.title.focus();
}

function issueCreateButton(label: string, defaults: IssueCreateDefaults = {}, className = "primary-button"): HTMLButtonElement {
  const control = button(label, className);
  control.addEventListener("click", () => {
    control.disabled = true;
    void openIssueCreateDialog(defaults).catch((error) => mutationError(app.querySelector("main") ?? app, error)).finally(() => {
      control.disabled = false;
    });
  });
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
  "order", "id", "workspace", "priority", "status", "assignee", "created", "updated", "type", "blockers",
] as const;

let draggedListIssue: Issue | null = null;

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
  const workspace: IssueColumn = {
    key: "workspace",
    label: "Workspace",
    render: (row) => textCell("id", row.workspace),
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
      workspace,
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
      workspace,
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
    workspace,
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
  const orderingEnabled = state.key === "order" && state.direction === "asc";
  for (const issue of issues) {
    const row = document.createElement("tr");
    row.dataset.issue = issue.id;
    for (const column of columns) {
      const td = document.createElement("td");
      td.dataset.label = column.label;
      td.append(column.render(issue));
      row.append(td);
    }
    if (orderingEnabled) {
      configureDragSurface(row, issue, () => { draggedListIssue = issue; }, () => { draggedListIssue = null; });
      let before = issue.id;
      let after = "";
      row.addEventListener("dragover", (event) => {
        if (draggedListIssue === null || draggedListIssue.id === issue.id || draggedListIssue.workspace !== issue.workspace) return;
        event.preventDefault();
        const below = event.clientY > row.getBoundingClientRect().top + row.getBoundingClientRect().height / 2;
        before = below ? "" : issue.id;
        after = below ? issue.id : "";
        row.classList.toggle("drop-after", below);
        row.classList.toggle("drop-before", !below);
      });
      row.addEventListener("dragleave", () => row.classList.remove("drop-before", "drop-after"));
      row.addEventListener("drop", (event) => {
        const moving = draggedListIssue;
        row.classList.remove("drop-before", "drop-after");
        if (moving === null || moving.id === issue.id) return;
        event.preventDefault();
        draggedListIssue = null;
        if (moving.workspace !== issue.workspace) {
          mutationError(row, new Error(`Issues cannot be reordered across workspaces (${moving.workspace} → ${issue.workspace}).`));
          return;
        }
        row.classList.add("moving");
        void api.moveIssue(moving.id, {
          status: moving.status,
          ...(before === "" ? {} : { before }),
          ...(after === "" ? {} : { after }),
        }).then(() => render()).catch((error) => {
          row.classList.remove("moving");
          mutationError(row, error);
        });
      });
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
  const defaultKey = "order";
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
  listingActions.append(issueCreateButton("New issue", {
    workspace: route.query.getAll("workspace").length === 1 ? route.query.get("workspace") ?? undefined : undefined,
  }));
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
  const workspace = query.getAll("workspace");
  if (workspace.length > 0) filters.workspace = workspace;
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
  workspaces: Workspace[],
  labels: Facet[] | null,
  assignees: Facet[] | null,
  paginationControl: HTMLElement,
): HTMLElement {
  const bar = element("div", "facets");
  const paginationGroup = lowestFacetGroup(labels, assignees);

  const workspaceGroup = element("div", "facet-group workspaces");
  const workspaceValues = element("span", "facet-values");
  workspaceGroup.append(element("span", "facet-title", "workspaces"), workspaceValues);
  const workspaceEmpty = emptyFacetLabel(workspaces);
  if (workspaceEmpty !== null) {
    workspaceValues.append(element("span", "facet-empty", workspaceEmpty));
  } else {
    for (const workspace of workspaces) {
      const active = route.query.getAll("workspace").includes(workspace.key);
      const anchor = link(
        facetHref(route, "workspace", workspace.key),
        workspace.key,
        active ? "facet active" : "facet",
      );
      anchor.dataset.facetName = "workspace";
      anchor.dataset.facetValue = workspace.key;
      workspaceValues.append(anchor);
    }
  }
  if (paginationGroup === "workspace") {
    workspaceGroup.classList.add("with-pagination");
    workspaceGroup.append(paginationControl);
  }
  bar.append(workspaceGroup);

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
  let [page, workspaces, labels, assignees] = await Promise.all([
    load(),
    api.workspaces(filters["include-archived"] ? { state: "all" } : {}, signal),
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
      workspaces.rows,
      labels.rows,
      kind === "ready" ? null : assignees.rows,
      pagination(route, page.total),
    ),
  ));
  return view;
}

const boardLanePageSize = 10;
const boardCardPageSize = 8;
let draggedBoardIssue: Issue | null = null;
const dragInteractionSelector = "a, button, input, select, textarea, label, [data-act], [contenteditable], [role='button']";
let suppressedDragSurface: HTMLElement | null = null;

function restoreDragSurface(): void {
  if (suppressedDragSurface === null) return;
  suppressedDragSurface.draggable = matchMedia("(min-width: 701px)").matches;
  suppressedDragSurface = null;
}

function clearDragFeedback(): void {
  for (const target of document.querySelectorAll(".drop-target, .drop-before, .drop-after")) {
    target.classList.remove("drop-target", "drop-before", "drop-after");
  }
}

document.addEventListener("pointerup", restoreDragSurface);
document.addEventListener("pointercancel", restoreDragSurface);

function configureDragSurface(
  surface: HTMLElement,
  issue: Issue,
  start: () => void,
  end: () => void,
): void {
  surface.draggable = matchMedia("(min-width: 701px)").matches;
  surface.addEventListener("pointerdown", (event) => {
    if (!(event.target instanceof Element)) return;
    const control = event.target.closest(dragInteractionSelector);
    if (control === null || !surface.contains(control)) return;
    restoreDragSurface();
    surface.draggable = false;
    suppressedDragSurface = surface;
  }, true);
  surface.addEventListener("dragstart", (event) => {
    start();
    event.dataTransfer?.setData("text/plain", issue.id);
    if (event.dataTransfer !== null) event.dataTransfer.effectAllowed = "move";
    requestAnimationFrame(() => surface.classList.add("dragging"));
  });
  surface.addEventListener("dragend", () => {
    surface.classList.remove("dragging");
    clearDragFeedback();
    end();
  });
}

function boardStatusLabel(status: BoardStatus): string {
  if (status === "in_progress") return "In progress";
  return status[0].toUpperCase() + status.slice(1);
}

function boardLaneCollapseKey(ref: string): string {
  return `awb.board.${ref}.collapsed-lanes`;
}

function collapsedBoardLanes(ref: string): Set<string> {
  try {
    const stored: unknown = JSON.parse(localStorage.getItem(boardLaneCollapseKey(ref)) ?? "[]");
    return new Set(Array.isArray(stored) ? stored.filter((value): value is string => typeof value === "string") : []);
  } catch {
    return new Set();
  }
}

function saveCollapsedBoardLanes(ref: string, workspaces: Set<string>): void {
  try { localStorage.setItem(boardLaneCollapseKey(ref), JSON.stringify([...workspaces].sort())); } catch { /* presentation state is best-effort */ }
}

async function moveBoardIssue(
  host: HTMLElement,
  issue: Issue,
  epic: string,
  target: BoardStatus,
  before = "",
  after = "",
): Promise<void> {
	const movedCardStatus = (): HTMLSelectElement | null =>
		host.classList.contains("board-card") && host.dataset.issue === issue.id
			? host.querySelector<HTMLSelectElement>(".board-status-select")
			: null;
  if (target === issue.status && (before === issue.id || after === issue.id)) return;
  if (!legalBoardTargets(issue, identity).includes(target)) {
    mutationError(host, new Error("This move would release somebody else's assignment."));
    return;
  }
  if (target === "closed" && issue.status !== "closed") {
    const confirmed = await confirmMutation(
      "Close issue?",
      `Close ${issue.id}? This can be reopened later.`,
      host,
      true,
    );
    if (!confirmed) {
		const control = movedCardStatus();
      if (control !== null) control.value = issue.status;
      return;
    }
  }
  host.classList.add("moving");
  try {
    await api.moveIssue(issue.id, {
      status: target,
      epic,
      ...(before === "" ? {} : { before }),
      ...(after === "" ? {} : { after }),
    });
    await render();
  } catch (error) {
    host.classList.remove("moving");
    mutationError(host, error);
		const control = movedCardStatus();
    if (control !== null) control.value = issue.status;
  }
}

type BoardEpicChoice = { id: string; workspace: string; title: string };

function boardCard(issue: Issue, epic: string, status: BoardStatus, epics: BoardEpicChoice[]): HTMLElement {
  const card = element("article", `board-card${issue.status === "closed" ? " closed" : ""}`);
  card.dataset.issue = issue.id;
  configureDragSurface(card, issue, () => { draggedBoardIssue = issue; }, () => { draggedBoardIssue = null; });
  const address = nameLink(`#/issues/${encodeURIComponent(issue.id)}`, issue.id, issue.title);
  const top = element("div", "board-card-top");
  top.append(address);
  card.append(top, issueBadges(issue));
  const move = element("div", "board-card-move");
  const epicLabel = element("label");
  epicLabel.append(document.createTextNode("Epic"));
  const epicSelect = document.createElement("select");
  epicSelect.className = "board-epic-select";
  epicSelect.setAttribute("aria-label", `Epic for ${issue.id}`);
  const noEpic = document.createElement("option");
  noEpic.value = "";
  noEpic.textContent = "No epic";
  noEpic.selected = epic === "";
  epicSelect.append(noEpic);
  for (const choice of epics.filter((candidate) => candidate.workspace === issue.workspace)) {
    const option = document.createElement("option");
    option.value = choice.id;
    option.textContent = choice.title;
    option.selected = choice.id === epic;
    epicSelect.append(option);
  }
  epicLabel.append(epicSelect);
  const statusLabel = element("label");
  statusLabel.append(document.createTextNode("Status"));
  const select = document.createElement("select");
  select.className = "board-status-select";
  select.setAttribute("aria-label", `Status for ${issue.id}`);
  for (const status of legalBoardTargets(issue, identity)) {
    const option = document.createElement("option");
    option.value = status;
    option.textContent = status === "closed" && status !== issue.status ? "Closed…" : boardStatusLabel(status);
    option.selected = status === issue.status;
    select.append(option);
  }
  epicSelect.addEventListener("change", () => void moveBoardIssue(card, issue, epicSelect.value, select.value as BoardStatus));
  select.addEventListener("change", () => void moveBoardIssue(card, issue, epicSelect.value, select.value as BoardStatus));
  statusLabel.append(select);
  move.append(epicLabel, statusLabel);
  card.append(move);
  card.addEventListener("dragover", (event) => {
    const moving = draggedBoardIssue;
    if (moving !== null && moving.id !== issue.id && moving.workspace === issue.workspace && legalBoardTargets(moving, identity).includes(status)) {
      event.preventDefault();
      event.stopPropagation();
      card.classList.add("drop-before");
    }
  });
  card.addEventListener("dragleave", () => card.classList.remove("drop-before"));
	card.addEventListener("drop", (event) => {
		const moving = draggedBoardIssue;
		card.classList.remove("drop-before");
		if (moving === null || moving.id === issue.id) return;
		event.preventDefault();
		event.stopPropagation();
		draggedBoardIssue = null;
		if (moving.workspace !== issue.workspace) {
			mutationError(card, new Error(`Issues cannot be reordered across workspaces (${moving.workspace} → ${issue.workspace}).`));
			return;
		}
		void moveBoardIssue(card, moving, epic, status, issue.id);
	});
  return card;
}

function syncBoardEpicChoices(root: HTMLElement, epics: BoardEpicChoice[], issuesByID: Map<string, Issue>): void {
  for (const card of root.querySelectorAll<HTMLElement>(".board-card")) {
    const issue = issuesByID.get(card.dataset.issue ?? "");
    const select = card.querySelector<HTMLSelectElement>(".board-epic-select");
    if (issue === undefined || select === null) continue;
    const existing = new Set([...select.options].map((option) => option.value));
    for (const choice of epics) {
      if (choice.workspace !== issue.workspace || existing.has(choice.id)) continue;
      const option = document.createElement("option");
      option.value = choice.id;
      option.textContent = choice.title;
      select.append(option);
    }
  }
}

function boardColumn(
  ref: string,
  epic: Issue | null,
  selectedWorkspaces: string[],
  column: Board["lanes"][number]["columns"][number],
  issuesByID: Map<string, Issue>,
  epics: BoardEpicChoice[],
): HTMLElement {
  const host = element("section", "board-column");
  host.dataset.status = column.status;
  const laneKey = epic?.id ?? "no-epic";
  const epicID = epic?.id ?? "";
  const headingID = `board-column-${laneKey}-${column.status}`;
  host.setAttribute("aria-labelledby", headingID);
  const heading = element("header");
  const title = element("h3", "", boardStatusLabel(column.status));
  title.id = headingID;
  const count = element("span", "board-column-count", String(column.total));
  const columnActions = element("div", "board-column-actions");
  columnActions.append(count);
  if (column.status !== "closed") {
    const quickCreate = issueCreateButton(
      "+",
      {
        workspace: epic?.workspace ?? (selectedWorkspaces.length === 1 ? selectedWorkspaces[0] : undefined),
        epic: epic ?? undefined,
        assignToMe: column.status === "in_progress",
      },
      "board-column-create",
    );
    quickCreate.setAttribute("aria-label", `Create ${boardStatusLabel(column.status).toLowerCase()} issue in ${epic?.title ?? "No epic"}`);
    quickCreate.title = column.status === "in_progress" ? "Create and assign to me" : "Create issue";
    columnActions.append(quickCreate);
  }
  heading.append(title, columnActions);
  host.append(heading);
  const cards = element("div", "board-cards");
  const loadedIDs = new Set<string>();
  const append = (issues: Issue[]): void => {
    for (const issue of issues) {
      if (loadedIDs.has(issue.id)) continue;
      loadedIDs.add(issue.id);
      issuesByID.set(issue.id, issue);
      cards.append(boardCard(issue, epicID, column.status, epics));
    }
  };
  append(column.issues);
  if (column.total === 0) cards.append(element("p", "board-column-empty", "No issues."));
  host.append(cards);
  if (column.issues.length < column.total) {
    let total = column.total;
    let cursor = column.issues.length;
    const more = button("", "secondary-button board-column-more");
    const labelMore = (): void => {
      const remaining = Math.max(0, total - loadedIDs.size);
      more.textContent = `Load ${Math.min(boardCardPageSize, remaining)} more · ${loadedIDs.size} of ${total}`;
    };
    labelMore();
    more.addEventListener("click", () => {
      more.disabled = true;
      void api.board(ref, {
        ...(epic === null && selectedWorkspaces.length > 0 ? { workspace: selectedWorkspaces } : {}),
        epic: epic?.id ?? "none", status: column.status, "lane-limit": 1,
        "card-limit": boardCardPageSize, "card-offset": cursor,
      }).then((page) => {
        const nextColumn = page.lanes[0]?.columns[0];
        if (nextColumn === undefined) { void render(); return; }
        const next = nextColumn.issues;
        cursor += next.length;
        total = nextColumn.total;
        count.textContent = String(total);
        append(next);
        if (loadedIDs.size > total || (cursor >= total && loadedIDs.size < total)) { void render(); return; }
        if (loadedIDs.size >= total) more.remove();
        else { more.disabled = false; labelMore(); }
      }).catch((error) => { more.disabled = false; mutationError(host, error); });
    });
    host.append(more);
  }
  host.addEventListener("dragover", (event) => {
    const issue = draggedBoardIssue;
    if (issue !== null && (epic === null || issue.workspace === epic.workspace) && legalBoardTargets(issue, identity).includes(column.status)) {
      event.preventDefault();
      host.classList.add("drop-target");
      return;
    }
    if (event.dataTransfer?.types.includes("text/plain") === true) event.preventDefault();
  });
  host.addEventListener("dragleave", (event) => {
    if (!(event.relatedTarget instanceof Node) || !host.contains(event.relatedTarget)) host.classList.remove("drop-target");
  });
  host.addEventListener("drop", (event) => {
    host.classList.remove("drop-target");
    const issue = draggedBoardIssue ?? issuesByID.get(event.dataTransfer?.getData("text/plain") ?? "");
    draggedBoardIssue = null;
    if (issue === undefined || issue === null) return;
    event.preventDefault();
    if (epic !== null && issue.workspace !== epic.workspace) {
      mutationError(host, new Error(`Issues cannot move out of workspace ${issue.workspace}.`));
      return;
    }
    void moveBoardIssue(host, issue, epicID, column.status);
  });
  return host;
}

function boardLane(ref: string, lane: Board["lanes"][number], selectedWorkspaces: string[], issuesByID: Map<string, Issue>, epics: BoardEpicChoice[]): HTMLElement {
  const host = element("section", "board-lane");
  const laneKey = lane.epic?.id ?? "no-epic";
  const laneLabel = lane.epic?.title ?? "No epic";
  const headingID = `board-lane-${laneKey}`;
  host.setAttribute("aria-labelledby", headingID);
  const heading = element("header", "board-lane-heading");
  const name = element("div", "board-lane-name");
  const title = element("h2");
  title.id = headingID;
  if (lane.epic === undefined) title.append(document.createTextNode("No epic"));
  else title.append(element("code", "", lane.epic.workspace), document.createTextNode(` ${lane.epic.title}`));
  name.append(title);
  const total = lane.columns.reduce((sum, column) => sum + column.total, 0);
  const meta = element("div", "board-lane-meta");
  meta.append(element("span", "board-lane-total", `${total} issue${total === 1 ? "" : "s"}`));
  const columns = element("div", "board-columns");
  columns.id = `board-lane-columns-${laneKey}`;
  for (const column of lane.columns) columns.append(boardColumn(ref, lane.epic ?? null, selectedWorkspaces, column, issuesByID, epics));
	let isCollapsed = collapsedBoardLanes(ref).has(laneKey);
  const toggle = button("", "secondary-button board-lane-toggle");
  toggle.setAttribute("aria-controls", columns.id);
  const sync = (): void => {
    columns.hidden = isCollapsed;
    host.classList.toggle("collapsed", isCollapsed);
    toggle.setAttribute("aria-expanded", String(!isCollapsed));
    toggle.setAttribute("aria-label", `${isCollapsed ? "Expand" : "Collapse"} ${laneLabel} swimlane`);
    toggle.textContent = isCollapsed ? "▸ Expand" : "▾ Collapse";
  };
	toggle.addEventListener("click", () => {
		isCollapsed = !isCollapsed;
		const collapsed = collapsedBoardLanes(ref);
		if (isCollapsed) collapsed.add(laneKey); else collapsed.delete(laneKey);
    saveCollapsedBoardLanes(ref, collapsed);
    sync();
  });
  meta.append(toggle);
  heading.append(name, meta);
  sync();
  host.append(heading, columns);
  return host;
}

async function openBoardViewEditor(source: BoardView | null, duplicate: boolean, route: Route): Promise<void> {
  let preferences: WorkspacePreference[] = [];
  try { preferences = await api.workspacePreferences(); } catch {
    const workspaces = await api.workspaces();
    preferences = workspaces.rows.map((workspace) => ({ workspace, ignored: false }));
  }
  const dialog = element("dialog", "board-view-dialog") as HTMLDialogElement;
  dialog.setAttribute("aria-labelledby", "board-view-dialog-heading");
  const form = element("form", "board-view-form") as HTMLFormElement;
  form.method = "dialog";
  const heading = element("h2", "", source === null ? "Save board view" : duplicate ? "Duplicate board view" : "Edit board view");
  heading.id = "board-view-dialog-heading";
  form.append(heading);
  const name = document.createElement("input");
  name.required = true; name.maxLength = 100; name.value = source === null ? "" : `${source.name}${duplicate ? " copy" : ""}`;
  form.append(field("Name", name));
  const shared = document.createElement("input"); shared.type = "checkbox"; shared.checked = !duplicate && (source?.shared ?? false);
  const sharedLabel = element("label", "board-view-check"); sharedLabel.append(shared, document.createTextNode("Anyone with the link can open this view"));
  form.append(sharedLabel);
  const all = document.createElement("input"); all.type = "checkbox"; all.checked = source?.all_workspaces ?? route.query.getAll("workspace").length === 0;
  const allLabel = element("label", "board-view-check"); allLabel.append(all, document.createTextNode("All visible workspaces")); form.append(allLabel);
  const selected = new Set(source?.workspaces ?? route.query.getAll("workspace"));
  const workspaces = element("fieldset", "board-view-workspaces"); workspaces.append(element("legend", "", "Selected workspaces"));
  for (const preference of preferences) {
    const row = element("label"); const input = document.createElement("input"); input.type = "checkbox"; input.value = preference.workspace.key; input.checked = selected.has(preference.workspace.key);
    row.append(input, document.createTextNode(`${preference.workspace.key} — ${preference.workspace.name}${preference.ignored ? " (ignored)" : ""}`)); workspaces.append(row);
  }
  workspaces.hidden = all.checked; all.addEventListener("change", () => { workspaces.hidden = all.checked; }); form.append(workspaces);
  const labels = document.createElement("input"); labels.value = source?.labels.join(", ") ?? ""; labels.placeholder = "release, frontend"; form.append(field("Labels (any)", labels));
  const assignees = document.createElement("input"); assignees.value = source?.assignees.join(", ") ?? ""; assignees.placeholder = "alex, sam"; form.append(field("Assignees (any)", assignees));
  const priority = select(["0", "1", "2", "3", "4"], String(source?.priority_max ?? 4)); form.append(field("Maximum priority", priority));
  const error = element("p", "edit-error");
  const actions = element("div", "board-view-dialog-actions");
  const cancel = button("Cancel"); cancel.addEventListener("click", () => dialog.close());
  const save = element("button", "primary-button", source === null ? "Save view" : duplicate ? "Duplicate" : "Save changes") as HTMLButtonElement; save.type = "submit";
  actions.append(cancel);
  if (source !== null && !duplicate && source.owner === identity) {
    const remove = button("Delete", "danger-button");
    remove.addEventListener("click", async () => {
      const confirmed = await confirmMutation(
        "Delete board view?",
        `Delete “${source.name}”? Issues are not affected.`,
        remove,
        true,
      );
      if (!confirmed) return;
      remove.disabled = true;
      void api.deleteBoardView(source.id).then(() => { dialog.close(); location.hash = "#/boards"; }).catch((reason) => { remove.disabled = false; error.textContent = String(reason); });
    });
    actions.append(remove);
  }
  actions.append(save); form.append(error, actions); dialog.append(form); document.body.append(dialog);
  dialog.addEventListener("close", () => dialog.remove());
  form.addEventListener("submit", (event) => {
    event.preventDefault(); save.disabled = true; error.textContent = "";
    const body: BoardViewCreate = { name: name.value, shared: shared.checked, all_workspaces: all.checked,
      workspaces: all.checked ? [] : [...workspaces.querySelectorAll<HTMLInputElement>('input:checked')].map((input) => input.value),
      labels: splitBoardFilter(labels.value), assignees: splitBoardFilter(assignees.value), priority_max: Number(priority.value) as 0|1|2|3|4 };
    let operation: Promise<BoardView>;
    if (source !== null && !duplicate) {
      const patch: BoardViewPatch = {};
      if (body.name !== source.name) patch.name = body.name;
      if (body.shared !== source.shared) patch.shared = body.shared;
      if (body.all_workspaces !== source.all_workspaces) patch.all_workspaces = body.all_workspaces;
      if (JSON.stringify(body.workspaces) !== JSON.stringify(source.workspaces)) patch.workspaces = body.workspaces;
      if (JSON.stringify(body.labels) !== JSON.stringify(source.labels)) patch.labels = body.labels;
      if (JSON.stringify(body.assignees) !== JSON.stringify(source.assignees)) patch.assignees = body.assignees;
      if (body.priority_max !== source.priority_max) patch.priority_max = body.priority_max;
      operation = api.updateBoardView(source.id, patch);
    } else {
      operation = api.createBoardView(body);
    }
    void operation.then((view) => { dialog.close(); location.hash = `#/boards/${view.id}`; }).catch((reason) => { save.disabled = false; error.textContent = reason instanceof Error ? reason.message : String(reason); });
  });
  dialog.showModal(); name.focus();
}

async function viewBoards(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  const ref = route.path[1] ?? "default";
  const filters: Parameters<typeof api.board>[1] = { "lane-limit": boardLanePageSize, "card-limit": boardCardPageSize };
  if (ref === "default") {
    const workspaces = route.query.getAll("workspace"); if (workspaces.length > 0) filters.workspace = workspaces;
  }
  const [board, owned] = await Promise.all([api.board(ref, filters, signal), api.boardViews()]);
  const view = element("div", "board-page");
  const heading = element("div", "board-heading");
  const title = element("div"); title.append(element("h1", "", "Boards"), element("p", "lede", "Move work through the existing awb workflow."));
  const actions = element("div", "board-view-actions");
  actions.append(issueCreateButton("New issue", {
    workspace: filters.workspace?.length === 1 ? filters.workspace[0] : undefined,
  }));
  const pickerLabel = element("label"); pickerLabel.append(element("span", "", "View"));
  const picker = document.createElement("select"); picker.setAttribute("aria-label", "Board view");
  const option = (id: string, label: string): void => { const item = document.createElement("option"); item.value = id; item.textContent = label; item.selected = id === ref; picker.append(item); };
  option("default", "Default board"); for (const item of owned) option(item.id, item.name);
  if (board.view !== undefined && !owned.some((item) => item.id === board.view?.id)) option(board.view.id, board.view.name);
  picker.addEventListener("change", () => { location.hash = picker.value === "default" ? "#/boards" : `#/boards/${picker.value}`; }); pickerLabel.append(picker); actions.append(pickerLabel);
  const saved = board.view;
  if (saved === undefined) {
    const save = button("Save as view"); save.addEventListener("click", () => void openBoardViewEditor(null, false, route)); actions.append(save);
  } else if (saved.owner === identity) {
    const edit = button("Edit view"); edit.addEventListener("click", () => void api.boardView(saved.id).then((full) => openBoardViewEditor(full, false, route))); actions.append(edit);
  } else {
    const duplicate = button("Duplicate"); duplicate.addEventListener("click", () => void openBoardViewEditor(saved, true, route)); actions.append(duplicate);
  }
  if (saved?.shared) {
    const share = button("Copy link", "primary-button");
    share.addEventListener("click", () => {
      if (navigator.clipboard === undefined) {
        mutationError(view, new Error("Copy is unavailable in this browser."));
        return;
      }
      void navigator.clipboard.writeText(location.href)
        .then(() => { share.textContent = "Copied"; })
        .catch((error: unknown) => mutationError(view, error));
    });
    actions.append(share);
  }
  heading.append(title, actions); view.append(heading);
  if (saved !== undefined) {
    const summary = element("section", "board-summary"); const owner = element("div"); owner.append(element("strong", "", saved.name), element("span", "", `${saved.shared ? "Shared" : "Private"} · owned by @${saved.owner}`));
    const chips = element("div", "board-filter-chips"); chips.append(element("span", "", saved.all_workspaces ? "All workspaces" : `${saved.workspaces.length} workspaces`));
    for (const label of saved.labels) chips.append(element("span", "", `#${label}`)); for (const assignee of saved.assignees) chips.append(element("span", "", `@${assignee}`)); chips.append(element("span", "", `P0–P${saved.priority_max}`)); summary.append(owner, chips); view.append(summary);
  }
  view.append(element("p", board.workspaces_omitted ? "board-scope-note warning" : "board-scope-note", board.workspaces_omitted
    ? "Some workspaces are archived or hidden by your access or ignored-workspace settings."
    : "Workspace access and your ignored-workspace settings always apply to this view."));
  const lanes = element("div", "board-lanes"); const issuesByID = new Map<string, Issue>();
  const selectedWorkspaces = filters.workspace ?? [];
  const epics: BoardEpicChoice[] = board.lanes.flatMap((lane) => lane.epic === undefined
    ? []
    : [{ id: lane.epic.id, workspace: lane.epic.workspace, title: lane.epic.title }]);
  const loadedLanes = new Set<string>();
  for (const lane of board.lanes) { const key = lane.epic?.id ?? "no-epic"; loadedLanes.add(key); lanes.append(boardLane(ref, lane, selectedWorkspaces, issuesByID, epics)); }
  if (board.lane_total === 0) lanes.append(element("p", "empty", "No epic lanes match this view."));
  view.append(lanes);
  if (board.lanes.length < board.lane_total) {
    let total = board.lane_total;
    let cursor = board.lanes.length;
    const more = button("", "secondary-button board-lanes-more");
    const labelMore = (): void => { more.textContent = `Load up to ${boardLanePageSize} more epics · ${loadedLanes.size} of ${total}`; };
    labelMore();
    more.addEventListener("click", () => {
      more.disabled = true; const nextFilters = { ...filters, "lane-offset": cursor };
      void api.board(ref, nextFilters).then((page) => {
        cursor += page.lanes.length;
        total = page.lane_total;
        for (const lane of page.lanes) {
          const key = lane.epic?.id ?? "no-epic";
          if (loadedLanes.has(key)) continue;
          loadedLanes.add(key);
          if (lane.epic !== undefined) epics.push({ id: lane.epic.id, workspace: lane.epic.workspace, title: lane.epic.title });
          lanes.append(boardLane(ref, lane, selectedWorkspaces, issuesByID, epics));
        }
        syncBoardEpicChoices(lanes, epics, issuesByID);
        if (loadedLanes.size > total || (cursor >= total && loadedLanes.size < total)) { void render(); return; }
        if (loadedLanes.size >= total) more.remove(); else { more.disabled = false; labelMore(); }
      }).catch((error) => { more.disabled = false; mutationError(view, error); });
    }); view.append(more);
  }
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

const workspaceSortKeys = ["key", "active", "updated"] as const;
const workspaceColumns: SortChoice[] = [
  { key: "key", label: "Workspace" },
  { key: "active", label: "Open" },
  { key: "updated", label: "Updated" },
];

function workspaceSortButton(route: Route, key: string, label: string, state: SortState): HTMLElement {
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
    const next = nextSortValue(query.get("sort"), key, workspaceSortKeys, "key");
    if (next === null) query.delete("sort");
    else query.set("sort", next);
    query.delete("page");
    location.hash = routeHref(route, query).slice(1);
  });
  return button;
}

function workspaceTable(route: Route, workspaces: Workspace[], state: SortState): HTMLElement {
  const table = element("table", "listing-table workspace-table") as HTMLTableElement;
  const head = document.createElement("thead");
  const heading = document.createElement("tr");
  for (const column of workspaceColumns) {
    const th = document.createElement("th");
    th.scope = "col";
    if (state.key === column.key) th.setAttribute("aria-sort", state.direction === "asc" ? "ascending" : "descending");
    const controls = element("div", "column-heading");
    controls.append(workspaceSortButton(route, column.key, column.label, state));
    if (column.key === "updated") controls.append(updatedDisplayControl());
    th.append(controls);
    heading.append(th);
  }
  head.append(heading);
  table.append(head);

  const body = document.createElement("tbody");
  for (const workspace of workspaces) {
    const row = document.createElement("tr");
    const href = `#/workspaces/${encodeURIComponent(workspace.key)}`;

    const workspaceCell = document.createElement("td");
    workspaceCell.dataset.label = "Workspace";
    workspaceCell.append(nameLink(href, workspace.key, workspace.name));
    if (workspace.state === "archived") workspaceCell.append(element("span", "listing-badge archived-badge", "Archived"));
    if (workspace.description !== "") {
      const description = element("div", "workspace-description markdown");
      description.innerHTML = renderMarkdown(workspace.description);
      workspaceCell.append(description);
    }
    row.append(workspaceCell);

    const active = document.createElement("td");
    active.dataset.label = "Open";
    active.append(element("span", "open-count", String(workspace.active_issues)));
    row.append(active);

    const updated = document.createElement("td");
    updated.dataset.label = "Updated";
    updated.append(updatedTimeElement(workspace.updated_at));
    row.append(updated);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewWorkspaces(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  const requested = pageNumber(route.query);
  const size = listingPageSize(route.query);
  const filters: WorkspaceFilters = {
    limit: size,
    offset: (requested - 1) * size,
  };
  const lifecycle = route.query.get("state") === "archived" ? "archived" : "active";
  filters.state = lifecycle;
  const filterText = route.query.get("filter");
  if (filterText !== null && filterText !== "") filters.filter = filterText;
  const sort = route.query.get("sort");
  const apiSorts = workspaceSortKeys.flatMap((key) => [key, `-${key}`]);
  if (sort !== null && apiSorts.includes(sort)) filters.sort = sort as WorkspaceFilters["sort"];
  let page = await api.workspaces(filters, signal);
  const normalized = normalizePageRoute(route, page.total);
  if ((filters.offset ?? 0) !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.workspaces(filters, signal);
  }

  const view = element("div");
  const heading = element("div", "workspaces-heading");
  heading.append(element("h1", "", "Workspaces"));
  const create = button("New workspace", "primary-button") as HTMLButtonElement;
  const createForm = workspaceCreateForm();
  createForm.id = "workspace-creator";
  createForm.hidden = true;
  create.setAttribute("aria-controls", createForm.id);
  create.setAttribute("aria-expanded", "false");
  if (await mayManageWorkspaces()) {
    heading.append(create);
    create.addEventListener("click", () => {
      createForm.hidden = !createForm.hidden;
      create.setAttribute("aria-expanded", String(!createForm.hidden));
      create.textContent = createForm.hidden ? "New workspace" : "Hide creator";
      if (!createForm.hidden) {
        activateMarkdownEditors(createForm);
        createForm.querySelector<HTMLInputElement>("input")?.focus();
      }
    });
  }
  view.append(heading, createForm);

  const tabs = element("div", "workspace-state-tabs");
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
  const state = sortState(route.query.get("sort"), workspaceSortKeys, "key");
  const listingActions = element("div", "listing-actions");
  listingActions.append(
    mobileSortControl(route, workspaceColumns, "Natural order"),
    mobileUpdatedDisplayControl(),
    pagination(route, page.total),
  );
  if (page.rows.length === 0) {
    host.append(element("p", "empty", filterText === null
      ? lifecycle === "archived" ? "No archived workspaces." : "No workspaces yet. Create one above or with: awb workspace create <key>"
      : "No workspaces match this filter."));
  } else host.append(workspaceTable(route, page.rows, state));
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

function workspaceCreateForm(): HTMLFormElement {
  const form = element("form", "edit-panel workspace-create-panel") as HTMLFormElement;
  form.append(element("h2", "", "Create workspace"), element("p", "muted", "The key becomes every issue ID prefix in this workspace and cannot be changed. Issues cannot move between workspaces."));
  const key = document.createElement("input");
  key.name = "key";
  key.required = true;
  key.maxLength = 16;
  key.pattern = "[a-z][a-z0-9-]*";
  const name = document.createElement("input");
  name.name = "name";
  name.maxLength = 500;
  const description = createMarkdownEditor("", "description", "Workspace description (Markdown)");
  const preview = element("p", "workspace-key-preview muted", "Issue IDs will use this key as their prefix.");
  key.addEventListener("input", () => {
    preview.textContent = key.value === "" ? "Issue IDs will use this key as their prefix." : `Issue IDs will start with ${key.value}-.`;
  });
  const submit = element("button", "primary-button", "Create workspace") as HTMLButtonElement;
  submit.type = "submit";
  form.append(
    field("Key", key),
    preview,
    field("Name (optional)", name),
    markdownField("Description (Markdown)", description),
    submit,
  );
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    submit.disabled = true;
    try {
      const workspace = await api.createWorkspace({
        key: key.value,
        name: name.value,
        description: description.textarea.value,
      });
      location.hash = `#/workspaces/${encodeURIComponent(workspace.key)}`;
    } catch (error) {
      submit.disabled = false;
      mutationError(form, error);
    }
  });
  return form;
}

function userTable(users: DirectoryUser[], manageable: boolean): HTMLElement {
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
    const name = element(manageable ? "a" : "span", "user-name");
    if (name instanceof HTMLAnchorElement) name.href = userEditorHref(user.name);
    const identityText = element("span", "user-identity");
    identityText.append(element("span", "user-full-name", user.full_name || user.name));
    if (user.full_name !== "") identityText.append(element("span", "muted", `@${user.name}`));
    name.append(avatar(user.name), identityText);
    userCell.append(name);
    row.append(userCell);

    const memberships = document.createElement("td");
    memberships.dataset.label = "Memberships";
    const membershipList = element("div", "user-workspaces");
    for (const membership of user.workspaces) {
      const workspace = element("span", "listing-badge user-workspace", membership.workspace);
      workspace.title = `${membership.access} access`;
      membershipList.append(workspace);
    }
    if (user.workspaces.length === 0) membershipList.append(element("span", "muted", "—"));
    memberships.append(membershipList);
    row.append(memberships);

    const activity = document.createElement("td");
    activity.dataset.label = "Activity";
    const activityList = element("div", "user-workspaces");
    for (const workspaceName of user.activity_workspaces) {
      activityList.append(element("span", "listing-badge user-workspace", workspaceName));
    }
    if (user.activity_workspaces.length === 0) activityList.append(element("span", "muted", "—"));
    activity.append(activityList);
    row.append(activity);

    const roles = document.createElement("td");
    roles.dataset.label = "Roles";
    const roleList = element("div", "user-roles");
    if (user.workspace_admin) roleList.append(element("span", "listing-badge", "workspace admin"));
    if (user.user_admin) roleList.append(element("span", "listing-badge", "user admin"));
    if (!user.workspace_admin && !user.user_admin) roleList.append(element("span", "muted", "member"));
    roles.append(roleList);
    row.append(roles);
    body.append(row);
  }
  table.append(body);
  return table;
}

async function viewUsers(route: Route, signal?: AbortSignal): Promise<HTMLElement> {
  await refreshCaller();
  const manageable = mayManageUsers;
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
  const heading = element("div", "directory-heading");
  heading.append(element("h1", "", "Users"));
  if (manageable) heading.append(link(userCreateHref, "Add user", "primary-button"));
  view.append(heading);

  const listing = element("div", "listing");
  const host = element("div", "listing-host");
  if (page.rows.length === 0) {
    host.append(element("p", "empty", filterText === null ? "No users yet." : "No users match this filter."));
  } else {
    host.append(userTable(page.rows, manageable));
  }
  listing.append(
    listingFilter(route, "Filter all users…", "user", page.total, pagination(route, page.total)),
    host,
  );
  view.append(listing);
  return view;
}

function accountAdminDenied(): ApiError {
  return new ApiError(403, "Only a user administrator may administer accounts.");
}

function formMessage(): HTMLElement {
  const message = element("p", "profile-form-message");
  message.setAttribute("aria-live", "polite");
  return message;
}

function setFormError(message: HTMLElement, error: unknown): void {
  message.className = "profile-form-message form-error";
  message.setAttribute("role", "alert");
  message.textContent = error instanceof ApiError ? error.message : String(error);
}

async function recoverStaleUser(error: unknown): Promise<boolean> {
  if (!(error instanceof ApiError) || error.status !== 412) return false;
  pendingNotice = {
    message: "This account changed elsewhere. The latest values have been loaded; review them and try again.",
    error: true,
  };
  await render();
  return true;
}

function userAccountForm(user: User, directory: DirectoryUser[]): HTMLFormElement {
  const form = element("form", "user-admin-form") as HTMLFormElement;
  const fullName = document.createElement("input");
  fullName.value = user.full_name;
  fullName.maxLength = 500;
  fullName.autocomplete = "name";
  const workspaceAdmin = document.createElement("input");
  workspaceAdmin.type = "checkbox";
  workspaceAdmin.checked = user.workspace_admin;
  const userAdmin = document.createElement("input");
  userAdmin.type = "checkbox";
  userAdmin.checked = user.user_admin;
  const workspaceAdminLabel = element("label", "check-field user-role-field");
  workspaceAdminLabel.append(workspaceAdmin, element("span", "", "Workspace administrator"));
  const userAdminLabel = element("label", "check-field user-role-field");
  userAdminLabel.append(userAdmin, element("span", "", "User administrator"));
  const lastUserAdmin = user.user_admin
    && directory.filter((candidate) => candidate.user_admin).length === 1;
  const roleWarning = element(
    "p",
    "profile-form-help",
    lastUserAdmin
      ? "This is the last user administrator. Removing that role leaves account administration available only through direct database access."
      : "Administrative roles are independent; grant only what this account needs.",
  );
  const submit = element("button", "primary-button", "Save changes") as HTMLButtonElement;
  submit.type = "submit";
  const message = formMessage();
  form.append(field("Full name", fullName), workspaceAdminLabel, userAdminLabel, roleWarning, submit, message);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (lastUserAdmin && !userAdmin.checked && !window.confirm(
      "This removes the last user administrator. Only direct database access can restore account administration. Continue?",
    )) return;
    form.setAttribute("aria-busy", "true");
    submit.disabled = true;
    message.textContent = "Saving…";
    message.className = "profile-form-message";
    try {
      await api.updateUser(user.name, {
        full_name: fullName.value,
        workspace_admin: workspaceAdmin.checked,
        user_admin: userAdmin.checked,
      });
      await refreshCaller();
      pendingNotice = { message: `@${user.name} was updated.`, error: false };
      if (!mayManageUsers) {
        location.hash = "#/users";
        return;
      }
      await render();
    } catch (error) {
      if (await recoverStaleUser(error)) return;
      setFormError(message, error);
      submit.disabled = false;
      form.removeAttribute("aria-busy");
    }
  });
  return form;
}

function userPasswordResetForm(user: User): HTMLFormElement {
  const form = element("form", "user-admin-form") as HTMLFormElement;
  const password = document.createElement("input");
  password.type = "password";
  password.required = true;
  password.maxLength = 72;
  password.autocomplete = "new-password";
  const confirmation = password.cloneNode() as HTMLInputElement;
  const submit = element("button", "secondary-button", "Reset password") as HTMLButtonElement;
  submit.type = "submit";
  const message = formMessage();
  form.append(
    element("p", "profile-form-help", "Set a new password. The current password is never shown."),
    field("New password", password),
    field("Confirm new password", confirmation),
    submit,
    message,
  );
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    message.className = "profile-form-message";
    if (password.value !== confirmation.value) {
      setFormError(message, new Error("The passwords do not match."));
      confirmation.focus();
      return;
    }
    form.setAttribute("aria-busy", "true");
    submit.disabled = true;
    message.textContent = "Resetting…";
    try {
      await api.updateUser(user.name, { password: password.value });
      form.reset();
      message.textContent = "Password reset.";
    } catch (error) {
      if (await recoverStaleUser(error)) return;
      setFormError(message, error);
    } finally {
      submit.disabled = false;
      form.removeAttribute("aria-busy");
    }
  });
  return form;
}

function userMembershipList(user: User): HTMLElement {
  const list = element("ul", "profile-workspaces");
  for (const membership of user.workspaces) {
    const item = element("li", "profile-workspace");
    item.append(
      link(
        `#/workspaces/${encodeURIComponent(membership.workspace)}/members`,
        membership.workspace,
        "profile-workspace-name",
      ),
      element("span", "profile-workspace-title", "Workspace Members page"),
      element("span", "listing-badge", membership.access),
    );
    list.append(item);
  }
  if (user.workspaces.length === 0) list.append(element("li", "empty", "No workspace memberships."));
  return list;
}

function userDeleteForm(user: User, directory: DirectoryUser[]): HTMLFormElement {
  const impact = userDeletionImpact(user, directory, identity);
  const form = element("form", "user-delete-form") as HTMLFormElement;
  const warning = element("p", "user-delete-warning", userDeletionWarning(user, impact));
  const confirmation = document.createElement("input");
  confirmation.autocomplete = "off";
  confirmation.placeholder = user.name;
  const submit = element("button", "danger-button", "Delete user") as HTMLButtonElement;
  submit.type = "submit";
  submit.disabled = true;
  const message = formMessage();
  confirmation.addEventListener("input", () => {
    submit.disabled = confirmation.value !== user.name;
  });
  form.append(
    warning,
    element("p", "profile-form-help", `Type ${user.name} to confirm.`),
    field("Username", confirmation),
    submit,
    message,
  );
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (confirmation.value !== user.name) return;
    form.setAttribute("aria-busy", "true");
    submit.disabled = true;
    message.textContent = "Deleting…";
    try {
      await api.deleteUser(user.name);
      pendingNotice = { message: `@${user.name} was deleted.`, error: false };
      location.hash = "#/users";
      if (parseRoute().path.length > 1) await render();
    } catch (error) {
      if (await recoverStaleUser(error)) return;
      setFormError(message, error);
      submit.disabled = false;
      form.removeAttribute("aria-busy");
    }
  });
  return form;
}

async function viewUserEditor(name: string, signal?: AbortSignal): Promise<HTMLElement> {
  await refreshCaller();
  if (!mayManageUsers) throw accountAdminDenied();
  const [user, directory] = await Promise.all([api.user(name), api.users({}, signal)]);
  const view = element("div", "profile-view user-admin-view");
  view.append(link("#/users", "← Users", "detail-back-link"));
  const heading = element("div", "profile-heading user-admin-heading");
  heading.append(avatar(user.name, "profile-avatar"));
  const title = element("div");
  title.append(
    element("h1", "", user.full_name || `@${user.name}`),
    element("p", "lede", user.full_name === "" ? "Account administration" : `@${user.name} · Account administration`),
  );
  heading.append(title);
  const cards = element("div", "user-admin-grid");
  const accountCard = element("section", "profile-card");
  accountCard.append(element("h2", "", "Account details"), userAccountForm(user, directory.rows));
  const passwordCard = element("section", "profile-card");
  passwordCard.append(element("h2", "", "Reset password"), userPasswordResetForm(user));
  const membershipCard = element("section", "profile-card");
  membershipCard.append(
    element("h2", "", "Workspaces"),
    element(
      "p",
      "profile-form-help",
      "Workspace memberships are read-only here. Manage access on each workspace's Members page.",
    ),
    userMembershipList(user),
  );
  const factsCard = element("section", "profile-card");
  const facts = element("dl", "profile-facts");
  facts.append(
    element("dt", "", "Username"), element("dd", "", user.name),
    element("dt", "", "Created"), element("dd", "", user.created_at),
    element("dt", "", "Updated"), element("dd", "", user.updated_at),
  );
  factsCard.append(element("h2", "", "Account information"), facts);
  const deleteCard = element("section", "profile-card user-delete-card");
  deleteCard.append(element("h2", "", "Delete account"), userDeleteForm(user, directory.rows));
  cards.append(accountCard, passwordCard, membershipCard, factsCard, deleteCard);
  view.append(heading, cards);
  return view;
}

function userCreateForm(): HTMLFormElement {
  const form = element("form", "user-admin-form user-create-form") as HTMLFormElement;
  const username = document.createElement("input");
  username.required = true;
  username.maxLength = 64;
  username.autocomplete = "username";
  const fullName = document.createElement("input");
  fullName.maxLength = 500;
  fullName.autocomplete = "name";
  const password = document.createElement("input");
  password.type = "password";
  password.required = true;
  password.maxLength = 72;
  password.autocomplete = "new-password";
  const confirmation = password.cloneNode() as HTMLInputElement;
  const workspaceAdmin = document.createElement("input");
  workspaceAdmin.type = "checkbox";
  const userAdmin = document.createElement("input");
  userAdmin.type = "checkbox";
  const workspaceAdminLabel = element("label", "check-field user-role-field");
  workspaceAdminLabel.append(workspaceAdmin, element("span", "", "Workspace administrator"));
  const userAdminLabel = element("label", "check-field user-role-field");
  userAdminLabel.append(userAdmin, element("span", "", "User administrator"));
  const submit = element("button", "primary-button", "Create user") as HTMLButtonElement;
  submit.type = "submit";
  const message = formMessage();
  form.append(
    field("Username", username), field("Full name", fullName),
    field("Password", password), field("Confirm password", confirmation),
    workspaceAdminLabel, userAdminLabel, submit, message,
  );
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    message.className = "profile-form-message";
    if (password.value !== confirmation.value) {
      setFormError(message, new Error("The passwords do not match."));
      confirmation.focus();
      return;
    }
    form.setAttribute("aria-busy", "true");
    submit.disabled = true;
    message.textContent = "Creating…";
    const body: UserCreate = {
      name: username.value,
      full_name: fullName.value,
      password: password.value,
      workspace_admin: workspaceAdmin.checked,
      user_admin: userAdmin.checked,
    };
    try {
      const created = await api.createUser(body);
      pendingNotice = { message: `@${created.name} was created.`, error: false };
      location.hash = userEditorHref(created.name);
    } catch (error) {
      setFormError(message, error);
      submit.disabled = false;
      form.removeAttribute("aria-busy");
    }
  });
  return form;
}

async function viewUserCreate(): Promise<HTMLElement> {
  await refreshCaller();
  if (!mayManageUsers) throw accountAdminDenied();
  const view = element("div", "profile-view user-admin-view");
  view.append(link("#/users", "← Users", "detail-back-link"));
  const heading = element("div", "settings-heading");
  heading.append(element("h1", "", "Add user"), element("p", "lede", "Create an account and its initial roles."));
  const card = element("section", "profile-card user-create-card");
  card.append(element("h2", "", "Account"), userCreateForm());
  view.append(heading, card);
  return view;
}

async function viewWorkspace(key: string, signal?: AbortSignal): Promise<HTMLElement> {
  const [workspace, activity, canManage, memberPage, currentUser] = await Promise.all([
    api.workspace(key),
    api.workspaceActivity(key),
    mayManageWorkspaces(),
    api.workspaceMembers(key, signal),
    identity === "" ? Promise.resolve(null) : api.user(identity),
  ]);
  const view = element("div", "workspace-view");
  if (workspace.state === "archived") {
    const banner = element("div", "workspace-archive-banner");
    banner.setAttribute("role", "status");
    banner.append(
      element("strong", "", "Archived"),
      document.createTextNode("This workspace is retained as read-only history. Restore it to resume work inside the same workspace boundary."),
    );
    view.append(banner);
  }
  const heading = element("div", "detail-heading");
  const title = element("div");
  title.append(element("div", "issue-key", workspace.key), element("h1", "", workspace.name));
  const edit = button("Edit workspace");
  heading.append(title);
  if (canManage && workspace.state === "active") heading.append(edit);
  view.append(heading);

  const form = workspaceEditForm(workspace);
  form.hidden = true;
  edit.addEventListener("click", () => {
    form.hidden = !form.hidden;
    edit.textContent = form.hidden ? "Edit workspace" : "Hide editor";
    if (!form.hidden) {
      activateMarkdownEditors(form);
      form.querySelector<HTMLInputElement>("input")?.focus();
    }
  });
  view.append(form);

  const description = element("section", "workspace-detail-description");
  description.append(element("h2", "", "Description"));
  if (workspace.description === "") description.append(element("p", "empty", "No description."));
  else {
    const body = element("div", "markdown");
    body.innerHTML = renderMarkdown(workspace.description);
    description.append(body);
  }
  view.append(description);
  const facts = element("p", "workspace-facts");
  facts.append(
    document.createTextNode(`${workspace.active_issues} open issue${workspace.active_issues === 1 ? "" : "s"} · Updated `),
    updatedTimeElement(workspace.updated_at),
  );
  view.append(facts, link(
    `#/issues?workspace=${encodeURIComponent(workspace.key)}${workspace.state === "archived" ? "&include-archived=true&include-closed=true" : ""}`,
    workspace.state === "archived" ? "View this workspace's historical issues" : "View this workspace's issues",
    "action",
  ));
  view.append(workspaceLifecycleCard(workspace, activity.rows, canManage));
  view.append(workspaceMembershipSection(workspace, memberPage.rows, currentUser));
  return view;
}

async function changeWorkspaceMembership(
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
      location.hash = "#/workspaces";
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

function workspaceMembershipSection(workspace: Workspace, members: Membership[], currentUser: User | null): HTMLElement {
  const section = element("section", "profile-card membership-card");
  const heading = element("div", "membership-heading");
  const title = element("div");
  title.append(
    element("h2", "", "Workspace members"),
    element(
      "p",
      "membership-help",
      "Membership grants access to this workspace. It is separate from each user's ignored-workspace preference.",
    ),
  );
  heading.append(title, element("span", "membership-count", String(members.length)));
  section.append(heading);

  const manageable = mayManageWorkspaceMembership(identity, currentUser, workspace.key, members);
  if (manageable) section.append(workspaceMembershipEditor(workspace, members));
  else section.append(element("p", "membership-help", "Workspace administrators can change membership and access."));

  if (members.length === 0) {
    section.append(element("p", "empty", "No stored members. Global workspace administrators still have access."));
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
        void changeWorkspaceMembership(
          accessCell,
          rowControls,
          () => api.setWorkspaceMember(workspace.key, member.user, next),
          `@${member.user} now has ${next} access to workspace ${workspace.key}.`,
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
      remove.setAttribute("aria-label", `Remove @${member.user} from workspace ${workspace.key}`);
      rowControls.push(remove);
      remove.addEventListener("click", () => {
        if (!window.confirm(membershipChangeConfirmation(member, members, identity, null))) return;
        const losesAccess = member.user === identity && currentUser?.workspace_admin !== true;
        void changeWorkspaceMembership(
          actions,
          rowControls,
          () => api.removeWorkspaceMember(workspace.key, member.user),
          `@${member.user} was removed from workspace ${workspace.key}.`,
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

function workspaceMembershipEditor(workspace: Workspace, members: Membership[]): HTMLFormElement {
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
    void changeWorkspaceMembership(
      form,
      [input, access, add],
      () => api.addWorkspaceMember(workspace.key, user, next),
      `@${user} was added with ${next} access to workspace ${workspace.key}.`,
      false,
      false,
      true,
    );
  });
  return form;
}

async function viewWorkspaceMembership(key: string, signal?: AbortSignal): Promise<HTMLElement> {
  if (identity === "") throw new Error("No authenticated user is available.");
  const [preferences, memberPage, currentUser] = await Promise.all([
    api.workspacePreferences(),
    api.workspaceMembers(key, signal),
    api.user(identity),
  ]);
  const preference = preferences.find((candidate) => candidate.workspace.key === key);
  if (preference === undefined) throw new ApiError(404, `no such workspace: ${key}`);

  const view = element("div", "workspace-view membership-admin-view");
  const heading = element("div", "detail-heading");
  const title = element("div");
  title.append(
    element("div", "issue-key", preference.workspace.key),
    element("h1", "", preference.workspace.name),
    element("p", "lede", preference.ignored ? "Ignored workspace administration" : "Workspace administration"),
  );
  heading.append(title, link("#/settings", "Back to settings", "secondary-button"));
  view.append(heading, workspaceMembershipSection(preference.workspace, memberPage.rows, currentUser));
  return view;
}

function workspaceLifecycleCard(workspace: Workspace, activity: WorkspaceActivity[], canManage: boolean): HTMLElement {
  const card = element("section", "workspace-lifecycle-card");
  card.append(element("h2", "", "Lifecycle"));
  if (workspace.state === "archived") {
    card.append(element("p", "", "Issues, comments, attachments, transitions and relations are read-only while this workspace is archived. Issues remain in this workspace and cannot be transferred elsewhere."));
    if (workspace.archived_at !== "") {
      const meta = element("p", "muted", `Archived${workspace.archived_by === "" ? "" : ` by @${workspace.archived_by}`} · `);
      meta.append(updatedTimeElement(workspace.archived_at));
      card.append(meta);
    }
    if (canManage) {
      const restore = element("button", "primary-button", "Restore workspace") as HTMLButtonElement;
      restore.type = "button";
      restore.addEventListener("click", () => void mutate(card, [restore], () => api.restoreWorkspace(workspace.key)));
      card.append(restore);
    }
  } else {
    card.append(element("p", "", "Archive this workspace to remove it from everyday discovery and make its retained work read-only. Its issues keep their stable workspace-prefixed IDs."));
    if (canManage) card.append(workspaceArchiveConfirmation(workspace, card));
  }
  if (activity.length > 0) {
    card.append(element("h3", "", "Lifecycle history"));
    const list = element("ol", "workspace-lifecycle-history");
    for (const entry of activity) {
      const item = document.createElement("li");
      item.append(document.createTextNode(`${entry.action === "archived" ? "Archived" : "Restored"}${entry.actor === "" ? "" : ` by @${entry.actor}`} · `), updatedTimeElement(entry.created_at));
      list.append(item);
    }
    card.append(list);
  }
  return card;
}

function workspaceArchiveConfirmation(workspace: Workspace, host: HTMLElement): HTMLElement {
  const form = element("form", "workspace-archive-form") as HTMLFormElement;
  const input = document.createElement("input");
  input.placeholder = workspace.key;
  input.setAttribute("aria-label", `Type ${workspace.key} to confirm`);
  const archive = element("button", "archive-button", "Archive workspace") as HTMLButtonElement;
  archive.type = "submit";
  archive.disabled = true;
  input.addEventListener("input", () => { archive.disabled = input.value !== workspace.key; });
  form.append(element("label", "", `Type ${workspace.key} to confirm`), input, archive);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (input.value !== workspace.key) return;
    void mutate(host, [input, archive], () => api.archiveWorkspace(workspace.key));
  });
  return form;
}

function workspaceEditForm(workspace: Workspace): HTMLFormElement {
  const form = element("form", "edit-panel") as HTMLFormElement;
  form.append(element("h2", "", "Edit workspace"));
  const name = document.createElement("input");
  name.value = workspace.name;
  name.maxLength = 500;
  const description = createMarkdownEditor(workspace.description, undefined, "Workspace description (Markdown)");
  const save = element("button", "primary-button", "Save changes") as HTMLButtonElement;
  save.type = "submit";
  form.append(field("Name", name), markdownField("Description (Markdown)", description), save);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(form, [save], () => api.updateWorkspace(workspace.key, {
      name: name.value,
      description: description.textarea.value,
    }));
  });
  return form;
}

async function viewIssue(id: string): Promise<HTMLElement> {
  const issue = await api.issue(id);
  const [activity, workspace] = await Promise.all([api.activity(id), api.workspace(issue.workspace)]);

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
    if (show) activateMarkdownEditors(editForm);
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
  if (workspace.state === "archived") {
    view.classList.add("archived-workspace-issue");
    const banner = element("div", "workspace-archive-banner issue-archive-banner");
    banner.setAttribute("role", "status");
    banner.append(
      element("strong", "", "Read-only"),
      document.createTextNode(`Workspace ${workspace.key} is archived.`),
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
  if (existingDraft !== undefined && workspace.state === "active") showEditor(true);
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
      remove.addEventListener("click", async () => {
        const addressed = relation.direction === "in" ? relation.other : issue.id;
        const addressedOther = relation.direction === "in" ? issue.id : relation.other;
        const confirmed = await confirmMutation(
          "Remove relation?",
          `${addressed} — ${relation.type} — ${addressedOther}`,
          remove,
          true,
        );
        if (!confirmed) return;
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
  const editor = issueForm(
    "Edit issue",
    "Save changes",
    draft?.title ?? issue.title,
    draft?.description ?? issue.description,
    "edit-panel issue-edit-form",
  );
  editor.actions.append(
    element("span", "edit-shortcut-hint", "Esc to hide · Ctrl/⌘+Enter to save"),
    editor.submit,
  );
  const rememberDraft = (): void => {
    issueEditDrafts.set(issue.id, { title: editor.title.value, description: editor.description.textarea.value });
  };
  editor.title.addEventListener("input", rememberDraft);
  editor.description.textarea.addEventListener("input", rememberDraft);
  editor.form.addEventListener("submit", (event) => {
    event.preventDefault();
    void mutate(editor.form, [editor.submit], async () => {
      const updated = await api.updateIssue(issue.id, {
        title: editor.title.value,
        description: editor.description.textarea.value,
      });
      issueEditDrafts.delete(issue.id);
      return updated;
    });
  });
  return editor.form;
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
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const confirmed = await confirmMutation(
      "Add relation?",
      `${issueID} — ${type.value} — ${other.value}`,
      add,
    );
    if (!confirmed) return;
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
  const upload = async (selected: File): Promise<void> => {
    const confirmed = await confirmMutation(
      "Upload attachment?",
      `Add ${selected.name} (${formatSize(selected.size)}) to ${issueID}.`,
      file,
    );
    if (!confirmed) {
      file.value = "";
      return;
    }
    void mutate(form, [file], () => api.addAttachment(issueID, selected));
  };
  file.addEventListener("change", () => {
    const selected = file.files?.[0];
    if (selected !== undefined) void upload(selected);
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
    if (selected !== undefined) void upload(selected);
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
  add("Workspace", link(`#/workspaces/${encodeURIComponent(issue.workspace)}`, issue.workspace));
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
      api.workspaceMembers(issue.workspace, signal),
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
    remove.addEventListener("click", async () => {
      const confirmed = await confirmMutation(
        "Remove attachment?",
        `Remove ${attachment.name} from ${attachment.issue}.`,
        remove,
        true,
      );
      if (!confirmed) return;
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
  nav.append(navLink(workspaceScopedHref("ready", route.query), "Ready", "ready"));
  nav.append(navLink(workspaceScopedHref("issues", route.query), "Issues", "issues"));
  nav.append(navLink(workspaceScopedHref("blocked", route.query), "Blocked", "blocked"));
  nav.append(navLink(workspaceScopedHref("boards", route.query), "Boards", "boards"));
  nav.append(navLink(workspaceScopedHref("workspaces", route.query), "Workspaces", "workspaces"));
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

function profileWorkspaceList(user: User, workspaces: Workspace[]): HTMLElement {
  const list = element("ul", "profile-workspaces");
  const memberships = new Map(user.workspaces.map((membership) => [membership.workspace, membership.access]));
  for (const workspace of workspaces) {
    const item = element("li", "profile-workspace");
    const query = new URLSearchParams({ workspace: workspace.key });
    item.append(link(`#/issues?${query.toString()}`, workspace.key, "profile-workspace-name"));
    if (workspace.name !== "") item.append(element("span", "profile-workspace-title", workspace.name));
    const access = user.workspace_admin ? "admin" : memberships.get(workspace.key);
    if (access !== undefined) item.append(element("span", "listing-badge", access));
    list.append(item);
  }
  if (workspaces.length === 0) list.append(element("li", "empty", "No workspace access."));
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
  const [user, workspaces] = await Promise.all([api.user(identity), api.workspaces()]);
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
  access.append(element("h2", "", "Workspace access"), profileWorkspaceList(user, workspaces.rows));
  const security = element("section", "profile-card");
  security.append(element("h2", "", "Password"), passwordForm(user));
  view.append(details, profile, access, security);
  return view;
}

function ignoredWorkspacesSettingsCard(workspaces: WorkspacePreference[]): HTMLElement {
  const card = element("section", "profile-card ignored-workspaces-card");
  const heading = element("div", "ignored-workspaces-heading");
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
  const summary = element("span", "ignored-summary", workspacePreferenceSummary(workspaces));
  heading.append(copy, summary);

  const filterLabel = element("label", "workspace-preference-filter");
  filterLabel.append(element("span", "visually-hidden", "Find a workspace"));
  const filter = document.createElement("input");
  filter.type = "search";
  filter.placeholder = "Find a workspace by name or key";
  const filterControl = element("span", "search-control workspace-preference-search");
  const filterClear = attachSearchClear(filter);
  filterControl.append(filter, filterClear.button);
  filterLabel.append(filterControl);

  const list = element("ul", "workspace-preference-list");
  const rows = new Map<WorkspacePreference, HTMLElement>();
  for (const preference of workspaces) {
    const row = element("li", `workspace-preference-row${preference.ignored ? " ignored" : ""}`);
    const name = element("span", "workspace-preference-identity");
    name.append(
      element("code", "", preference.workspace.key),
      element("span", "", preference.workspace.name),
    );
    const state = element(
      "span",
      `workspace-preference-state ${preference.ignored ? "ignored-state" : "active-state"}`,
      preference.ignored ? "Ignored" : "Active",
    );
    const action = element(
      "button",
      "secondary-button workspace-preference-action",
      preference.ignored ? "Re-enable" : "Ignore",
    ) as HTMLButtonElement;
    action.type = "button";
    action.addEventListener("click", () => {
      void mutate(row, [action], () => api.setWorkspaceIgnored(
        preference.workspace.key, !preference.ignored,
      ));
    });
    const actions = element("span", "workspace-preference-actions");
    actions.append(link(
      `#/workspaces/${encodeURIComponent(preference.workspace.key)}/members`,
      "Members",
      "secondary-button workspace-preference-members",
    ));
    actions.append(action);
    row.append(name, state, actions);
    rows.set(preference, row);
    list.append(row);
  }
  const empty = element("p", "workspace-preference-empty empty", "No authorized workspaces match your search.");
  empty.hidden = workspaces.length !== 0;
  const refresh = (): void => {
    const visible = new Set(filterWorkspacePreferences(workspaces, filter.value));
    for (const [preference, row] of rows) row.hidden = !visible.has(preference);
    empty.hidden = visible.size !== 0;
  };
  filter.addEventListener("input", refresh);
  card.append(heading, filterLabel, list, empty);
  return card;
}

async function viewSettings(): Promise<HTMLElement> {
  if (identity === "") throw new Error("No authenticated user is available.");
  let workspacePreferences: WorkspacePreference[] | null = null;
  try {
    workspacePreferences = await api.workspacePreferences();
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
  if (workspacePreferences !== null) view.append(ignoredWorkspacesSettingsCard(workspacePreferences));
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
  const loading = element("p", "route-loading", "Loading…");
  loading.setAttribute("role", "status");
  loading.setAttribute("aria-live", "polite");
  main.append(loading);
  app.append(main);

  try {
    const view = await routeView(route, request.signal);
    if (generation !== renderGeneration || request.signal.aborted) return;
    clear(main);
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
    clear(main);
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
    case "boards":
      return viewBoards(route, signal);
    case "workspaces":
    case "workspaces":
      if (route.path.length > 2 && route.path[2] === "members") {
        return viewWorkspaceMembership(route.path[1], signal);
      }
      return route.path.length > 1 ? viewWorkspace(route.path[1], signal) : viewWorkspaces(route, signal);
    case "profile":
      return viewProfile();
    case "settings":
      return viewSettings();
    case "users":
      if (route.path[1] === "-" && route.path[2] === "new") return viewUserCreate();
      return route.path.length > 1
        ? viewUserEditor(userNameFromRouteSegment(route.path[1]), signal)
        : viewUsers(route, signal);
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
  const current = rawCurrent === "workspaces" ? "workspaces" : rawCurrent;
  for (const anchor of app.querySelectorAll("nav a")) {
    const target = navigationPath(anchor.getAttribute("href") ?? "");
    anchor.classList.toggle("active", target === current);
  }
}

async function start(): Promise<void> {
  await refreshCaller();
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
        location.hash = destination.workspaceScoped === undefined
          ? destination.path
          : workspaceScopedHref(destination.workspaceScoped, route.query);
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

async function refreshCaller(): Promise<void> {
  // Account administration can change the current caller's workspace role.
  // Force the next workspace view to resolve that effective capability again.
  workspaceManager = null;
  try {
    const caller = await api.identity();
    identity = caller.identity;
    mayManageUsers = caller.may_manage_users;
  } catch {
    // A server that cannot say who the caller is still browses fine.
    identity = "";
    mayManageUsers = false;
  }
}

void start();
