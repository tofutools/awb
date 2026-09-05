import assert from "node:assert/strict";
import test from "node:test";

import { historyDiff, historyDiffPreview } from "../../static/history-diff.js";

const changedText = (parts) => parts.filter((part) => part.kind !== "same").map((part) => [part.kind, part.text]);

test("a preview centers an edit after a long unchanged prefix", () => {
  const prefix = "The beginning says the same thing. ".repeat(20);
  const parts = historyDiff(`${prefix}old ending`, `${prefix}new ending`);
  const preview = historyDiffPreview(parts, 24);
  assert.equal(preview[0].kind, "omitted");
  assert.match(preview.map((part) => part.text).join(""), /old.*new.*ending/s);
  assert.deepEqual(changedText(parts), [["remove", "old"], ["add", "new"]]);
});

test("multiline additions, removals and replacements preserve source whitespace", () => {
  const before = "# Plan\n\nKeep this.\nRemove this line.\nOld ending.\n";
  const after = "# Plan\n\nKeep this.\nAdd this line.\nNew ending.\n";
  const parts = historyDiff(before, after);
  assert.equal(parts.filter((part) => part.kind !== "add").map((part) => part.text).join(""), before);
  assert.equal(parts.filter((part) => part.kind !== "remove").map((part) => part.text).join(""), after);
  assert.ok(parts.some((part) => part.kind === "remove" && part.text.includes("Remove")));
  assert.ok(parts.some((part) => part.kind === "add" && part.text.includes("Add")));
  assert.ok(parts.some((part) => part.kind === "remove" && part.text.includes("Old")));
  assert.ok(parts.some((part) => part.kind === "add" && part.text.includes("New")));
});

test("multiple separated edits remain separate and omitted context is explicit", () => {
  const middle = " unchanged context ".repeat(20);
  const parts = historyDiff(`alpha old${middle}omega old`, `alpha new${middle}omega new`);
  assert.equal(parts.filter((part) => part.kind === "remove").length, 2);
  assert.equal(parts.filter((part) => part.kind === "add").length, 2);
  const preview = historyDiffPreview(parts, 12);
  assert.ok(preview.some((part) => part.kind === "omitted"));
  assert.equal(preview.filter((part) => part.kind === "remove").length, 2);
  assert.equal(preview.filter((part) => part.kind === "add").length, 2);
});

test("pure insertions and deletions retain their non-colour operation", () => {
  assert.deepEqual(changedText(historyDiff("alpha beta", "alpha bright beta")), [["add", "bright "]]);
  assert.deepEqual(changedText(historyDiff("alpha stale beta", "alpha beta")), [["remove", "stale "]]);
});
