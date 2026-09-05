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

/** withClosedIssues returns a route query widened to all statuses or narrowed
 * back to the default non-closed set, without disturbing the listing's other
 * filters and presentation choices. */
export function withClosedIssues(query: URLSearchParams, include: boolean): URLSearchParams {
  const next = new URLSearchParams(query);
  next.delete("page");
  if (include) next.set("include-closed", "true");
  else next.delete("include-closed");
  return next;
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
