import assert from "node:assert/strict";
import test from "node:test";

import {
  filterIssues,
  filterProjects,
  nextSortValue,
  sortIssues,
  sortProjects,
  sortState,
} from "../../static/listings.js";

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
    assignee: "",
    close_reason: "",
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

test("sort headers cycle ascending, descending, then natural order", () => {
  const allowed = ["key", "active"];
  assert.equal(nextSortValue(null, "active", allowed, "key"), "active");
  assert.equal(nextSortValue("active", "active", allowed, "key"), "-active");
  assert.equal(nextSortValue("-active", "active", allowed, "key"), null);
  assert.equal(nextSortValue(null, "key", allowed, "key"), "-key");
});

test("issue filtering matches every word across visible listing values", () => {
  const rows = [
    issue({ id: "awb-one", title: "Sortable listings", assignee: "mikael" }),
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

test("issue sorting supports UI-only columns and keeps blank assignees last", () => {
  const rows = [
    issue({ id: "awb-c", project: "zeta", assignee: "" }),
    issue({ id: "awb-b", project: "alpha", assignee: "zoe" }),
    issue({ id: "awb-a", project: "alpha", assignee: "anna" }),
  ];
  assert.deepEqual(
    sortIssues(rows, { key: "project", direction: "asc", explicit: true }).map((row) => row.id),
    ["awb-a", "awb-b", "awb-c"],
  );
  assert.deepEqual(
    sortIssues(rows, { key: "assignee", direction: "desc", explicit: true }).map((row) => row.id),
    ["awb-b", "awb-a", "awb-c"],
  );
});

test("priority sorting retains the API's oldest-first tie break", () => {
  const rows = [
    issue({ id: "awb-new", priority: 1, created_at: "2026-02-01T00:00:00.000Z" }),
    issue({ id: "awb-old", priority: 1, created_at: "2026-01-01T00:00:00.000Z" }),
    issue({ id: "awb-p2", priority: 2 }),
  ];
  assert.deepEqual(
    sortIssues(rows, { key: "priority", direction: "asc", explicit: false }).map((row) => row.id),
    ["awb-old", "awb-new", "awb-p2"],
  );
});

test("project sorting handles numeric open counts", () => {
  const rows = [
    project({ key: "few", active_issues: 2 }),
    project({ key: "many", active_issues: 12 }),
  ];
  assert.deepEqual(
    sortProjects(rows, { key: "active", direction: "desc", explicit: true }).map((row) => row.key),
    ["many", "few"],
  );
});
