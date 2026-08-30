import assert from "node:assert/strict";
import test from "node:test";

import { commentSubmitShortcut } from "../../static/keyboard.js";

function key(overrides = {}) {
  return {
    key: "Enter",
    ctrlKey: false,
    metaKey: false,
    isComposing: false,
    ...overrides,
  };
}

test("Ctrl+Enter and Cmd+Enter submit a comment", () => {
  assert.equal(commentSubmitShortcut(key({ ctrlKey: true })), true);
  assert.equal(commentSubmitShortcut(key({ metaKey: true })), true);
});

test("plain Enter remains a newline and composition confirmation never submits", () => {
  assert.equal(commentSubmitShortcut(key()), false);
  assert.equal(commentSubmitShortcut(key({ key: "Space", ctrlKey: true })), false);
  assert.equal(commentSubmitShortcut(key({ ctrlKey: true, isComposing: true })), false);
});
