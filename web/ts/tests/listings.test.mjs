import assert from "node:assert/strict";
import test from "node:test";

import {
  emptyFacetLabel,
  filterIssues,
  filterProjects,
  filterUsers,
  nextSortValue,
  sortIssues,
  sortProjects,
  sortState,
  withClosedIssues,
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
  const query = new URLSearchParams("project=awb&label=frontend&sort=-updated");
  assert.equal(
    withClosedIssues(query, true).toString(),
    "project=awb&label=frontend&sort=-updated&include-closed=true",
  );
  assert.equal(query.has("include-closed"), false, "the current route is not mutated");
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
    },
    {
      name: "dana", project_admin: false, user_admin: true,
      projects: [],
    },
  ];
  assert.deepEqual(filterUsers(rows, "alice awb").map((row) => row.name), ["alice"]);
  assert.deepEqual(filterUsers(rows, "user administrator").map((row) => row.name), ["dana"]);
  assert.equal(filterUsers(rows, "hidden-project").length, 0);
});

test("issue sorting supports UI-only columns and keeps blank assignees last", () => {
  const rows = [
    issue({ id: "awb-c", project: "zeta" }),
    issue({ id: "awb-b", project: "alpha", assignees: ["zoe", "mikael"] }),
    issue({ id: "awb-a", project: "alpha", assignees: ["anna"] }),
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

test("created sorting preserves supported API URL orderings", () => {
  const rows = [
    issue({ id: "awb-new", created_at: "2026-02-01T00:00:00.000Z" }),
    issue({ id: "awb-old", created_at: "2026-01-01T00:00:00.000Z" }),
  ];
  assert.deepEqual(
    sortIssues(rows, { key: "created", direction: "asc", explicit: true }).map((row) => row.id),
    ["awb-old", "awb-new"],
  );
  assert.deepEqual(
    sortIssues(rows, { key: "created", direction: "desc", explicit: true }).map((row) => row.id),
    ["awb-new", "awb-old"],
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
