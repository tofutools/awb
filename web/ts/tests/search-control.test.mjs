import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("Chromium's redundant native cancel affordance stays suppressed", async () => {
  const css = await readFile(new URL("../../static/app.css", import.meta.url), "utf8");
  assert.match(css, /\.search-control > input\[type="search"\]::\-webkit-search-cancel-button\s*\{[^}]*-webkit-appearance: none;[^}]*appearance: none;[^}]*\}/);
});
