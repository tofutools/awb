import assert from "node:assert/strict";
import test from "node:test";

import {
  MAX_IMAGE_PREVIEW,
  MAX_TEXT_PREVIEW,
  attachmentContentHref,
  imageType,
  previewKind,
  withoutFrontmatter,
} from "../../static/attachment-preview.js";

import { createRenderer } from "../../static/markdown-config.js";
import { vendorBundle } from "./vendor.mjs";

const file = (name, content_type, size = 100) => ({ name, content_type, size });

test("images preview by type or, when the type was sniffed, by extension", () => {
  assert.equal(previewKind(file("shot.png", "image/png")), "image");
  assert.equal(previewKind(file("photo", "image/jpeg")), "image");
  assert.equal(previewKind(file("diagram.svg", "text/xml; charset=utf-8")), "image");
  assert.equal(imageType(file("diagram.svg", "text/xml; charset=utf-8")), "image/svg+xml");
  assert.equal(imageType(file("SHOT.PNG", "application/octet-stream")), "image/png");
});

test("Markdown is recognised by type or extension, ahead of plain text", () => {
  assert.equal(previewKind(file("notes.md", "text/plain; charset=utf-8")), "markdown");
  assert.equal(previewKind(file("README.markdown", "application/octet-stream")), "markdown");
  assert.equal(previewKind(file("notes", "text/markdown")), "markdown");
});

test("other text previews as text", () => {
  assert.equal(previewKind(file("trace.txt", "text/plain; charset=utf-8")), "text");
  assert.equal(previewKind(file("data", "application/json")), "text");
  assert.equal(previewKind(file("config.yaml", "application/octet-stream")), "text");
  assert.equal(previewKind(file("page.html", "text/html; charset=utf-8")), "text");
});

test("binary and oversized files stay downloads only", () => {
  assert.equal(previewKind(file("archive.zip", "application/zip")), undefined);
  assert.equal(previewKind(file("blob", "application/octet-stream")), undefined);
  assert.equal(previewKind(file("big.log", "text/plain", MAX_TEXT_PREVIEW + 1)), undefined);
  assert.equal(previewKind(file("big.png", "image/png", MAX_IMAGE_PREVIEW + 1)), undefined);
  assert.equal(previewKind(file("big.svg", "image/svg+xml", MAX_TEXT_PREVIEW + 1)), undefined);
  assert.equal(imageType(file("trace.txt", "text/plain")), undefined);
});

test("content is addressed by issue and encoded name", () => {
  assert.equal(
    attachmentContentHref("awb-1a2b", "a b/c.md"),
    "api/issues/awb-1a2b/attachments/a%20b%2Fc.md/content",
  );
});

const { default: MarkdownIt } = await import(vendorBundle("markdown-it"));
const md = createRenderer(MarkdownIt);

test("frontmatter is hidden while Markdown content renders", () => {
  for (const metadata of [
    '---\ntitle: Hidden\n---\n',
    '---\ntitle: Hidden\n...\n',
    '+++\ntitle = "Hidden"\n+++\n',
    ';;;\n{"title":"Hidden"}\n;;;\n',
    '{"title":"Hidden", "nested":{"brace":"}\\\"{"}}\n',
    '{}\n',
  ]) {
    for (const newline of ["\n", "\r\n"]) {
      const source = metadata.replaceAll("\n", newline) + "# Visible";
      assert.equal(md.render(withoutFrontmatter(source)), "<h1>Visible</h1>\n", source);
      assert.equal(md.render(withoutFrontmatter("\uFEFF" + source)), "<h1>Visible</h1>\n");
    }
    assert.equal(md.render(withoutFrontmatter(metadata.trimEnd())), "");
  }
});

test("incomplete metadata and markers within Markdown stay visible", () => {
  for (const source of [
    "---\ntitle: Hidden", "+++\ntitle = 1", ';;;\n{}',
    '{"title": "unclosed"', '{not JSON}\n# Body',
    '{} ordinary prose', "Text\n\n---\nbody\n---",
    "```\n---\ntitle: example\n---\n```",
    "---\nbody\n+++", "\n---\nbody\n---",
  ]) {
    assert.equal(withoutFrontmatter(source), source);
  }
});
