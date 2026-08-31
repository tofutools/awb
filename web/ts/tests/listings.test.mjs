import assert from "node:assert/strict";
import test from "node:test";

import {
  BackendListingFilter,
  emptyFacetLabel,
  listingFilterMaxLength,
  lowestFacetGroup,
  nextSortValue,
  pageNumber,
  pageSizeFrom,
  pageSizeStorage,
  pageWindow,
  rememberedPageSize,
  rememberPageSize,
  sortState,
  withClosedIssues,
  withPage,
  withPageSize,
} from "../../static/listings.js";
import { autocompleteDebounceMs } from "../../static/autocomplete.js";

const wait = (duration) => new Promise((resolve) => setTimeout(resolve, duration));

test("listing filter length mirrors the OpenAPI contract", () => {
  assert.equal(listingFilterMaxLength, 500);
});

test("empty applicable facet groups remain visible", () => {
  assert.equal(emptyFacetLabel([]), "none");
  assert.equal(emptyFacetLabel([{ value: "frontend", count: 1 }]), null);
  assert.equal(emptyFacetLabel(null), null, "inapplicable groups stay omitted");
});

test("pagination follows the lowest applicable facet row", () => {
  assert.equal(lowestFacetGroup([], []), "assignee");
  assert.equal(lowestFacetGroup([], null), "label");
  assert.equal(lowestFacetGroup(null, null), "project");
});

test("sort state accepts known signed keys and otherwise uses the natural order", () => {
  const allowed = ["key", "active"];
  assert.deepEqual(sortState("-active", allowed, "key"), {
    key: "active", direction: "desc", explicit: true,
  });
  assert.deepEqual(sortState("unknown", allowed, "key"), {
    key: "key", direction: "asc", explicit: false,
  });
});

test("showing closed issues preserves the rest of the listing route", () => {
  const query = new URLSearchParams("project=awb&label=frontend&sort=-updated&page=3");
  assert.equal(
    withClosedIssues(query, true).toString(),
    "project=awb&label=frontend&sort=-updated&include-closed=true",
  );
  assert.equal(query.has("include-closed"), false, "the current route is not mutated");
});

test("backend pagination uses canonical one-based route state", () => {
  assert.equal(pageNumber(new URLSearchParams()), 1);
  assert.equal(pageNumber(new URLSearchParams("page=3")), 3);
  assert.equal(pageNumber(new URLSearchParams("page=-1")), 1);
  assert.equal(pageNumber(new URLSearchParams("page=not-a-number")), 1);
  assert.equal(withPage(new URLSearchParams("project=awb&page=3"), 1).toString(), "project=awb");
  assert.equal(withPage(new URLSearchParams("project=awb"), 4).toString(), "project=awb&page=4");
});

test("page size is a fixed UI choice and changing it resets the page", () => {
  assert.equal(pageSizeFrom(new URLSearchParams()), 10);
  assert.equal(pageSizeFrom(new URLSearchParams(), 25), 25);
  assert.equal(pageSizeFrom(new URLSearchParams("size=25")), 25);
  assert.equal(pageSizeFrom(new URLSearchParams("size=37")), 10);
  assert.equal(
    withPageSize(new URLSearchParams("project=awb&page=4"), 100).toString(),
    "project=awb&size=100",
  );
  assert.equal(
    withPageSize(new URLSearchParams("project=awb&page=4&size=25"), 10).toString(),
    "project=awb",
  );
});

test("page size is remembered when browser storage is available", () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };

  assert.equal(rememberedPageSize(storage), 10);
  rememberPageSize(storage, 50);
  assert.equal(rememberedPageSize(storage), 50);
  values.set("awb.page-size", "37");
  assert.equal(rememberedPageSize(storage), 10);

  const blocked = pageSizeStorage({ get localStorage() { throw new Error("unavailable"); } });
  assert.equal(blocked, null);
  assert.doesNotThrow(() => rememberPageSize(blocked, 25));
});

test("pagination ranges are clamped to the unpaged backend total", () => {
  assert.deepEqual(pageWindow(214, 2), { page: 2, pages: 22, first: 11, last: 20 });
  assert.deepEqual(pageWindow(214, 2, 25), { page: 2, pages: 9, first: 26, last: 50 });
  assert.deepEqual(pageWindow(214, 99), { page: 22, pages: 22, first: 211, last: 214 });
  assert.deepEqual(pageWindow(0, 1), { page: 1, pages: 1, first: 0, last: 0 });
});

test("hiding closed issues restores the default status set", () => {
  const query = new URLSearchParams("project=awb&include-closed=true&filter=docs");
  assert.equal(withClosedIssues(query, false).toString(), "project=awb&filter=docs");
});

test("sort headers cycle ascending, descending, then natural order", () => {
  const allowed = ["key", "active"];
  assert.equal(nextSortValue(null, "active", allowed, "key"), "active");
  assert.equal(nextSortValue("active", "active", allowed, "key"), "-active");
  assert.equal(nextSortValue("-active", "active", allowed, "key"), null);
  assert.equal(nextSortValue(null, "key", allowed, "key"), "-key");
});

test("listing filters debounce requests and reject stale completions", async () => {
  const pending = [];
  const updates = [];
  const search = new BackendListingFilter(
    (query, signal) => new Promise((resolve) => pending.push({ query, signal, resolve })),
    (result) => updates.push(result),
    assert.fail,
  );

  search.query("old");
  await wait(autocompleteDebounceMs + 20);
  search.query("new");
  assert.equal(pending[0].signal.aborted, true);
  pending[0].resolve("stale");
  await wait(0);
  assert.deepEqual(updates, []);

  await wait(autocompleteDebounceMs + 20);
  pending[1].resolve("current");
  await wait(0);
  assert.deepEqual(updates, ["current"]);
  search.close();
});

test("clearing a listing filter can request the unfiltered page immediately", async () => {
  const calls = [];
  const search = new BackendListingFilter(
    async (query) => { calls.push(query); return query; },
    () => {},
    assert.fail,
  );
  search.query("", true);
  await wait(0);
  assert.deepEqual(calls, [""]);
  search.close();
});
