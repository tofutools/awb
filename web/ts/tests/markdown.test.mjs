// The renderer configuration is tested against the very bundle that ships, so
// this covers the real rendering rather than a copy of it.
//
// It exercises the configuration alone, without DOMPurify, because sanitising
// needs a DOM this test has no way to provide. That is deliberate: the point
// of html:false is that the *parser* never emits attacker-controlled markup in
// the first place, so the escaping below is a real defence rather than
// something the sanitiser has to clean up afterwards.

import assert from "node:assert/strict";
import test from "node:test";

import { createRenderer, markdownOptions, sanitizeConfig } from "../../static/markdown-config.js";
import { vendorBundle } from "./vendor.mjs";

const { default: MarkdownIt } = await import(vendorBundle("markdown-it"));

const md = createRenderer(MarkdownIt);

test("renders CommonMark", () => {
  assert.match(md.render("*emphasis*"), /<em>emphasis<\/em>/);
  assert.match(md.render("**strong**"), /<strong>strong<\/strong>/);
  assert.match(md.render("# Heading"), /<h1>Heading<\/h1>/);
  assert.match(md.render("`code`"), /<code>code<\/code>/);
  assert.match(md.render("> quoted"), /<blockquote>/);
});

// The pinned GFM set, one test per extension.

test("renders GFM tables", () => {
  const html = md.render("| a | b |\n| - | - |\n| 1 | 2 |\n");
  assert.match(html, /<table>/);
  assert.match(html, /<th>a<\/th>/);
  assert.match(html, /<td>1<\/td>/);
});

test("renders GFM strikethrough", () => {
  assert.match(md.render("~~gone~~"), /<s>gone<\/s>/);
});

test("renders GFM task lists", () => {
  const html = md.render("- [ ] todo\n- [x] done\n");
  assert.match(html, /<input type="checkbox" disabled>/);
  assert.match(html, /<input type="checkbox" disabled checked>/);
  assert.match(html, /class="task-list-item"/);
  assert.match(html, /todo/);
  assert.doesNotMatch(html, /\[ \]/, "the marker itself is replaced, not left in the text");
});

test("renders GFM autolinks", () => {
  assert.match(
    md.render("see https://example.com/1 for details"),
    /<a href="https:\/\/example\.com\/1">/,
  );
  assert.match(md.render("see www.example.com"), /<a href="http:\/\/www\.example\.com">/);
  assert.match(md.render("mail dev@example.com"), /<a href="mailto:dev@example\.com">/);
});

// Where linkify-it is wider than GFM, pinned so that it is a decision and not a
// surprise. `fuzzyLink` is one switch: it cannot recognise a `www.` host, which
// GFM autolinks, without also recognising a bare one, which GFM does not. So a
// bare host is a link in the prose and absent from the API's `links` array. A
// bare IP address is neither.
test("a bare host is linkified and a bare IP address is not", () => {
  assert.match(md.render("see example.com now"), /<a href="http:\/\/example\.com">/);
  assert.doesNotMatch(md.render("ping 127.0.0.1 now"), /<a /);
});

test("renders CommonMark autolinks", () => {
  assert.match(md.render("<https://example.com/1>"), /<a href="https:\/\/example\.com\/1">/);
});

// The XSS gate. Raw HTML is escaped outright, which is stricter than GFM's
// disallowed-raw-HTML rule and safe in the same direction.
//
// The check is on the tags actually emitted, not on text that merely looks
// like markup: escaped text may well contain the characters "onclick=" without
// any of it being live.

// The tags the renderer is ever allowed to emit, read from the sanitiser's own
// allow-list rather than copied beside it. The two lists are one list: a tag
// the renderer emits and the sanitiser does not allow is deleted from what the
// reader sees, and a second copy here could only hide that by agreeing with the
// renderer while the sanitiser disagreed.
const emittableTags = new Set(sanitizeConfig.ALLOWED_TAGS);

/** tagsIn returns the names of the tags a rendered fragment really contains. */
function tagsIn(html) {
  return [...html.matchAll(/<\/?([a-zA-Z][a-zA-Z0-9]*)/g)].map((m) => m[1].toLowerCase());
}

// The gate read the other way. Above, nothing the renderer emits may fall
// outside the allow-list; here, nothing the pinned dialect renders may be
// missing from it. Only the first direction fails loudly — a tag the sanitiser
// does not allow is deleted, so the reader loses the markup and no test
// notices. Strikethrough was exactly that: the renderer emitted `<s>` and the
// allow-list named `del`, so `~~x~~` reached the page as plain text.
test("every tag the pinned dialect renders survives the allow-list", () => {
  const document = [
    "# h1", "## h2", "### h3", "#### h4", "##### h5", "###### h6",
    "*em* **strong** ~~struck~~ `code`",
    "a line\\\nbroken",
    "---",
    "> quoted",
    "- bullet", "1. numbered", "- [x] done", "- [ ] todo",
    "| a | b |\n| - | - |\n| 1 | 2 |",
    "```\nfenced\n```",
    "[link](https://example.com/1) ![image](https://example.com/i.png)",
  ].join("\n\n");

  const emitted = new Set(tagsIn(md.render(document)));
  assert.ok(emitted.has("s"), "the document exercises strikethrough");
  for (const tag of emitted) {
    assert.ok(emittableTags.has(tag), `<${tag}> is rendered but the sanitiser strips it`);
  }
});

test("emits no tag outside the allowed set", () => {
  const attacks = [
    "<script>alert(1)</script>",
    "<img src=x onerror=alert(1)>",
    "<iframe src='https://evil.example.com'></iframe>",
    '<a href="https://ok.example.com" onclick="alert(1)">text</a>',
    "<svg><use href='#x' /></svg>",
    "<style>body{display:none}</style>",
    "<object data='x'></object>",
    "<form action='https://evil.example.com'><input name=a></form>",
    "<math><mi//xlink:href=\"data:x,<script>alert(1)</script>\">",
    "<!-- <img src=x onerror=alert(1)> -->",
  ];

  for (const attack of attacks) {
    const html = md.render(attack);
    for (const tag of tagsIn(html)) {
      assert.ok(emittableTags.has(tag), `${attack} emitted a <${tag}>`);
    }
    assert.match(html, /&lt;/, `${attack} was not escaped`);
  }
});

test("emits no event-handler attribute", () => {
  // Attributes are read from the tags themselves, so escaped text saying
  // "onclick=" does not count against this.
  const html = md.render('<img src=x onerror=alert(1)><a onclick="alert(1)">x</a>');
  for (const tag of html.matchAll(/<[a-zA-Z][^>]*>/g)) {
    assert.doesNotMatch(tag[0], /\son\w+\s*=/i, `live event handler in ${tag[0]}`);
  }
});

test("does not linkify a javascript: URL into an anchor", () => {
  const html = md.render("[click](javascript:alert(1))");
  assert.doesNotMatch(html, /href="javascript:/i);
});

// A hand-written anchor is escaped rather than honoured, so its href, its text
// and any attributes it carried are all inert. This is the same rule that
// makes the API's derived links array ignore raw HTML.
//
// One divergence is worth stating rather than hiding: because the tag is
// escaped to *text*, linkify then sees the URL inside it as a bare URL and
// links it. goldmark, which keeps the tag as a raw-HTML node, does not, so a
// description written this way renders a link the links array does not list.
test("a hand-written anchor is escaped, not honoured", () => {
  const html = md.render('<a href="https://example.com/1" onclick="alert(1)">raw</a>');

  assert.match(html, /&lt;a href=/, "the tag itself is escaped");
  assert.doesNotMatch(html, /<a[^>]*onclick/i, "its attributes are not honoured");
  assert.match(html, /raw/, "its text survives as text");
  assert.equal(markdownOptions.html, false);
});

test("the pinned options are what the renderer is built with", () => {
  assert.equal(markdownOptions.html, false, "raw HTML is escaped");
  assert.equal(markdownOptions.linkify, true, "the autolink extension is on");
  assert.equal(markdownOptions.breaks, false, "GFM does not turn a single newline into a break");
  assert.equal(markdownOptions.typographer, false, "nothing beyond the pinned set");
});

test("the sanitiser allow-lists no scripting surface", () => {
  for (const tag of ["script", "iframe", "object", "embed", "style", "form", "base", "link"]) {
    assert.ok(!sanitizeConfig.ALLOWED_TAGS.includes(tag), `${tag} must not be allowed`);
  }
  for (const attr of sanitizeConfig.ALLOWED_ATTR) {
    assert.doesNotMatch(attr, /^on/i, `${attr} looks like an event handler`);
  }
  assert.equal(sanitizeConfig.ALLOW_DATA_ATTR, false);

  // The tags a description legitimately renders to are all present.
  for (const tag of ["p", "a", "code", "pre", "table", "ul", "li", "input"]) {
    assert.ok(sanitizeConfig.ALLOWED_TAGS.includes(tag), `${tag} must be allowed`);
  }
});

test("rendering is deterministic", () => {
  const source = "# Title\n\n- [x] done\n- [ ] todo\n\nSee https://example.com/1 and **this**.\n";
  const first = md.render(source);
  for (let i = 0; i < 5; i++) {
    assert.equal(md.render(source), first);
  }
});

// Two denial-of-service advisories, both remediated by the bundled markdown-it
// release and each reached through a different part of linkify. A description
// or comment is prose one workspace member writes and every other member's
// browser renders, so either is a stored denial of service rather than
// something a caller only does to itself.
//
// Both inputs are deliberately larger than the 64 KiB a single description or
// comment may hold: one issue view renders the description and every comment on
// it, so what a page feeds the renderer is the sum, not the cap. The bound is
// wall-clock and the same for both, and generous — each takes on the order of a
// hundred milliseconds here and tens of seconds on a vulnerable bundle, so the
// two are nowhere near close enough for a loaded CI runner to confuse them.
const redosBudgetMs = 2000;

/** renderMs renders source and returns how long that took, in milliseconds. */
function renderMs(source) {
  const started = process.hrtime.bigint();
  md.render(source);
  return Number(process.hrtime.bigint() - started) / 1e6;
}

// GHSA-38c4-r59v-3vqw / CVE-2026-2327, markdown-it >=13.0.0 <14.1.1: the
// linkify rule trimmed trailing asterisks off a matched link with /\*+$/, which
// backtracks catastrophically on a long run of them ending in anything else.
//
// The asterisks have to follow something linkify actually matched — a bare run
// of them is never handed to that regular expression at all, and costs nothing.
test("a long asterisk run after a link renders in linear time", () => {
  const elapsedMs = renderMs("https://example.com/" + "*".repeat(256 * 1024) + "x");
  assert.ok(elapsedMs < redosBudgetMs, `rendering a 256 KiB asterisk run took ${elapsedMs.toFixed(0)} ms`);
});

// GHSA-v245-v573-v5vm / CVE-2026-59887, linkify-it <=5.0.1, which markdown-it
// 14 depended on: the `mailto:` validator copied and rescanned the rest of the
// text at every occurrence, so a run of them costs time quadratic in its length.
test("a run of mailto prefixes renders in linear time", () => {
  const elapsedMs = renderMs("mailto::".repeat(32 * 1024)); // 256 KiB
  assert.ok(elapsedMs < redosBudgetMs, `rendering 256 KiB of mailto prefixes took ${elapsedMs.toFixed(0)} ms`);
});
