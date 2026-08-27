// The bundled read-only web UI: projects, issues, search and dependency trees,
// over the same HTTP API anything else would use.

import { api, ApiError, type Facet, type Filters, type Issue, type IssueTree, type Project } from "./api.js";
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

function element(tag: string, className = "", text = ""): HTMLElement {
  const node = document.createElement(tag);
  if (className !== "") node.className = className;
  if (text !== "") node.textContent = text;
  return node;
}

function clear(node: HTMLElement): void {
  node.replaceChildren();
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

function issueRow(issue: Issue): HTMLElement {
  const row = element("li", "issue-row");
  row.append(link(`#/issues/${issue.id}`, issue.id, "id"));
  row.append(element("span", "title", issue.title));
  row.append(issueBadges(issue));
  return row;
}

function issueList(issues: Issue[], total: number, emptyMessage: string): HTMLElement {
  if (issues.length === 0) return element("p", "empty", emptyMessage);

  const section = element("div");
  const list = element("ul", "issues");
  for (const issue of issues) list.append(issueRow(issue));
  section.append(list);

  if (total !== issues.length) {
    section.append(element("p", "count", `Showing ${issues.length} of ${total}.`));
  } else {
    section.append(element("p", "count", `${total} issue${total === 1 ? "" : "s"}.`));
  }
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
  const sort = query.get("sort");
  if (sort !== null) filters.sort = sort;
  return filters;
}

/** facetBar renders the label and assignee menus a UI narrows with. */
function facetBar(route: Route, labels: Facet[], assignees: Facet[]): HTMLElement {
  const bar = element("div", "facets");

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
        `#/${route.path.join("/")}?${query.toString()}`,
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
  const [page, labels, assignees] = await Promise.all([
    kind === "ready" ? api.ready(filters) : kind === "blocked" ? api.blocked(filters) : api.issues(filters),
    api.labels(kind === "ready" ? {} : filters),
    api.assignees(kind === "ready" ? {} : filters),
  ]);

  const view = element("div");
  view.append(element("h1", "", titleFor(kind)));
  view.append(element("p", "lede", ledeFor(kind)));
  view.append(facetBar(route, labels.rows, assignees.rows));
  view.append(issueList(page.rows, page.total, emptyFor(kind)));
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

async function viewProjects(): Promise<HTMLElement> {
  const page = await api.projects();

  const view = element("div");
  view.append(element("h1", "", "Projects"));
  if (page.rows.length === 0) {
    view.append(element("p", "empty", "No projects yet. Create one with: awb project add <key>"));
    return view;
  }

  const list = element("ul", "projects");
  for (const project of page.rows) {
    list.append(projectRow(project));
  }
  view.append(list);
  return view;
}

function projectRow(project: Project): HTMLElement {
  const row = element("li", "project-row");
  row.append(link(`#/issues?project=${encodeURIComponent(project.key)}`, project.key, "id"));
  row.append(element("span", "title", project.name));
  row.append(element("span", "count", `${project.active_issues} open`));
  if (project.description !== "") {
    const description = element("div", "markdown");
    description.innerHTML = renderMarkdown(project.description);
    row.append(description);
  }
  return row;
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
  row.append(link(`#/issues/${node.id}`, node.id, "id"));
  row.append(element("span", "title", node.title));
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

  const filters: Filters = { q: terms, ...filtersFrom(route.query) };
  const page = await api.search(filters);
  view.append(issueList(page.rows, page.total, `Nothing matches ${terms.join(" ")}.`));
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
      return viewProjects();
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
