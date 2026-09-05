import assert from "node:assert/strict";
import test from "node:test";

import {
  defaultIssueStatuses,
  hasEmptyStatusSelection,
  issueStatusLabel,
  issueStatusVocabulary,
  selectedIssueStatuses,
  withIssueStatuses,
} from "../../static/status-filter.js";

test("status vocabulary and defaults match the backend contract", () => {
  assert.deepEqual(issueStatusVocabulary, ["backlog", "open", "in_progress", "closed"]);
  assert.deepEqual(defaultIssueStatuses, ["backlog", "open", "in_progress"]);
  assert.equal(issueStatusLabel("in_progress"), "In progress");
});

test("an absent selection uses defaults and legacy closed links still widen it", () => {
  assert.deepEqual(selectedIssueStatuses(new URLSearchParams()), defaultIssueStatuses);
  assert.deepEqual(
    selectedIssueStatuses(new URLSearchParams("include-closed=true")),
    issueStatusVocabulary,
  );
});

test("explicit selections are canonical, unique, and ignore unknown values", () => {
  const query = new URLSearchParams("status=closed&status=open&status=open&status=unknown");
  assert.deepEqual(selectedIssueStatuses(query), ["open", "closed"]);
});

test("legacy include-closed widens an explicit status selection in the UI too", () => {
  const query = new URLSearchParams("status=open&include-closed=true");
  assert.deepEqual(selectedIssueStatuses(query), ["open", "closed"]);
});

test("status selections preserve other filters and reset pagination", () => {
  const query = new URLSearchParams(
    "workspace=awb&type=bug&priority=1&label=frontend&assignee=alex&page=3&include-closed=true",
  );
  assert.equal(
    withIssueStatuses(query, ["closed", "open"]).toString(),
    "workspace=awb&type=bug&priority=1&label=frontend&assignee=alex&status=open&status=closed",
  );
  assert.equal(query.has("status"), false, "the current route is not mutated");
});

test("restoring defaults removes status state from the URL", () => {
  const query = new URLSearchParams("workspace=awb&status=closed&include-closed=true");
  assert.equal(withIssueStatuses(query, defaultIssueStatuses).toString(), "workspace=awb");
});

test("an empty selection stays distinct from the parameter-free default", () => {
  const query = withIssueStatuses(new URLSearchParams("workspace=awb"), []);
  assert.equal(query.toString(), "workspace=awb&status=");
  assert.equal(hasEmptyStatusSelection(query), true);
  assert.deepEqual(selectedIssueStatuses(query), []);
});
