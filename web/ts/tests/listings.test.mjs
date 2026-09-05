import assert from "node:assert/strict";
import test from "node:test";

import {
  activeListingFamily,
  emptyFacetLabel,
  listingFilterMaxLength,
  listingParentTitle,
  listingParentTitleMaxLength,
  listingRelationshipRole,
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

const wait = (duration) => new Promise((resolve) => setTimeout(resolve, duration));

test("listing filter length mirrors the OpenAPI contract", () => {
  assert.equal(listingFilterMaxLength, 500);
});

test("parent titles are bounded for their narrow listing column", () => {
  assert.equal(listingParentTitle("Short epic"), "Short epic");
  assert.equal(listingParentTitle("A title that is deliberately longer than the parent column"),
    "A title that is deliberately lo…");
  assert.equal(Array.from(listingParentTitle("x".repeat(100))).length, listingParentTitleMaxLength);
});

test("listing family markers identify the visible parent and siblings", () => {
  assert.equal(listingRelationshipRole("awb-parent", undefined, "awb-child", "awb-parent"), "parent");
  assert.equal(listingRelationshipRole("awb-sibling", "awb-parent", "awb-child", "awb-parent"), "sibling");
  assert.equal(listingRelationshipRole("awb-child", "awb-parent", "awb-child", "awb-parent"), null);
  assert.equal(listingRelationshipRole("awb-unrelated", "awb-other", "awb-child", "awb-parent"), null);
});

test("listing family hover temporarily takes precedence over keyboard focus", () => {
  assert.equal(activeListingFamily([
    { hovered: false, focused: true },
    { hovered: true, focused: false },
  ]), 1);
  assert.equal(activeListingFamily([
    { hovered: false, focused: true },
    { hovered: false, focused: false },
  ]), 0, "the focused family returns when the pointer leaves");
  assert.equal(activeListingFamily([
    { hovered: false, focused: false },
    { hovered: false, focused: false },
  ]), null);
});

test("empty applicable facet groups remain visible", () => {
  assert.equal(emptyFacetLabel([]), "none");
  assert.equal(emptyFacetLabel([{ value: "frontend", count: 1 }]), null);
  assert.equal(emptyFacetLabel(null), null, "inapplicable groups stay omitted");
});

test("pagination follows the lowest applicable facet row", () => {
  assert.equal(lowestFacetGroup([], []), "assignee");
  assert.equal(lowestFacetGroup([], null), "label");
  assert.equal(lowestFacetGroup(null, null), "workspace");
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
  const query = new URLSearchParams("workspace=awb&label=frontend&sort=-updated&page=3");
  assert.equal(
    withClosedIssues(query, true).toString(),
    "workspace=awb&label=frontend&sort=-updated&include-closed=true",
  );
  assert.equal(query.has("include-closed"), false, "the current route is not mutated");
});

test("backend pagination uses canonical one-based route state", () => {
  assert.equal(pageNumber(new URLSearchParams()), 1);
  assert.equal(pageNumber(new URLSearchParams("page=3")), 3);
  assert.equal(pageNumber(new URLSearchParams("page=-1")), 1);
  assert.equal(pageNumber(new URLSearchParams("page=not-a-number")), 1);
  assert.equal(withPage(new URLSearchParams("workspace=awb&page=3"), 1).toString(), "workspace=awb");
  assert.equal(withPage(new URLSearchParams("workspace=awb"), 4).toString(), "workspace=awb&page=4");
});

test("page size is a fixed UI choice and changing it resets the page", () => {
  assert.equal(pageSizeFrom(new URLSearchParams()), 10);
  assert.equal(pageSizeFrom(new URLSearchParams(), 25), 25);
  assert.equal(pageSizeFrom(new URLSearchParams("size=25")), 25);
  assert.equal(pageSizeFrom(new URLSearchParams("size=37")), 10);
  assert.equal(
    withPageSize(new URLSearchParams("workspace=awb&page=4"), 100).toString(),
    "workspace=awb&size=100",
  );
  assert.equal(
    withPageSize(new URLSearchParams("workspace=awb&page=4&size=25"), 10).toString(),
    "workspace=awb",
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
  const query = new URLSearchParams("workspace=awb&include-closed=true&filter=docs");
  assert.equal(withClosedIssues(query, false).toString(), "workspace=awb&filter=docs");
});

test("sort headers cycle ascending, descending, then natural order", () => {
  const allowed = ["key", "active"];
  assert.equal(nextSortValue(null, "active", allowed, "key"), "active");
  assert.equal(nextSortValue("active", "active", allowed, "key"), "-active");
  assert.equal(nextSortValue("-active", "active", allowed, "key"), null);
  assert.equal(nextSortValue(null, "key", allowed, "key"), "-key");
});

