import assert from "node:assert/strict";
import test from "node:test";

import {
  userCreateHref,
  userDeletionImpact,
  userDeletionWarning,
  userEditorHref,
  userNameFromRouteSegment,
} from "../../static/user-admin.js";

function user(overrides = {}) {
  return {
    name: "alice",
    full_name: "Alice Andersson",
    workspace_admin: false,
    user_admin: false,
    created_at: "2026-09-01T06:00:00Z",
    updated_at: "2026-09-01T06:00:00Z",
    workspaces: [],
    activity_workspaces: [],
    ...overrides,
  };
}

test("editor links encode the account name", () => {
  assert.equal(userEditorHref("team/user"), "#/users/team%2Fuser");
  assert.equal(userNameFromRouteSegment("team%2Fuser"), "team/user");
  assert.equal(userEditorHref("new"), "#/users/new");
  assert.equal(userCreateHref, "#/users/-/new");
});

test("deletion impact identifies self, memberships, and the last user administrator", () => {
  const target = user({
    user_admin: true,
    workspaces: [
      { workspace: "awb", user: "alice", access: "admin" },
      { workspace: "ops", user: "alice", access: "regular" },
    ],
  });
  const impact = userDeletionImpact(target, [target, user({ name: "bob" })], "alice");

  assert.deepEqual(impact, { memberships: 2, self: true, lastUserAdministrator: true });
  assert.match(userDeletionWarning(target, impact), /deleting your own account/i);
  assert.match(userDeletionWarning(target, impact), /last user administrator/i);
  assert.match(userDeletionWarning(target, impact), /2 workspace memberships/i);
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
