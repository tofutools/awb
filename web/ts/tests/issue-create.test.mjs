import assert from "node:assert/strict";
import test from "node:test";

import { stagedLabel } from "../../static/issue-create.js";

test("new issue labels are trimmed and validated against the API vocabulary", () => {
  assert.deepEqual(stagedLabel(" frontend ", []), { label: "frontend" });
  assert.deepEqual(stagedLabel("release/1.0", []), { label: "release/1.0" });
  assert.equal(stagedLabel("", []).error, "Enter a label.");
  assert.match(stagedLabel("Frontend Bug", []).error, /lowercase letters/);
  assert.match(stagedLabel("a".repeat(65), []).error, /at most 64/);
});

test("a new issue cannot stage the same label twice", () => {
  assert.deepEqual(stagedLabel("frontend", ["frontend"]), {
    error: "That label is already staged.",
  });
});
