// The API client's query building, which has to match the CLI's flags exactly:
// a repeatable filter is repeated rather than comma-separated, and the names
// are the kebab-case ones the server accepts.

import assert from "node:assert/strict";
import test from "node:test";

import { readyFilters, toQuery } from "../../static/api.js";

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
  const query = toQuery({ project: ["awb"], label: ["parser"], "include-closed": true });
  const params = new URLSearchParams(query.slice(1));
  assert.deepEqual(params.getAll("project"), ["awb"]);
  assert.deepEqual(params.getAll("label"), ["parser"]);
  assert.equal(params.get("include-closed"), "true");
});

// /api/ready fixes the status set and the assignee filter for itself and
// accepts neither, so a route carrying either must have them removed before it
// is asked. Regression: the listing view passed one filter object to every
// endpoint, so an assignee in the URL made the ready listing a 400.
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
    }),
    { project: ["awb"], label: ["parser"], sort: "priority" },
  );
});

test("ready filters keep an already narrow selection whole", () => {
  assert.deepEqual(readyFilters({}), {});
  assert.deepEqual(readyFilters({ parent: "awb-a1b2c3", limit: 10 }), {
    parent: "awb-a1b2c3",
    limit: 10,
  });
});
