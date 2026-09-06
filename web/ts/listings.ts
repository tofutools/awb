// Pure listing behavior shared by Preact components and their Node tests.


export type SortDirection = "asc" | "desc";

export interface SortState {
  key: string;
  direction: SortDirection;
  explicit: boolean;
}

// openapi.yaml caps every listing's shared filter at the same length. The UI
// enforces it before a request so an invalid pasted URL cannot strand the tab
// on an error page without its clear control.
export const listingFilterMaxLength = 500;

export const noEpicSelection = "none";
const issueIDPattern = /^[a-z][a-z0-9-]*-[0-9a-f]{6}$/;

export const initialFilterSuggestionLimit = 8;

/** Initial filter suggestions stay compact; an explicit search may return
 * every match so the user can find less common values. */
export function initialFilterSuggestions<T>(
  query: string,
  matches: readonly T[],
): T[] {
  return query === ""
    ? matches.slice(0, initialFilterSuggestionLimit)
    : [...matches];
}

/** Frequently used facet values make the most useful suggestions before search. */
export function rankCountedFilterSuggestions<
  T extends { value: string; count: number },
>(values: readonly T[]): T[] {
  return [...values].sort(
    (left, right) =>
      right.count - left.count || left.value.localeCompare(right.value),
  );
}

/** Active and recently updated epics are the most useful suggestions before
 * search. Stable IDs make otherwise equal entries deterministic. */
export function rankEpicFilterSuggestions<
  T extends { id: string; status: string; updated_at: string },
>(epics: readonly T[]): T[] {
  return [...epics].sort((left, right) => {
    const state =
      Number(left.status === "closed") - Number(right.status === "closed");
    return (
      state ||
      right.updated_at.localeCompare(left.updated_at) ||
      left.id.localeCompare(right.id)
    );
  });
}

/** epicSelectionFrom accepts exactly the values the listing API accepts. An
 * invalid hand-written URL is treated as an unfiltered view rather than
 * sending a request the API must reject. */
export function epicSelectionFrom(query: URLSearchParams): string | null {
  const value = query.get("epic");
  return value === noEpicSelection || (value !== null && issueIDPattern.test(value)) ? value : null;
}

export interface EpicParentFilter {
  parent?: string;
  "parent-type"?: "epic";
  recursive?: true;
  "include-parent"?: true;
}

/** epicParentFilter translates the epic-specific route state into the
 * general parent query exposed by the API. */
export function epicParentFilter(query: URLSearchParams): EpicParentFilter {
  const epic = epicSelectionFrom(query);
  if (epic === null) return {};
  if (epic === noEpicSelection) {
    return { parent: "none", "parent-type": "epic", recursive: true };
  }
  return { parent: epic, recursive: true, "include-parent": true };
}

/** withEpicSelection changes the single epic selection without disturbing
 * the other filters or sort, and returns to the first backend page. */
export function withEpicSelection(query: URLSearchParams, epic: string | null): URLSearchParams {
  const next = new URLSearchParams(query);
  next.delete("page");
  if (epic === null) next.delete("epic");
  else next.set("epic", epic);
  return next;
}

// Parent titles share a deliberately narrow listing column. Keep their text
// bounded even on wide screens; the anchor's tooltip carries the full title.
export const listingParentTitleMaxLength = 32;

export function listingParentTitle(title: string): string {
  const characters = Array.from(title);
  if (characters.length <= listingParentTitleMaxLength) return title;
  return `${characters.slice(0, listingParentTitleMaxLength - 1).join("")}…`;
}

export type ListingRelationshipRole = "parent" | "sibling" | null;

/** listingRelationshipRole classifies one visible row while a child's parent
 * link is active. The child itself stays unmarked: the active link already
 * identifies it, while the overlays point out its family elsewhere on the
 * current page. */
export function listingRelationshipRole(
  issueID: string,
  issueParentID: string | undefined,
  childID: string,
  parentID: string,
): ListingRelationshipRole {
  if (issueID === parentID) return "parent";
  if (issueID !== childID && issueParentID === parentID) return "sibling";
  return null;
}

export interface ListingFamilyActivation {
  hovered: boolean;
  focused: boolean;
}

/** activeListingFamily selects the relationship a listing should show. A
 * pointer hover temporarily takes precedence over keyboard focus; when it
 * leaves, the still-focused link becomes active again. */
export function activeListingFamily(states: readonly ListingFamilyActivation[]): number | null {
  const hovered = states.findIndex((state) => state.hovered);
  if (hovered !== -1) return hovered;
  const focused = states.findIndex((state) => state.focused);
  return focused === -1 ? null : focused;
}

export const defaultPageSize = 10;
export const pageSizes = [10, 25, 50, 100] as const;
const pageSizeKey = "awb.page-size";

interface PageSizeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface StorageHost {
  readonly localStorage: PageSizeStorage;
}

/** Access to localStorage itself can be forbidden by the browser. */
export function pageSizeStorage(host: StorageHost): PageSizeStorage | null {
  try {
    return host.localStorage;
  } catch {
    return null;
  }
}

/** rememberedPageSize returns the browser preference, or the UI default. */
export function rememberedPageSize(storage: PageSizeStorage | null): number {
  if (storage === null) return defaultPageSize;
  try {
    const value = Number(storage.getItem(pageSizeKey));
    return pageSizes.includes(value as typeof pageSizes[number]) ? value : defaultPageSize;
  } catch {
    return defaultPageSize;
  }
}

/** Storage can be unavailable in privacy modes; paging still works then. */
export function rememberPageSize(storage: PageSizeStorage | null, size: number): void {
  if (storage === null) return;
  try {
    storage.setItem(pageSizeKey, String(size));
  } catch {
    // The current route still changes even when the preference cannot persist.
  }
}

/** pageSizeFrom reads one of the deliberately small set of UI page sizes. */
export function pageSizeFrom(query: URLSearchParams, fallback = defaultPageSize): number {
  const value = Number(query.get("size"));
  if (pageSizes.includes(value as typeof pageSizes[number])) return value;
  return pageSizes.includes(fallback as typeof pageSizes[number]) ? fallback : defaultPageSize;
}

/** pageNumber reads the UI's one-based page parameter. Invalid values fall
 * back to the first page and never reach the API as an invalid offset. */
export function pageNumber(query: URLSearchParams): number {
  const value = query.get("page");
  if (value === null || !/^\d+$/.test(value)) return 1;
  const page = Number(value);
  return Number.isSafeInteger(page) && page > 0 ? page : 1;
}

/** withPage changes only pagination state. Page one is the canonical URL and
 * therefore has no explicit page parameter. */
export function withPage(query: URLSearchParams, page: number): URLSearchParams {
  const next = new URLSearchParams(query);
  if (page <= 1) next.delete("page");
  else next.set("page", String(page));
  return next;
}

/** withPageSize changes the backend page size and returns to the first page,
 * whose offset is the only one that remains meaningful for every size. */
export function withPageSize(query: URLSearchParams, size: number): URLSearchParams {
  const next = new URLSearchParams(query);
  next.delete("page");
  if (size === defaultPageSize) next.delete("size");
  else next.set("size", String(size));
  return next;
}

export interface PageWindow {
  page: number;
  pages: number;
  first: number;
  last: number;
}

export type FacetGroup = "workspace" | "label" | "assignee";

/** lowestFacetGroup names the final applicable detail row above an issue
 * listing. Pagination shares that row so it stays visually attached to the
 * results even when some listing variants omit facets. */
export function lowestFacetGroup(
  labels: readonly unknown[] | null,
  assignees: readonly unknown[] | null,
): FacetGroup {
  if (assignees !== null) return "assignee";
  if (labels !== null) return "label";
  return "workspace";
}

/** pageWindow describes the range represented by a backend page. */
export function pageWindow(total: number, requested: number, size = defaultPageSize): PageWindow {
  const pages = Math.max(1, Math.ceil(total / size));
  const page = Math.min(Math.max(1, requested), pages);
  const first = total === 0 ? 0 : (page - 1) * size + 1;
  const last = total === 0 ? 0 : Math.min(page * size, total);
  return { page, pages, first, last };
}

/** Empty applicable facet groups advertise themselves; null means omitted. */
export function emptyFacetLabel(values: readonly unknown[] | null): string | null {
  return values !== null && values.length === 0 ? "none" : null;
}

/** sortState reads a signed sort key and falls back to the view's natural order. */
export function sortState(
  value: string | null,
  allowed: readonly string[],
  defaultKey: string,
  defaultDirection: SortDirection = "asc",
): SortState {
  if (value !== null) {
    const direction: SortDirection = value.startsWith("-") ? "desc" : "asc";
    const key = value.startsWith("-") ? value.slice(1) : value;
    if (allowed.includes(key)) return { key, direction, explicit: true };
  }
  return { key: defaultKey, direction: defaultDirection, explicit: false };
}

/**
 * nextSortValue implements the header cycle: ascending, descending, natural.
 * A default ascending column therefore goes straight to descending when first
 * clicked; removing the explicit descending value returns to the same natural
 * ascending order.
 */
export function nextSortValue(
  value: string | null,
  column: string,
  allowed: readonly string[],
  defaultKey: string,
  defaultDirection: SortDirection = "asc",
): string | null {
  const current = sortState(value, allowed, defaultKey, defaultDirection);
  if (current.key !== column) return column;
  if (current.direction === "asc") return `-${column}`;
  return null;
}
