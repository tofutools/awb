const issueSidebarKey = "awb.issue-sidebar-collapsed";

interface SidebarStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** The sidebar starts open unless this browser explicitly remembers it closed. */
export function issueSidebarCollapsed(storage: SidebarStorage): boolean {
  try {
    return storage.getItem(issueSidebarKey) === "true";
  } catch {
    return false;
  }
}

/** Storage can be unavailable in privacy modes; the control should still work then. */
export function rememberIssueSidebar(storage: SidebarStorage, collapsed: boolean): void {
  try {
    storage.setItem(issueSidebarKey, String(collapsed));
  } catch {
    // The in-page state has still changed, even when it cannot be remembered.
  }
}
