// The API client's query building, which has to match the CLI's flags exactly:
// a repeatable filter is repeated rather than comma-separated, and the names
// are the kebab-case ones the server accepts.

import assert from "node:assert/strict";
import test from "node:test";

import { api, blockedFilters, facetFilters, readyFacetFilters, readyFilters, toQuery } from "../../static/api.js";

test("empty filters produce no query string", () => {
  assert.equal(toQuery({}), "");
});

test("a repeatable filter is repeated, not comma-separated", () => {
  assert.equal(toQuery({ label: ["a", "b"] }), "?label=a&label=b");
  assert.equal(toQuery({ status: ["open", "in_progress"] }), "?status=open&status=in_progress");
  assert.equal(toQuery({ q: ["parser", "crash"] }), "?q=parser&q=crash");
});

test("booleans are written true or false", () => {
  assert.equal(toQuery({ "include-closed": true }), "?include-closed=true");
  assert.equal(toQuery({ unassigned: true }), "?unassigned=true");
});

test("parameter names are the kebab-case ones", () => {
  assert.equal(toQuery({ "priority-max": 1 }), "?priority-max=1");
});

test("zero is sent, because limit=0 means no rows", () => {
  assert.equal(toQuery({ limit: 0 }), "?limit=0");
});

test("undefined and empty values are omitted", () => {
  assert.equal(toQuery({ parent: undefined, sort: "" }), "");
  assert.equal(toQuery({ label: [] }), "");
});

test("values are escaped", () => {
  assert.equal(toQuery({ label: ["team/web"] }), "?label=team%2Fweb");
  assert.equal(toQuery({ q: ["two words"] }), "?q=two+words");
});

test("several filters combine", () => {
  const query = toQuery({ project: ["awb"], label: ["parser"], filter: "needle docs", "include-closed": true });
  const params = new URLSearchParams(query.slice(1));
  assert.deepEqual(params.getAll("project"), ["awb"]);
  assert.deepEqual(params.getAll("label"), ["parser"]);
  assert.equal(params.get("filter"), "needle docs");
  assert.equal(params.get("include-closed"), "true");
});

test("profile edits use the safely encoded path and advance its ETag", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(JSON.stringify({ name: "a/b" }), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        ETag: calls.length === 1 ? '"user-v1"' : calls.length === 2 ? '"user-v2"' : '"user-v3"',
      },
    });
  });

  await api.user("a/b");
  await api.updateUser("a/b", { full_name: "Alice Andersson" });
  await api.updateUser("a/b", { password: "changed" });

  assert.equal(calls[0].path, "api/users/a%2Fb");
  assert.equal(calls[1].path, "api/users/a%2Fb");
  assert.equal(calls[1].init.method, "PATCH");
  assert.equal(new Headers(calls[1].init.headers).get("Content-Type"), "application/json");
  assert.equal(new Headers(calls[1].init.headers).get("If-Match"), '"user-v1"');
  assert.equal(calls[1].init.body, JSON.stringify({ full_name: "Alice Andersson" }));
  assert.equal(new Headers(calls[2].init.headers).get("If-Match"), '"user-v2"');
  assert.equal(calls[2].init.body, JSON.stringify({ password: "changed" }));
});

test("project preferences use their dedicated recovery endpoints", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(calls.length === 1 ? "[]" : "{}", { status: 200 });
  });

  await api.projectPreferences();
  await api.setProjectIgnored("team/web", true);

  assert.equal(calls[0].path, "api/preferences/projects");
  assert.equal(calls[1].path, "api/preferences/projects/team%2Fweb");
  assert.equal(calls[1].init.method, "PUT");
  assert.deepEqual(JSON.parse(calls[1].init.body), { ignored: true });
});

test("board views use stable encoded paths, ETags and paged board parameters", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    const body = calls.length === 3 ? { lanes: [], lane_total: 0, projects_omitted: false } : {
      id: "view-aaaaaaaaaaaaaaaaaaaaaaaa", name: "Release", owner: "alex", shared: false,
      all_projects: true, projects: [], labels: [], assignees: [], priority_max: 4,
      created_at: "2026-09-01T00:00:00.000Z", updated_at: "2026-09-01T00:00:00.000Z",
    };
    return new Response(JSON.stringify(body), { status: 200, headers: { ETag: '"view-v1"' } });
  });

  await api.boardView("view-aaaaaaaaaaaaaaaaaaaaaaaa");
  await api.updateBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa", { name: "Next" });
  await api.board("view-aaaaaaaaaaaaaaaaaaaaaaaa", {
    project: ["awb", "web"], status: "open", "lane-limit": 10, "card-offset": 8,
  });

  assert.equal(calls[0].path, "api/board-views/view-aaaaaaaaaaaaaaaaaaaaaaaa");
  assert.equal(new Headers(calls[1].init.headers).get("If-Match"), '"view-v1"');
  const boardURL = new URL(calls[2].path, "https://example.test/");
  assert.deepEqual(boardURL.searchParams.getAll("project"), ["awb", "web"]);
  assert.equal(boardURL.searchParams.get("status"), "open");
  assert.equal(boardURL.searchParams.get("lane-limit"), "10");
  assert.equal(boardURL.searchParams.get("card-offset"), "8");
});

// Every listing is asked with the filters it accepts. Regression: the listing
// view passed one filter object to all of them, so an assignee in the URL made
// the ready listing a 400, include-closed did the same to blocked, and a sort
// did it to both facet menus.
test("ready filters drop what that endpoint does not accept", () => {
  assert.deepEqual(
    readyFilters({
      project: ["awb"],
      label: ["parser"],
      sort: "priority",
      status: ["open"],
      "include-closed": true,
      assignee: ["mikael"],
      unassigned: true,
      q: ["parser"],
      filter: "needle",
    }),
    { project: ["awb"], label: ["parser"], filter: "needle", sort: "priority" },
  );
});

test("ready filters keep an already narrow selection whole", () => {
  assert.deepEqual(readyFilters({}), {});
  assert.deepEqual(readyFilters({ parent: "awb-a1b2c3", limit: 10 }), {
    parent: "awb-a1b2c3",
    limit: 10,
  });
});

test("ready label facets keep the backend text filter and fixed selection", () => {
  assert.deepEqual(readyFacetFilters({
    filter: "needle",
    project: ["awb"],
    assignee: ["somebody"],
    "include-closed": true,
    sort: "priority",
    limit: 10,
  }), {
    filter: "needle",
    project: ["awb"],
    status: ["open"],
    unassigned: true,
    readiness: "ready",
  });
});

test("blocked filters drop the status set it fixes for itself", () => {
  assert.deepEqual(
    blockedFilters({
      assignee: ["mikael"],
      sort: "priority",
      status: ["open"],
      "include-closed": true,
    }),
    { assignee: ["mikael"], sort: "priority" },
  );
});

test("facet filters drop the sort the row order fixes", () => {
  assert.deepEqual(
    facetFilters({ label: ["parser"], status: ["open"], filter: "needle", sort: "created", limit: 50, offset: 100 }),
    { label: ["parser"], status: ["open"], filter: "needle" },
  );
});

test("issue edits use the mutation endpoints and guard the version that was read", async () => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (input, init = {}) => {
    requests.push({ input, init });
    return new Response("{}", {
      status: 200,
      headers: requests.length === 1 ? { ETag: '"issue-version"' } : {},
    });
  };

  try {
    await api.issue("awb-a/b");
    await api.updateIssue("awb-a/b", { title: "Changed" });
    await api.addLabel("awb-a/b", "team/web");
    await api.removeRelation("awb-a/b", "blocked-by", "awb-c d");
    await api.releaseIssue("awb-a/b", { assignee: "operator", force: true });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(requests[0].input, "api/issues/awb-a%2Fb");
  assert.equal(requests[1].input, "api/issues/awb-a%2Fb");
  assert.equal(requests[1].init.method, "PATCH");
  assert.equal(new Headers(requests[1].init.headers).get("If-Match"), '"issue-version"');
  assert.deepEqual(JSON.parse(requests[1].init.body), { title: "Changed" });

  assert.equal(requests[2].input, "api/issues/awb-a%2Fb/labels");
  assert.equal(requests[2].init.method, "POST");
  assert.deepEqual(JSON.parse(requests[2].init.body), { label: "team/web" });
  assert.equal(new Headers(requests[2].init.headers).get("If-Match"), '"issue-version"');

  assert.equal(requests[3].input, "api/issues/awb-a%2Fb/relations/blocked-by/awb-c%20d");
  assert.equal(requests[3].init.method, "DELETE");

  assert.equal(requests[4].input, "api/issues/awb-a%2Fb/release");
  assert.equal(requests[4].init.method, "POST");
  assert.deepEqual(JSON.parse(requests[4].init.body), { assignee: "operator", force: true });
  assert.equal(new Headers(requests[4].init.headers).get("If-Match"), '"issue-version"');
});

test("claim adds one assignee while forced release removes every assignee", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response("{}", { status: 200 });
  });

  await api.claimIssue("awb-123", { assignee: "second", force: false });
  await api.releaseIssue("awb-123", { assignee: "operator", force: true });

  assert.equal(calls[0].path, "api/issues/awb-123/claim");
  assert.deepEqual(JSON.parse(calls[0].init.body), { assignee: "second", force: false });
  assert.equal(calls[1].path, "api/issues/awb-123/release");
  assert.deepEqual(JSON.parse(calls[1].init.body), { assignee: "operator", force: true });
});

test("project edits patch the project resource with its ETag", async () => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (input, init = {}) => {
    requests.push({ input, init });
    return new Response("{}", {
      status: 200,
      headers: requests.length === 1 ? { ETag: '"project-version"' } : {},
    });
  };

  try {
    await api.project("team/web");
    await api.updateProject("team/web", { name: "Web", description: "Markdown" });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(requests[1].input, "api/projects/team%2Fweb");
  assert.equal(requests[1].init.method, "PATCH");
  assert.equal(new Headers(requests[1].init.headers).get("If-Match"), '"project-version"');
  assert.deepEqual(JSON.parse(requests[1].init.body), { name: "Web", description: "Markdown" });
});
