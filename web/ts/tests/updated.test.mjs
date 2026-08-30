import assert from "node:assert/strict";
import test from "node:test";

import {
  formatUpdated,
  readUpdatedDisplay,
  rememberUpdatedDisplay,
  updatedStorage,
} from "../../static/updated.js";

test("updated timestamps support relative, local date and local date-time displays", () => {
  const value = new Date(2026, 7, 30, 16, 42);
  const timestamp = value.toISOString();
  const now = value.getTime() + (2 * 60 + 18) * 60 * 1000;

  assert.equal(formatUpdated(timestamp, "relative", now), "2h 18m ago");
  assert.equal(formatUpdated(timestamp, "date", now), "2026-08-30");
  assert.equal(formatUpdated(timestamp, "datetime", now), "2026-08-30 16:42");
  assert.equal(formatUpdated("not-a-date", "datetime", now), "not-a-date");
});

test("the relative display is the default and valid choices are remembered", () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };

  assert.equal(readUpdatedDisplay(storage), "relative");
  rememberUpdatedDisplay(storage, "datetime");
  assert.equal(readUpdatedDisplay(storage), "datetime");
  values.set("awb.updated-display", "unknown");
  assert.equal(readUpdatedDisplay(storage), "relative");
});

test("blocked local storage leaves the display usable", () => {
  const host = { get localStorage() { throw new Error("unavailable"); } };
  const storage = updatedStorage(host);

  assert.equal(storage, null);
  assert.equal(readUpdatedDisplay(storage), "relative");
  assert.doesNotThrow(() => rememberUpdatedDisplay(storage, "date"));
});
