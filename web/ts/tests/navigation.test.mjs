import assert from "node:assert/strict";
import test from "node:test";

import {
  legacyIssueSearchHref,
  namedDestinations,
  navigationPath,
  projectScopedHref,
} from "../../static/navigation.js";

test("issue listing tabs preserve selected workspace filters using compatibility query names", () => {
  const current = new URLSearchParams("project=awb&project=other%2Fteam&label=frontend&sort=-updated");

  assert.equal(projectScopedHref("ready", current), "#/ready?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("issues", current), "#/issues?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("workspaces", current), "#/workspaces?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("blocked", new URLSearchParams()), "#/blocked");
  assert.equal(projectScopedHref("boards", current), "#/boards?project=awb&project=other%2Fteam");
});

test("active navigation ignores preserved project filters", () => {
  assert.equal(navigationPath("#/issues?project=awb&project=other"), "issues");
  assert.equal(navigationPath("#/ready"), "ready");
  assert.equal(navigationPath("#/users"), "users");
});

test("legacy issue-search links migrate their query into the Issues filter", () => {
  assert.equal(
    legacyIssueSearchHref(new URLSearchParams(
      "q=parser&q=remote&project=awb&label=frontend&include-closed=true&page=4&sort=relevance",
    )),
    "#/issues?project=awb&label=frontend&include-closed=true&filter=parser+remote",
  );
  assert.equal(
    legacyIssueSearchHref(new URLSearchParams("filter=existing&q=extra&sort=-updated&page=2")),
    "#/issues?filter=existing+extra&sort=-updated",
  );
  assert.equal(legacyIssueSearchHref(new URLSearchParams()), "#/issues");

  const capped = new URLSearchParams(legacyIssueSearchHref(new URLSearchParams(
    `q=${"a".repeat(400)}&q=${"b".repeat(400)}`,
  )).split("?", 2)[1]);
  assert.equal(capped.get("filter").length, 500);
});

test("the command palette has no duplicate issue-search destination", () => {
  assert.equal(namedDestinations.some((destination) => destination.id === "search"), false);
  assert.equal(namedDestinations.filter((destination) => destination.path === "#/issues").length, 1);
});
