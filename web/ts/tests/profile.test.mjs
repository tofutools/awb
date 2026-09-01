import assert from "node:assert/strict";
import test from "node:test";

import { ApiError } from "../../static/api.js";
import {
  accountRoles,
  profileIdentity,
  saveProfileFullName,
} from "../../static/profile.js";

test("a user sees each administrative capability they hold", () => {
  assert.deepEqual(accountRoles({ user_admin: true, project_admin: true }), [
    "User administrator",
    "Workspace administrator",
  ]);
});

test("an account without administrative capabilities is a regular user", () => {
  assert.deepEqual(accountRoles({ user_admin: false, project_admin: false }), ["Regular user"]);
});

function user(overrides = {}) {
  return {
    name: "alice",
    full_name: "Alice Andersson",
    project_admin: false,
    user_admin: false,
    created_at: "2026-08-31T10:00:00Z",
    updated_at: "2026-08-31T10:00:00Z",
    projects: [],
    ...overrides,
  };
}

test("saving a full name updates every versioned profile value", async () => {
  const current = user();
  const calls = [];
  const result = await saveProfileFullName(current, "Alice Berg", async (name, patch) => {
    calls.push({ name, patch });
    return user({ full_name: "Alice Berg", updated_at: "2026-08-31T11:00:00Z" });
  });

  assert.deepEqual(calls, [{ name: "alice", patch: { full_name: "Alice Berg" } }]);
  assert.equal(result.ok, true);
  assert.equal(result.message, "Full name saved.");
  assert.deepEqual(profileIdentity(result.user), {
    heading: "Alice Berg",
    detail: "@alice · Your account and access",
    updated: "2026-08-31T11:00:00Z",
  });
});

test("a refused full name keeps the current profile intact", async () => {
  const current = user();
  const result = await saveProfileFullName(current, "line\nbreak", async () => {
    throw new ApiError(400, "full name must not contain control characters");
  });

  assert.equal(result.ok, false);
  assert.equal(result.user, current);
  assert.equal(result.message, "full name must not contain control characters");
  assert.deepEqual(profileIdentity(result.user), {
    heading: "Alice Andersson",
    detail: "@alice · Your account and access",
    updated: "2026-08-31T10:00:00Z",
  });
});
