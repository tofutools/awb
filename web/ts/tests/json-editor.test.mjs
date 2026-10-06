import assert from "node:assert/strict";
import test from "node:test";
import { metadataDiagnostics } from "../../static/json-editor.js";
import { vendorBundle } from "./vendor.mjs";

const { EditorState, json, syntaxTree } = await import(vendorBundle("codemirror"));

test("the bundled JSON language parses nested metadata and flags syntax errors", () => {
  const parse = (doc) => syntaxTree(EditorState.create({ doc, extensions: [json()] }));
  assert.equal(parse('{"nested":{"values":[true,null,42,"text"]}}').toString().includes("⚠"), false);
  assert.equal(parse('{"broken":}').toString().includes("⚠"), true);
});

test("live metadata diagnostics agree with creation validation", () => {
  assert.deepEqual(metadataDiagnostics(""), []);
  assert.deepEqual(metadataDiagnostics('{"nested":[true,null]}'), []);
  for (const raw of ['{broken', '[]', '{"id":9007199254740993}']) {
    const [diagnostic] = metadataDiagnostics(raw);
    assert.equal(diagnostic.from, 0);
    assert.equal(diagnostic.to, raw.length);
    assert.equal(diagnostic.severity, "error");
    assert.ok(diagnostic.message);
  }
});
