import assert from "node:assert/strict";
import test from "node:test";
import { pendingComment } from "../../static/comment-key.js";

test("an unchanged comment keeps its key across retries", () => {
  const first = pendingComment(null, "same", () => "key-1");
  const retry = pendingComment(first, "same", () => "key-2");
  assert.equal(retry, first);
  assert.equal(retry.key, "key-1");
});

test("editing a failed comment gives the new logical comment a fresh key", () => {
  const first = pendingComment(null, "before", () => "key-1");
  const edited = pendingComment(first, "after", () => "key-2");
  assert.deepEqual(edited, { body: "after", key: "key-2" });
});
