// Rendering an issue description: the configuration of markdown-config.ts
// wired to the committed vendor bundles, with everything sanitised on the way
// out.
//
// The API returns the description exactly as stored, so rendering it — and
// sanitising it — is this client's job.
//
// One caveat worth stating plainly. markdown-it's linkify-it and goldmark's
// GFM autolink extension are separate implementations of the same algorithm,
// so at the margin they can disagree about where a bare URL ends. That is why
// the issue view also renders the API's derived `links` array explicitly: the
// authoritative list is always on screen, whatever the prose rendering does.

import MarkdownIt from "markdown-it";
import DOMPurify from "dompurify";

import { createRenderer, sanitizeConfig, type MarkdownConstructor } from "./markdown-config.js";

const md = createRenderer(MarkdownIt as unknown as MarkdownConstructor);

// A description may link anywhere, so its links open in a new tab and are told
// not to hand the opener over.
DOMPurify.addHook("afterSanitizeAttributes", (node: Element) => {
  if (node.tagName === "A" && node.hasAttribute("href")) {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

/** renderMarkdown renders a description to sanitised HTML. */
export function renderMarkdown(source: string): string {
  if (source === "") return "";
  return DOMPurify.sanitize(md.render(source), sanitizeConfig);
}
