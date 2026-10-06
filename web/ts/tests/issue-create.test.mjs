import assert from "node:assert/strict";
import test from "node:test";

import { inlineChildIssueCreate, issueMetadata, stagedLabel } from "../../static/issue-create.js";

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

test("rapid child entry creates only a title in the parent's workspace and relation", () => {
  assert.deepEqual(inlineChildIssueCreate("awb", "awb-parent", "  First child  "), {
    workspace: "awb",
    title: "First child",
    relations: [{ type: "has-parent", other: "awb-parent" }],
  });
  assert.equal(inlineChildIssueCreate("awb", "awb-parent", " \n\t "), undefined);
});


test("creation metadata accepts objects and preserves nested JSON values", () => {
  assert.deepEqual(issueMetadata("  "), {});
  assert.deepEqual(issueMetadata('{"nested":{"items":[null,true,2,"text"]}}'), {
    nested: { items: [null, true, 2, "text"] },
  });
  assert.deepEqual(issueMetadata('{"__proto__":{"safe":true}}'),
    JSON.parse('{"__proto__":{"safe":true}}'));
});

test("creation metadata rejects malformed JSON and non-object values", () => {
  assert.throws(() => issueMetadata('{broken'), /valid JSON/);
  for (const value of ['null', '[]', 'true', '42', '"text"']) {
    assert.throws(() => issueMetadata(value), /JSON object/);
  }
});
