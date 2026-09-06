import assert from "node:assert/strict";
import test from "node:test";

import {
  defaultIssueTypes,
  hasEmptyIssueTypeSelection,
  issueTypeLabel,
  issueTypeVocabulary,
  selectedIssueTypes,
  withIssueTypes,
} from "../../static/type-filter.js";

test("type vocabulary and defaults match the backend contract", () => {
  assert.deepEqual(issueTypeVocabulary, ["epic", "feature", "bug", "task", "chore"]);
  assert.deepEqual(defaultIssueTypes, issueTypeVocabulary);
  assert.equal(issueTypeLabel("feature"), "Feature");
});

test("an absent type selection includes every type", () => {
  assert.deepEqual(selectedIssueTypes(new URLSearchParams()), defaultIssueTypes);
});

test("explicit type selections are canonical, unique, and ignore unknown values", () => {
  const query = new URLSearchParams("type=task&type=epic&type=task&type=unknown");
  assert.deepEqual(selectedIssueTypes(query), ["epic", "task"]);
});

test("type selections preserve other filters and reset pagination", () => {
  const query = new URLSearchParams(
    "workspace=awb&status=open&label=frontend&page=3&type=chore",
  );
  assert.equal(
    withIssueTypes(query, ["task", "epic"]).toString(),
    "workspace=awb&status=open&label=frontend&type=epic&type=task",
  );
  assert.equal(query.get("type"), "chore", "the current route is not mutated");
});

test("restoring every type removes type state from the URL", () => {
  const query = new URLSearchParams("workspace=awb&type=bug");
  assert.equal(withIssueTypes(query, defaultIssueTypes).toString(), "workspace=awb");
});

test("an empty type selection stays distinct from the parameter-free default", () => {
  const query = withIssueTypes(new URLSearchParams("workspace=awb"), []);
  assert.equal(query.toString(), "workspace=awb&type=");
  assert.equal(hasEmptyIssueTypeSelection(query), true);
  assert.deepEqual(selectedIssueTypes(query), []);
});
