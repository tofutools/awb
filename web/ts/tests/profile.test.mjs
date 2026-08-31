import assert from "node:assert/strict";
import test from "node:test";

import { accountRoles } from "../../static/profile.js";

test("a user sees each administrative capability they hold", () => {
  assert.deepEqual(accountRoles({ user_admin: true, project_admin: true }), [
    "User administrator",
    "Project administrator",
  ]);
});

test("an account without administrative capabilities is a regular user", () => {
  assert.deepEqual(accountRoles({ user_admin: false, project_admin: false }), ["Regular user"]);
});
