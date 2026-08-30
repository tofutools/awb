import assert from "node:assert/strict";
import test from "node:test";

import { navigationPath, projectScopedHref } from "../../static/navigation.js";

test("issue listing tabs preserve selected projects only", () => {
  const current = new URLSearchParams("project=awb&project=other%2Fteam&label=frontend&sort=-updated");

  assert.equal(projectScopedHref("ready", current), "#/ready?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("issues", current), "#/issues?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("projects", current), "#/projects?project=awb&project=other%2Fteam");
  assert.equal(projectScopedHref("blocked", new URLSearchParams()), "#/blocked");
});

test("active navigation ignores preserved project filters", () => {
  assert.equal(navigationPath("#/issues?project=awb&project=other"), "issues");
  assert.equal(navigationPath("#/ready"), "ready");
});
