import assert from "node:assert/strict";
import test from "node:test";

import {
  commentSubmitShortcut,
  inspectorDismissShortcut,
  issueEditorShortcut,
} from "../../static/keyboard.js";

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

test("Escape hides the issue editor and Ctrl+Enter or Cmd+Enter saves it", () => {
  assert.equal(issueEditorShortcut(key({ key: "Escape" })), "hide");
  assert.equal(issueEditorShortcut(key({ ctrlKey: true })), "save");
  assert.equal(issueEditorShortcut(key({ metaKey: true })), "save");
});

test("plain keys and input-method composition do not control the issue editor", () => {
  assert.equal(issueEditorShortcut(key()), undefined);
  assert.equal(issueEditorShortcut(key({ key: "Space", ctrlKey: true })), undefined);
  assert.equal(issueEditorShortcut(key({ key: "Escape", isComposing: true })), undefined);
  assert.equal(issueEditorShortcut(key({ ctrlKey: true, isComposing: true })), undefined);
});

test("Escape closes an inspector field only after an inner control has declined it", () => {
  assert.equal(inspectorDismissShortcut({ key: "Escape", defaultPrevented: false }), true);
  assert.equal(inspectorDismissShortcut({ key: "Escape", defaultPrevented: true }), false);
  assert.equal(inspectorDismissShortcut({ key: "Enter", defaultPrevented: false }), false);
});
