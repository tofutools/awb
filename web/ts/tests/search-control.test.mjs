import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { attachSearchClear } from "../../static/search-control.js";

class FakeElement extends EventTarget {
  value = "";
  hidden = false;
  focused = false;
  attributes = new Map();

  setAttribute(name, value) { this.attributes.set(name, value); }
  focus() { this.focused = true; }
  click() { this.dispatchEvent(new Event("click")); }
}

test("the shared search clear is named, hidden when empty, and clears with focus", () => {
  const originalDocument = globalThis.document;
  globalThis.document = { createElement: () => new FakeElement() };
  try {
    const input = new FakeElement();
    input.value = "backlog";
    let cleared = 0;
    const control = attachSearchClear(input, () => { cleared += 1; });

    assert.equal(input.type, "search");
    assert.equal(control.button.attributes.get("aria-label"), "Clear search");
    assert.equal(control.button.hidden, false);
    control.button.click();
    assert.equal(input.value, "");
    assert.equal(control.button.hidden, true);
    assert.equal(input.focused, true);
    assert.equal(cleared, 1);
  } finally {
    globalThis.document = originalDocument;
  }
});

test("typing toggles the clear action and default clearing emits an input event", () => {
  const originalDocument = globalThis.document;
  globalThis.document = { createElement: () => new FakeElement() };
  try {
    const input = new FakeElement();
    const control = attachSearchClear(input);
    assert.equal(control.button.hidden, true);

    input.value = "ready";
    input.dispatchEvent(new Event("input"));
    assert.equal(control.button.hidden, false);
    let inputs = 0;
    input.addEventListener("input", () => { inputs += 1; });
    control.button.click();
    assert.equal(inputs, 1);
    assert.equal(control.button.hidden, true);
  } finally {
    globalThis.document = originalDocument;
  }
});

test("Chromium's redundant native cancel affordance stays suppressed", async () => {
  const css = await readFile(new URL("../../static/app.css", import.meta.url), "utf8");
  assert.match(css, /\.search-control > input\[type="search"\]::\-webkit-search-cancel-button\s*\{[^}]*-webkit-appearance: none;[^}]*appearance: none;[^}]*\}/);
});
