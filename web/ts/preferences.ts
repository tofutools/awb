export const accountMenuItems = [
  { href: "#/profile", label: "Profile" },
  { href: "#/settings", label: "Settings" },
] as const;

const paginationAutoHideKey = "awb.pagination-auto-hide";

interface PreferenceStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface StorageHost {
  readonly localStorage: PreferenceStorage;
}

/** Access to local storage may itself be forbidden by the browser. */
export function preferenceStorage(host: StorageHost): PreferenceStorage | null {
  try {
    return host.localStorage;
  } catch {
    return null;
  }
}

/** Pagination auto-hide is enabled until this browser explicitly disables it. */
export function readPaginationAutoHide(storage: PreferenceStorage | null): boolean {
  if (storage === null) return true;
  try {
    return storage.getItem(paginationAutoHideKey) !== "false";
  } catch {
    return true;
  }
}

/** A blocked store must not prevent the in-page preference from changing. */
export function rememberPaginationAutoHide(
  storage: PreferenceStorage | null,
  autoHide: boolean,
): void {
  if (storage === null) return;
  try {
    storage.setItem(paginationAutoHideKey, String(autoHide));
  } catch {
    // The current page still changes even when the preference cannot persist.
  }
}

/** Ten is the smallest useful pagination limit, so controls remain useful at
 * the threshold and are hidden only when fewer than ten entries exist. */
export function showPagination(total: number, autoHide: boolean): boolean {
  return !autoHide || total >= 10;
}
