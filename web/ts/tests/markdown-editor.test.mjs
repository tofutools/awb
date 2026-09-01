import assert from "node:assert/strict";
import test from "node:test";

import { markdownEditorKeymap } from "../../static/markdown-editor.js";
import {
  EditorState,
  defaultKeymap,
  historyKeymap,
} from "../../static/vendor/codemirror-6.43.3.js";

test("the form save shortcut precedes CodeMirror's blank-line binding", () => {
  const bindings = markdownEditorKeymap(defaultKeymap, historyKeymap);
  const defaultSave = defaultKeymap.find((binding) => binding.key === "Mod-Enter");

  assert.equal(bindings[0].key, "Mod-Enter");
  assert.equal(bindings[0].run(), true);
  assert.ok(bindings.indexOf(defaultSave) > 0);

  const state = EditorState.create({ doc: "one\ntwo" });
  let changed;
  assert.equal(defaultSave.run({
    state,
    dispatch(transaction) { changed = transaction.state; },
  }), true);
  assert.notEqual(changed.doc.toString(), state.doc.toString());
});
