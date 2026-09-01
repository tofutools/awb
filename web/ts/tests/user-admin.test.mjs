import assert from "node:assert/strict";
import test from "node:test";

import {
  mayAdministerUsers,
  userDeletionImpact,
  userDeletionWarning,
  userEditorHref,
} from "../../static/user-admin.js";

function user(overrides = {}) {
  return {
    name: "alice",
    full_name: "Alice Andersson",
    project_admin: false,
    user_admin: false,
    created_at: "2026-09-01T06:00:00Z",
    updated_at: "2026-09-01T06:00:00Z",
    projects: [],
    activity_projects: [],
    ...overrides,
  };
}

test("only user administrators and bootstrap mode expose account administration", () => {
  assert.equal(mayAdministerUsers(null), true);
  assert.equal(mayAdministerUsers(user({ user_admin: true })), true);
  assert.equal(mayAdministerUsers(user({ project_admin: true })), false);
  assert.equal(mayAdministerUsers(user()), false);
});

test("editor links encode the account name", () => {
  assert.equal(userEditorHref("team/user"), "#/users/team%2Fuser");
});

test("deletion impact identifies self, memberships, and the last user administrator", () => {
  const target = user({
    user_admin: true,
    projects: [
      { project: "awb", user: "alice", access: "admin" },
      { project: "ops", user: "alice", access: "regular" },
    ],
  });
  const impact = userDeletionImpact(target, [target, user({ name: "bob" })], "alice");

  assert.deepEqual(impact, { memberships: 2, self: true, lastUserAdministrator: true });
  assert.match(userDeletionWarning(target, impact), /deleting your own account/i);
  assert.match(userDeletionWarning(target, impact), /last user administrator/i);
  assert.match(userDeletionWarning(target, impact), /2 project memberships/i);
});

test("another user administrator removes the last-admin warning", () => {
  const target = user({ user_admin: true });
  const impact = userDeletionImpact(
    target,
    [target, user({ name: "dana", user_admin: true })],
    "dana",
  );
  assert.deepEqual(impact, { memberships: 0, self: false, lastUserAdministrator: false });
  assert.doesNotMatch(userDeletionWarning(target, impact), /last user administrator/i);
});
