import assert from "node:assert/strict";
import test from "node:test";

import { activityValue, activityValues, initialFor, relativeTime } from "../../static/presentation.js";

test("identity initials tolerate handles, whitespace and the system actor", () => {
  assert.equal(initialFor("mikael"), "M");
  assert.equal(initialFor(" @sara "), "S");
  assert.equal(initialFor(""), "?");
});

test("timestamps use concise relative language", () => {
  const now = Date.parse("2026-08-30T12:00:00.000Z");
  assert.equal(relativeTime("2026-08-30T11:59:55.000Z", now), "just now");
  assert.equal(relativeTime("2026-08-30T11:59:40.000Z", now), "20s ago");
  assert.equal(relativeTime("2026-08-30T09:42:00.000Z", now), "2h 18m ago");
  assert.equal(relativeTime("2026-08-29T10:00:00.000Z", now), "1d 2h ago");
  assert.equal(relativeTime("2026-08-30T14:17:00.000Z", now), "in 2h 17m");
  assert.equal(relativeTime("not-a-date", now), "not-a-date");
});

test("activity values keep primitives exact and summarize structural snapshots", () => {
  assert.equal(activityValue("in_progress"), "in_progress");
  assert.equal(activityValue(null), "(none)");
  assert.equal(activityValue(["frontend", "release"]), "frontend, release");
  assert.equal(activityValue([
    { type: "blocked-by", other: "awb-one", direction: "out" },
    { type: "related", other: "awb-two", direction: "in" },
  ]), "2 relations");
  assert.equal(activityValue({ name: "trace.txt", size: 42 }), "trace.txt");
});

test("one-item activity deltas name the changed value", () => {
  const relation = { type: "blocked-by", other: "awb-one", direction: "out" };
  assert.deepEqual(activityValues([], [relation]), ["(none)", "blocked-by awb-one"]);
  assert.deepEqual(activityValues(["frontend"], ["frontend", "release"]), ["(none)", "release"]);
  assert.deepEqual(activityValues("open", "in_progress"), ["open", "in_progress"]);
});
