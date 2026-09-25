import assert from "node:assert/strict";
import test from "node:test";

import { labelSuggestionFilters, labelSuggestions } from "../../static/label-suggestions.js";

test("label suggestions come from the workspace's open and closed issues", () => {
  assert.deepEqual(labelSuggestionFilters("awb"), {
    workspace: ["awb"],
    "include-closed": true,
  });
});

test("label suggestions match case-insensitively and skip labels already carried", () => {
  const facets = [
    { value: "backend", count: 3 },
    { value: "frontend", count: 1 },
    { value: "parser", count: 2 },
  ];
  assert.deepEqual(labelSuggestions(facets, ["frontend"], "END"), [
    { value: "backend", label: "backend" },
  ]);
  assert.deepEqual(
    labelSuggestions(facets, [], "").map((s) => s.value),
    ["backend", "frontend", "parser"],
  );
});
