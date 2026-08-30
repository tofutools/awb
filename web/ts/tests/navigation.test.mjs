import assert from "node:assert/strict";
import test from "node:test";

import { issueListingHref, navigationPath } from "../../static/navigation.js";

test("issue listing tabs preserve selected projects only", () => {
  const current = new URLSearchParams("project=awb&project=other%2Fteam&label=frontend&sort=-updated");

  assert.equal(issueListingHref("ready", current), "#/ready?project=awb&project=other%2Fteam");
  assert.equal(issueListingHref("issues", current), "#/issues?project=awb&project=other%2Fteam");
  assert.equal(issueListingHref("blocked", new URLSearchParams()), "#/blocked");
});

test("active navigation ignores preserved project filters", () => {
  assert.equal(navigationPath("#/issues?project=awb&project=other"), "issues");
  assert.equal(navigationPath("#/ready"), "ready");
});
