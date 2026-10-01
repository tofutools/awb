import assert from "node:assert/strict";
import test from "node:test";

import { commitLink } from "../../static/commit-link.js";

test("commit links use each host's commit route", () => {
  const hash = "abcdef1234567";
  assert.equal(commitLink("https://github.com/team/repo", hash), `https://github.com/team/repo/commit/${hash}`);
  assert.equal(commitLink("https://gitlab.com/team/repo.git/", hash), `https://gitlab.com/team/repo/-/commit/${hash}`);
  assert.equal(commitLink("https://bitbucket.org/team/repo", hash), `https://bitbucket.org/team/repo/commits/${hash}`);
});

test("unconfigured or unsupported repositories keep the hash unlinked", () => {
  assert.equal(commitLink("", "abcdef1"), undefined);
  assert.equal(commitLink("https://example.com/team/repo", "abcdef1"), undefined);
  assert.equal(commitLink("javascript:alert(1)", "abcdef1"), undefined);
  assert.equal(commitLink("https://github.com/team/repo", "not-a-hash"), undefined);
});
