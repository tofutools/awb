import assert from "node:assert/strict";
import test from "node:test";

import { initialFor, relativeTime } from "../../static/presentation.js";

test("identity initials tolerate handles, whitespace and the system actor", () => {
  assert.equal(initialFor("mikael"), "M");
  assert.equal(initialFor(" @sara "), "S");
  assert.equal(initialFor(""), "?");
});

test("timestamps use concise relative language", () => {
  const now = Date.parse("2026-08-30T12:00:00.000Z");
  assert.equal(relativeTime("2026-08-30T11:59:40.000Z", now), "just now");
  assert.equal(relativeTime("2026-08-30T10:00:00.000Z", now), "2 hours ago");
  assert.equal(relativeTime("2026-08-29T12:00:00.000Z", now), "yesterday");
  assert.equal(relativeTime("not-a-date", now), "not-a-date");
});
