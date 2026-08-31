import assert from "node:assert/strict";
import test from "node:test";

import {
  autocompleteDebounceMs,
  autocompleteKeyAction,
  nextActiveIndex,
  SuggestionSearch,
} from "../../static/autocomplete.js";

const wait = (duration) => new Promise((resolve) => setTimeout(resolve, duration));

test("suggestions wait for the fixed debounce and expose loading then results", async () => {
  const calls = [];
  const states = [];
  const search = new SuggestionSearch(
    async (query) => {
      calls.push(query);
      return [{ value: query, label: query }];
    },
    (state, rows) => states.push([state, rows]),
  );

  search.query("parser");
  assert.deepEqual(states, [["loading", []]]);
  await wait(autocompleteDebounceMs - 30);
  assert.deepEqual(calls, []);
  await wait(50);
  assert.deepEqual(calls, ["parser"]);
  assert.equal(states.at(-1)[0], "ready");
  search.close();
});

test("a superseded request is aborted and its stale result is ignored", async () => {
  const pending = [];
  const states = [];
  const search = new SuggestionSearch(
    (query, signal) => new Promise((resolve) => pending.push({ query, signal, resolve })),
    (state, rows) => states.push([state, rows]),
  );

  search.query("old");
  await wait(autocompleteDebounceMs + 20);
  search.query("new");
  assert.equal(pending[0].signal.aborted, true);
  pending[0].resolve([{ value: "old", label: "old" }]);
  await wait(0);
  assert.equal(states.some(([state, rows]) => state === "ready" && rows[0]?.value === "old"), false);

  await wait(autocompleteDebounceMs + 20);
  pending[1].resolve([{ value: "new", label: "new" }]);
  await wait(0);
  assert.deepEqual(states.at(-1), ["ready", [{ value: "new", label: "new" }]]);
  search.close();
});

test("empty and failed backend results have distinct visible states", async () => {
  const states = [];
  const empty = new SuggestionSearch(async () => [], (state) => states.push(state));
  empty.query("missing");
  await wait(autocompleteDebounceMs + 20);
  assert.equal(states.at(-1), "empty");

  const failed = new SuggestionSearch(async () => { throw new Error("offline"); },
    (state) => states.push(state));
  failed.query("broken");
  await wait(autocompleteDebounceMs + 20);
  assert.equal(states.at(-1), "error");
});

test("keyboard navigation wraps and Enter only selects an active suggestion", () => {
  assert.equal(nextActiveIndex(-1, 3, 1), 0);
  assert.equal(nextActiveIndex(2, 3, 1), 0);
  assert.equal(nextActiveIndex(0, 3, -1), 2);
  assert.equal(autocompleteKeyAction("ArrowDown", true, -1, 3), "next");
  assert.equal(autocompleteKeyAction("Enter", true, 1, 3), "select");
  assert.equal(autocompleteKeyAction("Escape", true, -1, 0), "dismiss");
  assert.equal(autocompleteKeyAction("Enter", false, -1, 0), "submit",
    "manual entry keeps the form's normal submission path");
});
