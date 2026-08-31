// Pure listing behavior shared by the DOM renderer and its Node tests.

import { autocompleteDebounceMs } from "./autocomplete.js";

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

/** Runs one debounced listing request at a time. Aborting saves backend work;
 * the generation guard also rejects a stale transport completion. */
export class BackendListingFilter<T> {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private request: AbortController | undefined;
  private generation = 0;

  constructor(
    private readonly load: (query: string, signal: AbortSignal) => Promise<T>,
    private readonly update: (result: T) => void,
    private readonly failed: (error: unknown) => void,
  ) {}

  query(query: string, immediate = false): void {
    this.generation++;
    const generation = this.generation;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.request?.abort();
    const run = (): void => {
      const request = new AbortController();
      this.request = request;
      void this.load(query, request.signal).then((result) => {
        if (generation !== this.generation || request.signal.aborted) return;
        this.update(result);
      }).catch((error: unknown) => {
        if (generation !== this.generation || request.signal.aborted) return;
        this.failed(error);
      });
    };
    if (immediate) run();
    else this.timer = setTimeout(run, autocompleteDebounceMs);
  }

  close(): void {
    this.generation++;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.request?.abort();
  }
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

export type FacetGroup = "project" | "label" | "assignee";

/** lowestFacetGroup names the final applicable detail row above an issue
 * listing. Pagination shares that row so it stays visually attached to the
 * results even when some listing variants omit facets. */
export function lowestFacetGroup(
  labels: readonly unknown[] | null,
  assignees: readonly unknown[] | null,
): FacetGroup {
  if (assignees !== null) return "assignee";
  if (labels !== null) return "label";
  return "project";
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
