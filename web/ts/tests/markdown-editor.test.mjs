import assert from "node:assert/strict";
import test from "node:test";

import { markdownEditorKeymap } from "../../static/markdown-editor.js";

test("the form save shortcut is reserved before CodeMirror's default bindings", () => {
  const defaultBinding = { key: "Mod-Enter", run: () => false };
  const historyBinding = { key: "Mod-z", run: () => false };
  const bindings = markdownEditorKeymap([defaultBinding], [historyBinding]);

  assert.equal(bindings[0].key, "Mod-Enter");
  assert.equal(bindings[0].run(), true);
  assert.deepEqual(bindings.slice(1), [defaultBinding, historyBinding]);
});
