import assert from "node:assert/strict";
import test from "node:test";

import {
  codeBlockEdit,
  headingEdit,
  headingLevel,
  linePrefixEdit,
  linkEdit,
  numberedListEdit,
  ruleEdit,
  tableEdit,
  wrapEdit,
} from "../../static/markdown-toolbar.js";

// The document an edit produces. The changes are non-overlapping and in
// ascending order, which is what lets them be applied back to front.
function applied(doc, edit) {
  let result = doc;
  for (const change of [...edit.changes].reverse()) {
    result = result.slice(0, change.from) + change.insert + result.slice(change.to ?? change.from);
  }
  return result;
}

test("an inline marker wraps the selection, or opens an empty pair", () => {
  const wrapped = wrapEdit("one two", 4, 7, "**");
  assert.equal(applied("one two", wrapped), "one **two**");
  assert.deepEqual(wrapped.selection, { anchor: 11, head: 11 });

  const empty = wrapEdit("one ", 4, 4, "~~");
  assert.equal(applied("one ", empty), "one ~~~~");
  assert.deepEqual(empty.selection, { anchor: 6, head: 6 });
});

test("a line prefix reaches every line the selection touches", () => {
  const doc = "one\ntwo\nthree";
  assert.equal(applied(doc, linePrefixEdit(doc, 5, 9, "- ")), "one\n- two\n- three");
  assert.equal(applied(doc, linePrefixEdit(doc, 0, 0, "> ")), "> one\ntwo\nthree");
  assert.equal(applied(doc, linePrefixEdit(doc, 0, 13, "- [ ] ")), "- [ ] one\n- [ ] two\n- [ ] three");
});

test("a selection ending at a line start stops on the line before it", () => {
  const doc = "one\ntwo";
  assert.equal(applied(doc, linePrefixEdit(doc, 0, 4, "- ")), "- one\ntwo");
  assert.equal(applied(doc, headingEdit(doc, 0, 4, 2)), "## one\ntwo");
  assert.equal(applied(doc, numberedListEdit(doc, 0, 4)), "1. one\ntwo");
  // The newline itself is still part of the first line's selection, so the
  // line the cursor sits on is unaffected by where the range ends.
  assert.equal(applied(doc, linePrefixEdit(doc, 4, 4, "- ")), "one\n- two");
});

test("a numbered list counts from one", () => {
  const doc = "one\ntwo\nthree";
  assert.equal(applied(doc, numberedListEdit(doc, 0, 13)), "1. one\n2. two\n3. three");
});

test("a heading replaces the marker already on the line rather than stacking", () => {
  const doc = "## Title\nbody";
  assert.equal(applied(doc, headingEdit(doc, 0, 0, 1)), "# Title\nbody");
  assert.equal(applied(doc, headingEdit(doc, 0, 0, 0)), "Title\nbody");
  assert.equal(applied("body", headingEdit("body", 0, 0, 3)), "### body");
  assert.equal(applied(doc, headingEdit(doc, 0, 12, 2)), "## Title\n## body");
});

test("the heading level of a line is what the control shows", () => {
  assert.equal(headingLevel("### Title"), 3);
  assert.equal(headingLevel("body"), 0);
  // Three spaces of indentation still leaves a heading; a fourth makes it code.
  assert.equal(headingLevel("   ## Indented"), 2);
  assert.equal(headingLevel("    ## Code"), 0);
  // A line ending right after the marker is an empty heading; seven is none.
  assert.equal(headingLevel("###"), 3);
  assert.equal(headingLevel("####### seven"), 0);
  assert.equal(headingLevel("#no space"), 0);
});

test("a heading keeps the indentation it was written with", () => {
  assert.equal(applied("  ## Title", headingEdit("  ## Title", 0, 0, 1)), "  # Title");
  assert.equal(applied("  ## Title", headingEdit("  ## Title", 0, 0, 0)), "  Title");
  assert.equal(applied("###", headingEdit("###", 0, 0, 0)), "");
});

test("a block construct is padded onto lines of its own", () => {
  assert.equal(applied("text", ruleEdit("text", 4, 4)), "text\n\n---\n");
  assert.equal(applied("text\n\n", ruleEdit("text\n\n", 6, 6)), "text\n\n---\n");
  assert.equal(applied("", ruleEdit("", 0, 0)), "---\n");
  assert.equal(applied("a\n\nb", ruleEdit("a\n\nb", 3, 3)), "a\n\n---\n\nb");
});

test("a table is seeded with its first header label selected", () => {
  const edit = tableEdit("", 0, 0);
  assert.equal(applied("", edit), "| Column 1 | Column 2 |\n| --- | --- |\n| Cell | Cell |\n");
  assert.deepEqual(edit.selection, { anchor: 2, head: 10 });
});

test("a code block fences the selection, or opens with the cursor between the fences", () => {
  const doc = "let x = 1";
  const fenced = codeBlockEdit(doc, 0, 9);
  assert.equal(applied(doc, fenced), "```\nlet x = 1\n```\n");

  const empty = codeBlockEdit("", 0, 0);
  assert.equal(applied("", empty), "```\n\n```\n");
  assert.deepEqual(empty.selection, { anchor: 4, head: 4 });
});

test("a code block outlasts a fence in what it is given", () => {
  // A three-backtick fence would be closed by the selection's own fence line,
  // leaving the rest of it outside the block.
  const fenced = "```\ncode\n```";
  assert.equal(applied(fenced, codeBlockEdit(fenced, 0, 12)), "````\n```\ncode\n```\n````\n");
  // Backticks inside a line of prose close nothing, so they do not lengthen it.
  const inline = "a `code` span";
  assert.equal(applied(inline, codeBlockEdit(inline, 0, 13)), "```\na `code` span\n```\n");
});

test("a link keeps the selection as its text and selects the destination", () => {
  const doc = "the docs";
  const link = linkEdit(doc, 4, 8, false);
  assert.equal(applied(doc, link), "the [docs](https://example.com)");
  assert.deepEqual(link.selection, { anchor: 11, head: 30 });

  const empty = linkEdit("", 0, 0, false);
  assert.equal(applied("", empty), "[text](https://example.com)");
  assert.deepEqual(empty.selection, { anchor: 1, head: 5 });

  const image = linkEdit("", 0, 0, true);
  assert.equal(applied("", image), "![alt](https://example.com)");
  assert.deepEqual(image.selection, { anchor: 2, head: 5 });
});

test("a link label survives the brackets and backslashes it is given", () => {
  const doc = "a]b";
  const link = linkEdit(doc, 0, 3, false);
  assert.equal(applied(doc, link), "[a\\]b](https://example.com)");
  // The destination is still what is selected, measured on the escaped label.
  assert.deepEqual(link.selection, { anchor: 7, head: 26 });

  assert.equal(applied("[x\\", linkEdit("[x\\", 0, 3, false)), "[\\[x\\\\](https://example.com)");
});
