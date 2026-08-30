import assert from "node:assert/strict";
import test from "node:test";

import { configureSearchBox } from "../../static/search.js";

test("global search does not reuse unrelated browser form history", () => {
  const form = { autocomplete: "" };
  const input = { autocomplete: "", name: "", placeholder: "", type: "" };

  configureSearchBox(form, input);

  assert.equal(form.autocomplete, "off");
  assert.equal(input.autocomplete, "off");
  assert.equal(input.name, "", "the JavaScript-only input needs no form field name");
  assert.equal(input.type, "search");
  assert.equal(input.placeholder, "Search…");
});
