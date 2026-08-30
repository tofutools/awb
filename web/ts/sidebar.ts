const issueSidebarKey = "awb.issue-sidebar-collapsed";

interface SidebarStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface StorageHost {
  readonly localStorage: SidebarStorage;
}

/** Access to the localStorage property itself can be forbidden by the browser. */
export function issueSidebarStorage(host: StorageHost): SidebarStorage | null {
  try {
    return host.localStorage;
  } catch {
    return null;
  }
}

/** The sidebar starts open unless this browser explicitly remembers it closed. */
export function issueSidebarCollapsed(storage: SidebarStorage | null): boolean {
  if (storage === null) return false;
  try {
    return storage.getItem(issueSidebarKey) === "true";
  } catch {
    return false;
  }
}

/** Storage can be unavailable in privacy modes; the control should still work then. */
export function rememberIssueSidebar(storage: SidebarStorage | null, collapsed: boolean): void {
  if (storage === null) return;
  try {
    storage.setItem(issueSidebarKey, String(collapsed));
  } catch {
    // The in-page state has still changed, even when it cannot be remembered.
  }
}
