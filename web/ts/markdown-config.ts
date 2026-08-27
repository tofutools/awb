// The renderer configuration, kept free of bare import specifiers so that the
// frontend tests can exercise the very configuration that ships, rather than a
// copy of it. web/ts/markdown.ts wires it to the vendored bundles.
//
// The dialect is pinned to GitHub Flavored Markdown — CommonMark plus GFM's
// tables, task lists, strikethrough, autolink extension and
// disallowed-raw-HTML rule, and nothing beyond that — and requires this
// renderer to be configured to that same set, because `links` is a specified
// output and what the UI shows as a link must be what `links` lists.

/** The markdown-it options that match the pinned GFM set. */
export const markdownOptions = {
  // Raw HTML is escaped outright, which is stricter than GFM's
  // disallowed-raw-HTML rule and safe in the same direction. It is also why an
  // <a href=...> written by hand is not a link here, matching what the API's
  // derived `links` array does with it.
  html: false,
  // The autolink extension. Tables and strikethrough are markdown-it defaults.
  linkify: true,
  breaks: false,
  typographer: false,
};

/** The tags and attributes a rendered description may contain. */
export const sanitizeConfig = {
  ALLOWED_TAGS: [
    "p", "br", "hr", "em", "strong", "del", "code", "pre", "blockquote",
    "h1", "h2", "h3", "h4", "h5", "h6",
    "ul", "ol", "li", "a", "img", "input",
    "table", "thead", "tbody", "tr", "th", "td",
  ],
  ALLOWED_ATTR: [
    "href", "title", "src", "alt", "align", "class", "type", "checked", "disabled",
  ],
  ALLOW_DATA_ATTR: false,
};

// The shapes below are the parts of markdown-it this file touches. They are
// declared structurally so this module needs no import at all.

interface Token {
  type: string;
  content: string;
  children: Token[] | null;
  attrJoin(name: string, value: string): void;
}

interface StateCore {
  tokens: Token[];
  Token: new (type: string, tag: string, nesting: number) => Token;
}

interface Markdown {
  core: { ruler: { push(name: string, fn: (state: StateCore) => void): void } };
  render(src: string): string;
}

/** A markdown-it constructor, however it was imported. */
export type MarkdownConstructor = new (options: typeof markdownOptions) => Markdown;

/**
 * createRenderer builds the configured markdown-it instance. It is the single
 * definition of how awb renders a description.
 */
export function createRenderer(MarkdownIt: MarkdownConstructor): Markdown {
  const md = new MarkdownIt(markdownOptions);
  md.core.ruler.push("awb_task_lists", taskLists);
  return md;
}

const taskMarker = /^\[([ xX])\]\s+/;

/**
 * taskLists turns a list item beginning with "[ ] " or "[x] " into a disabled
 * checkbox, which is what GFM's task list extension renders. markdown-it has no
 * task lists of its own, so this is what completes the pinned set.
 */
export function taskLists(state: StateCore): void {
  const tokens = state.tokens;
  for (let i = 2; i < tokens.length; i++) {
    if (!isInlineInListItem(tokens, i)) continue;

    const token = tokens[i];
    const marker = taskMarker.exec(token.content);
    if (marker === null) continue;

    const checked = marker[1] !== " ";
    token.content = token.content.slice(marker[0].length);

    const children = token.children;
    if (children !== null && children.length > 0 && children[0].type === "text") {
      children[0].content = children[0].content.replace(taskMarker, "");
    }

    // The checkbox is added as a token; the sanitiser is still what decides it
    // is allowed through, so nothing here bypasses that gate.
    const checkbox = new state.Token("html_inline", "", 0);
    checkbox.content = `<input type="checkbox" disabled${checked ? " checked" : ""}> `;
    children?.unshift(checkbox);

    // Mark the enclosing list item so the stylesheet can drop its bullet. That
    // is tokens[i-2]: tokens[i-1] is the paragraph, which markdown-it marks
    // hidden in a tight list and therefore never renders.
    tokens[i - 2].attrJoin("class", "task-list-item");
  }
}

function isInlineInListItem(tokens: Token[], i: number): boolean {
  return (
    tokens[i].type === "inline" &&
    tokens[i - 1].type === "paragraph_open" &&
    tokens[i - 2].type === "list_item_open"
  );
}
