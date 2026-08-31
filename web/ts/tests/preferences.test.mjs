import assert from "node:assert/strict";
import test from "node:test";

import {
  accountMenuItems,
  preferenceStorage,
  readPaginationAutoHide,
  rememberPaginationAutoHide,
  showPagination,
} from "../../static/preferences.js";

test("pagination auto-hide is enabled by default and persists explicit choices", () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };

  assert.equal(readPaginationAutoHide(storage), true);
  rememberPaginationAutoHide(storage, false);
  assert.equal(readPaginationAutoHide(storage), false);
  rememberPaginationAutoHide(storage, true);
  assert.equal(readPaginationAutoHide(storage), true);
});

test("blocked browser storage preserves the safe default and remains usable", () => {
  const host = { get localStorage() { throw new Error("unavailable"); } };
  const storage = preferenceStorage(host);

  assert.equal(storage, null);
  assert.equal(readPaginationAutoHide(storage), true);
  assert.doesNotThrow(() => rememberPaginationAutoHide(storage, false));
});

test("auto-hide stops immediately at the ten-entry pagination threshold", () => {
  assert.equal(showPagination(0, true), false);
  assert.equal(showPagination(9, true), false);
  assert.equal(showPagination(10, true), true);
  assert.equal(showPagination(9, false), true);
});

test("settings follows profile in the account menu and routes to its own page", () => {
  assert.deepEqual(accountMenuItems, [
    { href: "#/profile", label: "Profile" },
    { href: "#/settings", label: "Settings" },
  ]);
});
