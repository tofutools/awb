import assert from "node:assert/strict";
import test from "node:test";

import { toQuery } from "../../static/api.js";
import { childIssueFilters } from "../../static/issue-children.js";

test("child issue requests follow the show-closed preference", () => {
  assert.equal(
    toQuery(childIssueFilters("awb-parent", true)),
    "?parent=awb-parent&include-closed=true&include-archived=true",
  );
  assert.equal(
    toQuery(childIssueFilters("awb-parent", false)),
    "?parent=awb-parent&include-closed=false&include-archived=true",
  );
});
