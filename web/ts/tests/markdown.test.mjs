// The renderer configuration is tested against the very bundle that ships, so
// this covers the real rendering rather than a copy of it.
//
// It exercises the configuration alone, without DOMPurify, because sanitising
// needs a DOM this test has no way to provide. That is deliberate: the point of
// html:false is that the *parser* never emits attacker-controlled markup in the
// first place, so the escaping below is a real defence rather than something
// the sanitiser has to clean up afterwards.

import assert from "node:assert/strict";
import test from "node:test";

import MarkdownIt from "../../static/vendor/markdown-it-14.1.0.js";
import { createRenderer, markdownOptions, sanitizeConfig } from "../../static/markdown-config.js";

const md = createRenderer(MarkdownIt);

test("renders CommonMark", () => {
  assert.match(md.render("*emphasis*"), /<em>emphasis<\/em>/);
  assert.match(md.render("**strong**"), /<strong>strong<\/strong>/);
  assert.match(md.render("# Heading"), /<h1>Heading<\/h1>/);
  assert.match(md.render("`code`"), /<code>code<\/code>/);
  assert.match(md.render("> quoted"), /<blockquote>/);
});

// The GFM set SPEC §2.4 pins, one test per extension.

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

test("renders CommonMark autolinks", () => {
  assert.match(md.render("<https://example.com/1>"), /<a href="https:\/\/example\.com\/1">/);
});

// The XSS gate. Raw HTML is escaped outright, which is stricter than GFM's
// disallowed-raw-HTML rule and safe in the same direction.
//
// The check is on the tags actually emitted, not on text that merely looks like
// markup: escaped text may well contain the characters "onclick=" without any
// of it being live.

/** The tags the renderer is ever allowed to emit. */
const emittableTags = new Set([
  "p", "br", "hr", "em", "strong", "s", "code", "pre", "blockquote",
  "h1", "h2", "h3", "h4", "h5", "h6",
  "ul", "ol", "li", "a", "img", "input",
  "table", "thead", "tbody", "tr", "th", "td",
]);

/** tagsIn returns the names of the tags a rendered fragment really contains. */
function tagsIn(html) {
  return [...html.matchAll(/<\/?([a-zA-Z][a-zA-Z0-9]*)/g)].map((m) => m[1].toLowerCase());
}

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
// and any attributes it carried are all inert. This is the same rule that makes
// the API's derived links array ignore raw HTML.
//
// One divergence is worth stating rather than hiding: because the tag is
// escaped to *text*, linkify then sees the URL inside it as a bare URL and
// links it. goldmark, which keeps the tag as a raw-HTML node, does not, so a
// description written this way renders a link the links array does not list.
// That is why the issue view shows the links array explicitly.
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
