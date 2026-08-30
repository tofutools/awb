import assert from "node:assert/strict";
import test from "node:test";

import { issueSidebarCollapsed, rememberIssueSidebar } from "../../static/sidebar.js";

test("the issue sidebar is open until the browser remembers it closed", () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };

  assert.equal(issueSidebarCollapsed(storage), false);
  rememberIssueSidebar(storage, true);
  assert.equal(issueSidebarCollapsed(storage), true);
  rememberIssueSidebar(storage, false);
  assert.equal(issueSidebarCollapsed(storage), false);
});

test("unavailable browser storage leaves the sidebar usable and open by default", () => {
  const storage = {
    getItem: () => { throw new Error("unavailable"); },
    setItem: () => { throw new Error("unavailable"); },
  };

  assert.equal(issueSidebarCollapsed(storage), false);
  assert.doesNotThrow(() => rememberIssueSidebar(storage, true));
});
