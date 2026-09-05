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
  const query = toQuery({ workspace: ["awb"], label: ["parser"], epic: "awb-a1b2c3", filter: "needle docs", "include-closed": true });
  const params = new URLSearchParams(query.slice(1));
  assert.deepEqual(params.getAll("workspace"), ["awb"]);
  assert.deepEqual(params.getAll("label"), ["parser"]);
  assert.equal(params.get("filter"), "needle docs");
  assert.equal(params.get("epic"), "awb-a1b2c3");
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

test("user administration creates and version-deletes accounts", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(JSON.stringify({ name: "a/b" }), {
      status: calls.length === 1 ? 200 : calls.length === 2 ? 201 : 200,
      headers: calls.length === 1 ? { ETag: '"user-v1"' } : { "Content-Type": "application/json" },
    });
  });

  await api.user("a/b");
  await api.createUser({ name: "new-user", password: "safe password", user_admin: true });
  await api.deleteUser("a/b");

  assert.equal(calls[1].path, "api/users");
  assert.equal(calls[1].init.method, "POST");
  assert.equal(new Headers(calls[1].init.headers).get("Content-Type"), "application/json");
  assert.deepEqual(JSON.parse(calls[1].init.body), {
    name: "new-user",
    password: "safe password",
    user_admin: true,
  });
  assert.equal(calls[2].path, "api/users/a%2Fb");
  assert.equal(calls[2].init.method, "DELETE");
  assert.equal(new Headers(calls[2].init.headers).get("If-Match"), '"user-v1"');
});

test("issue creation posts the complete atomic create body", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(JSON.stringify({ id: "awb-created" }), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    });
  });

  const body = {
    workspace: "awb",
    title: "Create tickets in the UI",
    description: "Use the shared editor.",
    type: "feature",
    priority: 1,
    assignees: ["alex"],
    labels: ["frontend", "release/1.0"],
    relations: [{ type: "has-parent", other: "awb-epic" }],
  };
  await api.createIssue(body);

  assert.equal(calls[0].path, "api/issues");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(new Headers(calls[0].init.headers).get("Content-Type"), "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), body);
});

test("workspace preferences use their dedicated recovery endpoints", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(calls.length === 1 ? "[]" : "{}", { status: 200 });
  });

  await api.workspacePreferences();
  await api.setWorkspaceIgnored("team/web", true);

  assert.equal(calls[0].path, "api/preferences/workspaces");
  assert.equal(calls[1].path, "api/preferences/workspaces/team%2Fweb");
  assert.equal(calls[1].init.method, "PUT");
  assert.deepEqual(JSON.parse(calls[1].init.body), { ignored: true });
});

test("board views use stable encoded paths, ETags and paged board parameters", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    const body = calls.length === 3 ? { lanes: [], lane_total: 0, workspaces_omitted: false } : {
      id: "view-aaaaaaaaaaaaaaaaaaaaaaaa", name: "Release", owner: "alex", shared: false,
      all_workspaces: true, workspaces: [], labels: [], assignees: [], priority_max: 4,
      created_at: "2026-09-01T00:00:00.000Z", updated_at: "2026-09-01T00:00:00.000Z",
    };
    return new Response(JSON.stringify(body), { status: 200, headers: { ETag: '"view-v1"' } });
  });

  await api.boardView("view-aaaaaaaaaaaaaaaaaaaaaaaa");
  await api.updateBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa", { name: "Next" });
  await api.board("view-aaaaaaaaaaaaaaaaaaaaaaaa", {
    workspace: ["awb", "web"], "all-workspaces": false, "hidden-epic": ["awb-hidden", "web-hidden"], status: "open",
    "all-epics": false, "selected-epic": ["awb-selected"], "include-no-epic": false,
    label: ["release", "frontend"], assignee: ["alex"], "priority-max": 2,
    epic: "awb-epic", "lane-limit": 10, "card-offset": 8, "epic-closed-days": 5,
  });

  assert.equal(calls[0].path, "api/board-views/view-aaaaaaaaaaaaaaaaaaaaaaaa");
  assert.equal(new Headers(calls[1].init.headers).get("If-Match"), '"view-v1"');
  const boardURL = new URL(calls[2].path, "https://example.test/");
  assert.deepEqual(boardURL.searchParams.getAll("workspace"), ["awb", "web"]);
  assert.equal(boardURL.searchParams.get("all-workspaces"), "false");
  assert.deepEqual(boardURL.searchParams.getAll("hidden-epic"), ["awb-hidden", "web-hidden"]);
  assert.equal(boardURL.searchParams.get("all-epics"), "false");
  assert.deepEqual(boardURL.searchParams.getAll("selected-epic"), ["awb-selected"]);
  assert.equal(boardURL.searchParams.get("include-no-epic"), "false");
  assert.deepEqual(boardURL.searchParams.getAll("label"), ["release", "frontend"]);
  assert.deepEqual(boardURL.searchParams.getAll("assignee"), ["alex"]);
  assert.equal(boardURL.searchParams.get("priority-max"), "2");
  assert.equal(boardURL.searchParams.get("status"), "open");
  assert.equal(boardURL.searchParams.get("epic"), "awb-epic");
  assert.equal(boardURL.searchParams.get("lane-limit"), "10");
  assert.equal(boardURL.searchParams.get("card-offset"), "8");
  assert.equal(boardURL.searchParams.get("epic-closed-days"), "5");
});

test("identity exposes the backend's effective account-administration capability", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({
    identity: "fixed-name",
    may_manage_users: true,
  }), { status: 200, headers: { "Content-Type": "application/json" } }));

  assert.deepEqual(await api.identity(), { identity: "fixed-name", may_manage_users: true });
});

// Every listing is asked with the filters it accepts. Regression: the listing
// view passed one filter object to all of them, so an assignee in the URL made
// the ready listing a 400, include-closed did the same to blocked, and a sort
// did it to both facet menus.
test("ready filters drop what that endpoint does not accept", () => {
  assert.deepEqual(
    readyFilters({
      workspace: ["awb"],
      label: ["parser"],
      sort: "priority",
      status: ["open"],
      "include-closed": true,
      assignee: ["mikael"],
      unassigned: true,
      q: ["parser"],
      filter: "needle",
    }),
    { workspace: ["awb"], label: ["parser"], filter: "needle", sort: "priority" },
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
    workspace: ["awb"],
    assignee: ["somebody"],
    "include-closed": true,
    sort: "priority",
    limit: 10,
  }), {
    filter: "needle",
    workspace: ["awb"],
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
    await api.moveIssue("awb-a/b", { epic: "awb-epic", status: "open", direction: "earlier" });
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

  assert.equal(requests[5].input, "api/issues/awb-a%2Fb/move");
  assert.equal(requests[5].init.method, "POST");
  assert.deepEqual(JSON.parse(requests[5].init.body), { epic: "awb-epic", status: "open", direction: "earlier" });
  assert.equal(new Headers(requests[5].init.headers).get("If-Match"), '"issue-version"');
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

test("workspace edits patch the workspace resource with its ETag", async () => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (input, init = {}) => {
    requests.push({ input, init });
    return new Response("{}", {
      status: 200,
      headers: requests.length === 1 ? { ETag: '"workspace-version"' } : {},
    });
  };

  try {
    await api.workspace("team/web");
    await api.updateWorkspace("team/web", { name: "Web", description: "Markdown" });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(requests[1].input, "api/workspaces/team%2Fweb");
  assert.equal(requests[1].init.method, "PATCH");
  assert.equal(new Headers(requests[1].init.headers).get("If-Match"), '"workspace-version"');
  assert.deepEqual(JSON.parse(requests[1].init.body), { name: "Web", description: "Markdown" });
});

test("workspace creation and lifecycle use stable paths and advance the workspace ETag", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(JSON.stringify(calls.length === 5 ? [] : { key: "team/web" }), {
      status: calls.length === 1 ? 201 : 200,
      headers: { "Content-Type": "application/json", ETag: `"v${calls.length}"` },
    });
  });

  await api.createWorkspace({ key: "team/web", name: "Web" });
  await api.updateWorkspace("team/web", { description: "Client" });
  await api.archiveWorkspace("team/web");
  await api.restoreWorkspace("team/web");
  await api.workspaceActivity("team/web");

  assert.equal(calls[0].path, "api/workspaces");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[1].path, "api/workspaces/team%2Fweb");
  assert.equal(new Headers(calls[1].init.headers).get("If-Match"), '"v1"');
  assert.equal(calls[2].path, "api/workspaces/team%2Fweb/archive");
  assert.equal(new Headers(calls[2].init.headers).get("If-Match"), '"v2"');
  assert.equal(calls[3].path, "api/workspaces/team%2Fweb/restore");
  assert.equal(new Headers(calls[3].init.headers).get("If-Match"), '"v3"');
  assert.equal(calls[4].path, "api/workspaces/team%2Fweb/activity");
});

test("workspace membership writes distinguish creation from the idempotent resource", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (path, init = {}) => {
    calls.push({ path, init });
    return new Response(JSON.stringify({ workspace: "team/web", user: "a/b", access: "admin" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });

  await api.addWorkspaceMember("team/web", "a/b", "admin");
  await api.setWorkspaceMember("team/web", "a/b", "admin");
  await api.removeWorkspaceMember("team/web", "a/b");

  assert.equal(calls[0].path, "api/workspaces/team%2Fweb/members");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(new Headers(calls[0].init.headers).get("Content-Type"), "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), { user: "a/b", access: "admin" });
  assert.equal(calls[1].path, "api/workspaces/team%2Fweb/members/a%2Fb");
  assert.equal(calls[1].init.method, "PUT");
  assert.deepEqual(JSON.parse(calls[1].init.body), { access: "admin" });
  assert.equal(calls[2].path, "api/workspaces/team%2Fweb/members/a%2Fb");
  assert.equal(calls[2].init.method, "DELETE");
});
