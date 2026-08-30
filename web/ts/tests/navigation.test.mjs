import assert from "node:assert/strict";
import test from "node:test";

import { issueListingHref } from "../../static/navigation.js";

test("issue listing tabs preserve selected projects only", () => {
  const current = new URLSearchParams("project=awb&project=other%2Fteam&label=frontend&sort=-updated");

  assert.equal(issueListingHref("ready", current), "#/ready?project=awb&project=other%2Fteam");
  assert.equal(issueListingHref("issues", current), "#/issues?project=awb&project=other%2Fteam");
  assert.equal(issueListingHref("blocked", new URLSearchParams()), "#/blocked");
});
