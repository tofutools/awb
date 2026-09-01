import assert from "node:assert/strict";
import test from "node:test";

import {
  legacyIssueSearchHref,
  namedDestinations,
  navigationPath,
  workspaceScopedHref,
} from "../../static/navigation.js";

test("issue listing tabs preserve selected workspace filters", () => {
  const current = new URLSearchParams("workspace=awb&workspace=other%2Fteam&label=frontend&sort=-updated");

  assert.equal(workspaceScopedHref("ready", current), "#/ready?workspace=awb&workspace=other%2Fteam");
  assert.equal(workspaceScopedHref("issues", current), "#/issues?workspace=awb&workspace=other%2Fteam");
  assert.equal(workspaceScopedHref("workspaces", current), "#/workspaces?workspace=awb&workspace=other%2Fteam");
  assert.equal(workspaceScopedHref("blocked", new URLSearchParams()), "#/blocked");
  assert.equal(workspaceScopedHref("boards", current), "#/boards?workspace=awb&workspace=other%2Fteam");
});

test("active navigation ignores preserved workspace filters", () => {
  assert.equal(navigationPath("#/issues?workspace=awb&workspace=other"), "issues");
  assert.equal(navigationPath("#/ready"), "ready");
  assert.equal(navigationPath("#/users"), "users");
});

test("legacy issue-search links migrate their query into the Issues filter", () => {
  assert.equal(
    legacyIssueSearchHref(new URLSearchParams(
      "q=parser&q=remote&workspace=awb&label=frontend&include-closed=true&page=4&sort=relevance",
    )),
    "#/issues?workspace=awb&label=frontend&include-closed=true&filter=parser+remote",
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
