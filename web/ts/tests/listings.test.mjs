import assert from "node:assert/strict";
import test from "node:test";

import {
  emptyFacetLabel,
  filterIssues,
  filterProjects,
  filterUsers,
  nextSortValue,
  pageNumber,
  pageSizeFrom,
  pageWindow,
  sortState,
  withClosedIssues,
  withPage,
  withPageSize,
} from "../../static/listings.js";

test("empty applicable facet groups remain visible", () => {
  assert.equal(emptyFacetLabel([]), "none");
  assert.equal(emptyFacetLabel([{ value: "frontend", count: 1 }]), null);
  assert.equal(emptyFacetLabel(null), null, "inapplicable groups stay omitted");
});

function issue(overrides = {}) {
  return {
    id: "awb-a00001",
    project: "awb",
    title: "Sortable listings",
    description: "",
    type: "feature",
    status: "open",
    priority: 2,
    labels: ["frontend"],
    assignees: [],
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-02T00:00:00.000Z",
    blocked: false,
    blockers: [],
    relations: [],
    links: [],
    attachments: [],
    ...overrides,
  };
}

function project(overrides = {}) {
  return {
    key: "awb",
    name: "Agent Work Board",
    description: "Agent-first issue tracking",
    active_issues: 3,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-02T00:00:00.000Z",
    ...overrides,
  };
}

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
  assert.equal(pageSizeFrom(new URLSearchParams()), 50);
  assert.equal(pageSizeFrom(new URLSearchParams("size=25")), 25);
  assert.equal(pageSizeFrom(new URLSearchParams("size=37")), 50);
  assert.equal(
    withPageSize(new URLSearchParams("project=awb&page=4"), 100).toString(),
    "project=awb&size=100",
  );
  assert.equal(
    withPageSize(new URLSearchParams("project=awb&page=4&size=25"), 50).toString(),
    "project=awb",
  );
});

test("pagination ranges are clamped to the unpaged backend total", () => {
  assert.deepEqual(pageWindow(214, 2), { page: 2, pages: 5, first: 51, last: 100 });
  assert.deepEqual(pageWindow(214, 2, 25), { page: 2, pages: 9, first: 26, last: 50 });
  assert.deepEqual(pageWindow(214, 99), { page: 5, pages: 5, first: 201, last: 214 });
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

test("issue filtering matches every word across visible listing values", () => {
  const rows = [
    issue({ id: "awb-one", title: "Sortable listings", assignees: ["mikael"] }),
    issue({ id: "cli-two", project: "cli", title: "Remote mode", labels: ["docs"] }),
  ];
  assert.deepEqual(filterIssues(rows, "sort mikael").map((row) => row.id), ["awb-one"]);
  assert.deepEqual(filterIssues(rows, "CLI docs").map((row) => row.id), ["cli-two"]);
  assert.equal(filterIssues(rows, "not-present").length, 0);
});

test("project filtering includes key, name and description", () => {
  const rows = [
    project(),
    project({ key: "cli", name: "CLI tools", description: "Remote clients" }),
  ];
  assert.deepEqual(filterProjects(rows, "agent issue").map((row) => row.key), ["awb"]);
  assert.deepEqual(filterProjects(rows, "remote").map((row) => row.key), ["cli"]);
});
test("user filtering includes names, roles and visible projects", () => {
  const rows = [
    {
      name: "alice", project_admin: false, user_admin: false,
      projects: [{ project: "awb", user: "alice", access: "regular" }],
      activity_projects: ["archive"],
    },
    {
      name: "dana", project_admin: false, user_admin: true,
      projects: [],
      activity_projects: [],
    },
  ];
  assert.deepEqual(filterUsers(rows, "alice awb").map((row) => row.name), ["alice"]);
  assert.deepEqual(filterUsers(rows, "archive").map((row) => row.name), ["alice"]);
  assert.deepEqual(filterUsers(rows, "user administrator").map((row) => row.name), ["dana"]);
  assert.equal(filterUsers(rows, "hidden-project").length, 0);
});
