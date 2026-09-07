import { useEffect, useState } from "preact/hooks";
import {
  api,
  readyFilters,
  blockedFilters,
  facetFilters,
  readyFacetFilters,
  type Filters,
} from "../api.js";
import {
  nextSortValue,
  sortState,
  epicParentFilter,
  epicSelectionFrom,
  pageNumber,
  pageWindow,
  withPage,
  lowestFacetGroup,
  rankCountedFilterSuggestions,
} from "../listings.js";
import {
  routeHref,
  facetHref,
  replaceRoute,
  type Route,
} from "../routing/route.js";
import {
  ErrorMessage,
  Loading,
  SearchInput,
  Pagination,
  useResource,
  listingPageSize,
  UpdatedDisplayControl,
} from "../components/ui.js";
import {
  IssueTable,
  issueSortKeys,
  issueColumns,
  type ListingKind,
} from "../components/issues.js";
import { IssueCreateButton } from "../components/issue-form.js";
import {
  DynamicFilterRow,
  EpicFilterRow,
  ListingFilters,
} from "../components/listing-filters.js";
import {
  selectedIssueStatuses,
  hasEmptyStatusSelection,
} from "../status-filter.js";
import { hasEmptyIssueTypeSelection } from "../type-filter.js";
export function filtersFrom(query: URLSearchParams): Filters {
  const result: Filters = {
    limit: listingPageSize(query),
    offset: (pageNumber(query) - 1) * listingPageSize(query),
  };
  for (const key of ["workspace", "label", "assignee"] as const) {
    const values = query.getAll(key);
    if (values.length) result[key] = values;
  }
  for (const key of ["include-closed", "include-archived"] as const)
    if (query.get(key) === "true") result[key] = true;
  const types = query
    .getAll("type")
    .filter((v) => ["epic", "feature", "bug", "task", "chore"].includes(v));
  if (types.length) result.type = types as Filters["type"];
  const priorities = query
    .getAll("priority")
    .filter((v) => /^[0-4]$/.test(v))
    .map(Number);
  if (priorities.length) result.priority = priorities as Filters["priority"];
  const maximum = query.get("priority-max");
  if (maximum !== null && /^[0-4]$/.test(maximum))
    result["priority-max"] = Number(maximum) as Filters["priority-max"];
  const statuses = selectedIssueStatuses(query);
  if (query.has("status") && statuses.length) result.status = statuses;
  Object.assign(result, epicParentFilter(query));
  if (query.get("filter")) result.filter = query.get("filter")!;
  const sort = query.get("sort");
  if (sort && issueSortKeys.flatMap((k) => [k, `-${k}`]).includes(sort))
    result.sort = sort as Filters["sort"];
  return result;
}
export function ListingPage({
  route,
  kind,
}: {
  route: Route;
  kind: ListingKind;
}) {
  const [filter, setFilter] = useState(route.query.get("filter") ?? "");
  useEffect(
    () => setFilter(route.query.get("filter") ?? ""),
    [route.query.toString()],
  );
  useEffect(() => {
    if (filter === (route.query.get("filter") ?? "")) return;
    const timer = setTimeout(
      () => {
        const query = new URLSearchParams(route.query);
        query.delete("page");
        if (filter) query.set("filter", filter);
        else query.delete("filter");
        replaceRoute(route, query);
      },
      filter ? 200 : 0,
    );
    return () => clearTimeout(timer);
  }, [filter]);
  const resource = useResource(async () => {
    const filters = filtersFrom(route.query);
    const load = () =>
      kind === "issues" && hasEmptyStatusSelection(route.query)
        ? Promise.resolve({ rows: [], total: 0 })
        : kind === "issues" && hasEmptyIssueTypeSelection(route.query)
        ? Promise.resolve({ rows: [], total: 0 })
        : kind === "ready"
          ? api.ready(readyFilters(filters))
          : kind === "blocked"
            ? api.blocked(blockedFilters(filters))
            : api.issues(filters);
    const [page, workspaces] = await Promise.all([
      load(),
      api.workspaces(filters["include-archived"] ? { state: "all" } : {}),
    ]);
    const normalized = pageWindow(
      page.total,
      pageNumber(route.query),
      listingPageSize(route.query),
    ).page;
    if (normalized !== pageNumber(route.query)) {
      replaceRoute(route, withPage(route.query, normalized));
      return;
    }
    return { page, workspaces };
  }, [kind, route.query.toString()]);
  const data = resource.data;
  const state = sortState(
    route.query.get("sort"),
    issueSortKeys,
    "order",
    "asc",
  );
  const sort = (key: string) => {
    const query = new URLSearchParams(route.query);
    const value = nextSortValue(
      query.get("sort"),
      key,
      issueSortKeys,
      "order",
      "asc",
    );
    query.delete("page");
    if (value) query.set("sort", value);
    else query.delete("sort");
    location.hash = routeHref(route, query);
  };
  const titles = { issues: "Issues", ready: "Ready", blocked: "Blocked" };
  const ledes = {
    issues: "All issues in the selected workspaces.",
    ready: "Open, unblocked and unassigned. Pick one up.",
    blocked: "Work waiting on something else.",
  };
  const lowest = lowestFacetGroup(
    [],
    kind === "ready" ? null : [],
  );
  const group = (
    name: string,
    title: string,
    values: Array<{ value: string; count?: number }>,
  ) => (
    <div
      class={`facet-group ${name === "workspace" ? "workspaces" : ""} ${lowest === name ? "with-pagination" : ""}`}
    >
      <span class="facet-title">{title}</span>
      <span class="facet-values">
        {values.length ? (
          values.map((item) => (
            <a
              key={item.value}
              class={`facet ${route.query.getAll(name).includes(item.value) ? "active" : ""}`}
              href={facetHref(route, name, item.value)}
              data-facet-name={name}
              data-facet-value={item.value}
            >
              {name === "label" ? "#" : name === "assignee" ? "@" : ""}
              {item.value}
              {item.count === undefined ? "" : ` ${item.count}`}
            </a>
          ))
        ) : (
          <span class="facet-empty">None</span>
        )}
      </span>
      {lowest === name && data && (
        <Pagination route={route} total={data.page.total} />
      )}
    </div>
  );
  return (
    <div>
      <h1>{titles[kind]}</h1>
      <p class="lede">{ledes[kind]}</p>
      <ErrorMessage error={resource.error} />
      <div class="listing">
        <div class="listing-tools">
          <SearchInput
            value={filter}
            onInput={setFilter}
            placeholder={`Filter all ${kind}…`}
          />
          <span class="filter-count">
            {data
              ? `${data.page.total} issue${data.page.total === 1 ? "" : "s"}`
              : "Loading…"}
          </span>
          {kind === "issues" && (
            <ListingFilters route={route} />
          )}
          <div class="listing-actions">
            <label class="mobile-sort-control">
              Sort
              <select
                aria-label="Sort issues"
                value={
                  state.explicit
                    ? (state.direction === "desc" ? "-" : "") + state.key
                    : ""
                }
                onChange={(e) => {
                  const query = new URLSearchParams(route.query);
                  query.delete("page");
                  if (e.currentTarget.value)
                    query.set("sort", e.currentTarget.value);
                  else query.delete("sort");
                  location.hash = routeHref(route, query);
                }}
              >
                <option value="">Natural order</option>
                {[
                  ...issueColumns(kind).filter(([k]) => k !== "parent"),
                  ["created", "Created"],
                ].flatMap(([key, label]) => [
                  <option key={key} value={key}>
                    {label} ↑
                  </option>,
                  <option key={`-${key}`} value={`-${key}`}>
                    {label} ↓
                  </option>,
                ])}
              </select>
            </label>
            <span class="mobile-updated-display">
              Updated
              <UpdatedDisplayControl />
            </span>
            <IssueCreateButton
              workspace={
                route.query.getAll("workspace").length === 1
                  ? route.query.get("workspace")!
                  : undefined
              }
              onCreated={resource.reload}
            />
          </div>
        </div>
        {data ? (
          <>
            <div class="facets">
              {group(
                "workspace",
                "workspaces",
                data.workspaces.rows.map((w) => ({ value: w.key })),
              )}
              <DynamicFilterRow
                route={route}
                title="labels"
                name="label"
                selected={route.query.getAll("label")}
                choices={[]}
                loadChoices={async () => {
                  const filters = filtersFrom(route.query);
                  const page = await api.labels(
                    kind === "ready"
                      ? readyFacetFilters(filters)
                      : facetFilters(filters),
                  );
                  return rankCountedFilterSuggestions(page.rows).map(
                    (label) => ({
                      value: label.value,
                      label: `#${label.value}`,
                      detail: `${label.count} issue${label.count === 1 ? "" : "s"}`,
                    }),
                  );
                }}
                trailing={
                  lowest === "label" ? (
                    <Pagination route={route} total={data.page.total} />
                  ) : undefined
                }
              />
              {kind === "issues" && (
                <EpicFilterRow
                  route={route}
                  initialEpics={data.page.rows.filter(
                    (issue) => issue.type === "epic",
                  )}
                />
              )}
              {kind !== "ready" && (
                <DynamicFilterRow
                  route={route}
                  title="assignees"
                  name="assignee"
                  selected={route.query.getAll("assignee")}
                  choices={[]}
                  loadChoices={async () => {
                    const page = await api.assignees(
                      facetFilters(filtersFrom(route.query)),
                    );
                    return rankCountedFilterSuggestions(page.rows).map(
                      (assignee) => ({
                        value: assignee.value,
                        label: `@${assignee.value}`,
                        detail: `${assignee.count} issue${assignee.count === 1 ? "" : "s"}`,
                      }),
                    );
                  }}
                  trailing={
                    lowest === "assignee" ? (
                      <Pagination route={route} total={data.page.total} />
                    ) : undefined
                  }
                />
              )}
            </div>
            <div class="listing-host">
              {data.page.rows.length ? (
                <IssueTable
                  issues={data.page.rows}
                  kind={kind}
                  sortKey={state.key}
                  direction={state.direction}
                  onSort={sort}
                  onReload={resource.reload}
                />
              ) : (
                <p class="empty">
                  {kind === "issues" && hasEmptyStatusSelection(route.query)
                    ? "No statuses selected."
                    : kind === "issues" && hasEmptyIssueTypeSelection(route.query)
                      ? "No issue types selected."
                    : filter || epicSelectionFrom(route.query) !== null
                      ? "No issues match these filters."
                      : `No ${kind === "issues" ? "issues" : kind + " issues"}.`}
                </p>
              )}
            </div>
          </>
        ) : (
          !resource.error && <Loading />
        )}
      </div>
    </div>
  );
}
