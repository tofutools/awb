import assert from "node:assert/strict";
import test from "node:test";

import { legalBoardTargets, splitBoardFilter } from "../../static/boards.js";

test("board moves map onto safe workflow transitions", () => {
  assert.deepEqual(legalBoardTargets(), ["open", "in_progress", "closed"]);
});

test("saved-view text filters are trimmed and deduplicated", () => {
  assert.deepEqual(splitBoardFilter(" release, frontend  release\napi "), ["release", "frontend", "api"]);
  assert.deepEqual(splitBoardFilter(""), []);
});
