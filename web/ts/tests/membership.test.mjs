import assert from "node:assert/strict";
import test from "node:test";

import {
  mayManageWorkspaceMembership,
  membershipAdditionError,
  membershipChangeConfirmation,
  membershipSuggestions,
} from "../../static/membership.js";

const membership = (user, access, workspace = "awb") => ({ workspace, user, access });

test("membership administration follows effective workspace access", () => {
  const regular = { workspace_admin: false, workspaces: [membership("alice", "regular")] };
  const admin = { workspace_admin: false, workspaces: [membership("alice", "admin")] };
  const globalAdmin = { workspace_admin: true, workspaces: [] };

  assert.equal(mayManageWorkspaceMembership("alice", regular, "awb"), false);
  assert.equal(mayManageWorkspaceMembership("alice", admin, "awb"), true);
  assert.equal(mayManageWorkspaceMembership("alice", globalAdmin, "other"), true);
  assert.equal(mayManageWorkspaceMembership("", null, "awb"), true);
  assert.equal(
    mayManageWorkspaceMembership("alice", regular, "web", [membership("alice", "admin", "web")]),
    true,
    "the dedicated member list retains ignored-workspace administration",
  );
});

test("scoped user suggestions omit members and preserve useful names", () => {
  const users = [
    { name: "alice", full_name: "Alice Andersson" },
    { name: "bob", full_name: "" },
  ];

  assert.deepEqual(membershipSuggestions(users, [membership("alice", "admin")]), [
    { value: "bob", label: "bob", detail: undefined },
  ]);
});

test("the add flow rejects duplicates and stale rows instead of restoring access", () => {
  const renderedMembers = [membership("alice", "admin"), membership("bob", "regular")];

  assert.equal(membershipAdditionError("carol", renderedMembers), null);
  assert.equal(
    membershipAdditionError("bob", renderedMembers),
    "@bob already has regular access. Use that member's Access control to grant different access.",
  );
});

test("self-removal and the last stored administrator get explicit warnings", () => {
  const alice = membership("alice", "admin");
  const bob = membership("bob", "regular");

  const lastAdmin = membershipChangeConfirmation(alice, [alice, bob], "alice", null);
  assert.match(lastAdmin, /last stored workspace administrator/);
  assert.match(lastAdmin, /lose access/);
  assert.match(lastAdmin, /direct database access/);

  const lastAdminDemotion = membershipChangeConfirmation(alice, [alice, bob], "alice", "regular");
  assert.match(lastAdminDemotion, /lose the membership management controls/);
  assert.doesNotMatch(lastAdminDemotion, /lose access to this membership page/);

  const ordinaryRemoval = membershipChangeConfirmation(bob, [alice, bob], "alice", null);
  assert.equal(ordinaryRemoval, "Do you want to remove @bob from this workspace?");

  const promotion = membershipChangeConfirmation(bob, [alice, bob], "alice", "admin");
  assert.equal(promotion, "Do you want to change @bob to admin access?");
  assert.equal(
    membershipChangeConfirmation(bob, [alice, bob], "bob", "admin"),
    "Do you want to change @bob to admin access?",
  );

  const twoAdmins = [alice, membership("carol", "admin")];
  assert.match(membershipChangeConfirmation(alice, twoAdmins, "alice", "regular"), /lose workspace administration/);
  assert.doesNotMatch(membershipChangeConfirmation(alice, twoAdmins, "alice", "regular"), /last stored/);
});
