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


test("syntax diagnostics point at the parse error instead of the whole object", () => {
  const raw = '{"valid": true, "broken": }';
  const tree = syntaxTree(EditorState.create({ doc: raw, extensions: [json()] }));
  const [diagnostic] = metadataDiagnostics(raw, tree);
  assert.ok(diagnostic.from > 0);
  assert.ok(diagnostic.to - diagnostic.from <= 1);
  assert.equal(diagnostic.message, "Metadata must be valid JSON.");

  const array = "[true]";
  const arrayTree = syntaxTree(EditorState.create({ doc: array, extensions: [json()] }));
  assert.deepEqual(metadataDiagnostics(array, arrayTree)[0], {
    from: 0, to: array.length, severity: "error", message: "Metadata must be a JSON object.",
  });
});
