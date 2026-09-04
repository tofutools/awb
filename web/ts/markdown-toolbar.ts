// The formatting toolbar above a Markdown editor, and the syntax reference it
// opens.
//
// Both are pinned to the one dialect awb accepts — CommonMark plus GFM's
// tables, task lists, strikethrough and autolinks — because that is what
// markdown-config.ts renders and what internal/domain's gate parses. The gate
// is narrower than the syntax: it refuses raw HTML outright and accepts only
// http, https and mailto destinations. So no button here composes a construct
// the gate would refuse, and the help is where the two differ is written down.
// The prose a button wraps stays the user's own, and so does keeping it valid.
//
// What a button does is a pure function of the document text and the
// selection, computed here and handed to the editor to apply. That keeps the
// behaviour testable without driving CodeMirror, which is what
// web/ts/tests/markdown-toolbar.test.mjs pins down.

/** A single replacement. A missing `to` inserts rather than replaces. */
export interface DocumentChange {
  readonly from: number;
  readonly to?: number;
  readonly insert: string;
}

/** An edit to apply. A missing selection leaves the editor to map the current
 * one through the changes, which is what the line-prefix edits want. */
export interface MarkdownEdit {
  readonly changes: readonly DocumentChange[];
  readonly selection?: { readonly anchor: number; readonly head: number };
}

/** The editor a toolbar drives, so the toolbar needs nothing of CodeMirror. */
export interface EditorHost {
  doc(): string;
  selection(): { from: number; to: number };
  apply(edit: MarkdownEdit): void;
}

export interface MarkdownToolbar {
  readonly element: HTMLElement;
  /** sync refreshes the controls that mirror where the cursor is. */
  sync(): void;
}

const headingMarker = /^#{1,6}[ \t]+/;

function lineStart(doc: string, pos: number): number {
  return doc.lastIndexOf("\n", pos - 1) + 1;
}

function lineEnd(doc: string, pos: number): number {
  const next = doc.indexOf("\n", pos);
  return next === -1 ? doc.length : next;
}

/** lineStarts lists the start of every line the range from..to touches. An
 * empty selection touches exactly the line the cursor sits on, and one ending
 * at a line start stops on the line before it: the line after the last
 * selected character is not one the user selected. */
function lineStarts(doc: string, from: number, to: number): number[] {
  const first = lineStart(doc, from);
  const last = lineEnd(doc, to > from ? to - 1 : to);
  const starts = [first];
  for (let i = first; i < last; i++) {
    if (doc[i] === "\n") starts.push(i + 1);
  }
  return starts;
}

/** blockPadding is the blank lines a block construct needs around it to be
 * parsed as one: a fence, a table or a thematic break directly under a
 * paragraph is not a block of its own, and `---` under one is a setext
 * heading rather than a rule. */
function blockPadding(doc: string, from: number, to: number): { prefix: string; suffix: string } {
  const before = doc.slice(0, from);
  const after = doc.slice(to);
  let prefix = "";
  if (before.length > 0) prefix = before.endsWith("\n\n") ? "" : before.endsWith("\n") ? "\n" : "\n\n";
  let suffix = "\n\n";
  if (after.length === 0) suffix = "\n";
  else if (after.startsWith("\n\n")) suffix = "";
  else if (after.startsWith("\n")) suffix = "\n";
  return { prefix, suffix };
}

function cursor(at: number): { anchor: number; head: number } {
  return { anchor: at, head: at };
}

function range(from: number, to: number): { anchor: number; head: number } {
  return { anchor: from, head: to };
}

/** wrapEdit surrounds the selection with an inline marker, or opens an empty
 * pair with the cursor inside it when there is no selection. */
export function wrapEdit(doc: string, from: number, to: number, marker: string): MarkdownEdit {
  if (from === to) {
    return { changes: [{ from, insert: `${marker}${marker}` }], selection: cursor(from + marker.length) };
  }
  const insert = `${marker}${doc.slice(from, to)}${marker}`;
  return { changes: [{ from, to, insert }], selection: cursor(from + insert.length) };
}

/** linePrefixEdit puts a marker in front of every line the selection touches,
 * which is how a bullet list, a task list and a blockquote are written. */
export function linePrefixEdit(doc: string, from: number, to: number, prefix: string): MarkdownEdit {
  return { changes: lineStarts(doc, from, to).map((start) => ({ from: start, insert: prefix })) };
}

/** numberedListEdit numbers the lines the selection touches from one. */
export function numberedListEdit(doc: string, from: number, to: number): MarkdownEdit {
  return {
    changes: lineStarts(doc, from, to).map((start, index) => ({ from: start, insert: `${index + 1}. ` })),
  };
}

/** headingEdit sets — or with level 0 clears — the ATX marker on every line
 * the selection touches. Any marker already there is replaced rather than
 * added to, so changing level does not stack `#`s. */
export function headingEdit(doc: string, from: number, to: number, level: number): MarkdownEdit {
  const insert = level > 0 ? `${"#".repeat(level)} ` : "";
  return {
    changes: lineStarts(doc, from, to).map((start) => {
      const existing = headingMarker.exec(doc.slice(start, lineEnd(doc, start)));
      return { from: start, to: start + (existing === null ? 0 : existing[0].length), insert };
    }),
  };
}

/** headingLevelAt reports the heading level of the line holding pos, or 0 when
 * it is not a heading. It is what the toolbar's heading control displays. */
export function headingLevelAt(doc: string, pos: number): number {
  const start = lineStart(doc, pos);
  const existing = headingMarker.exec(doc.slice(start, lineEnd(doc, start)));
  return existing === null ? 0 : existing[0].trimEnd().length;
}

/** tableEdit seeds a GFM table and selects its first header label so it can be
 * typed over. */
export function tableEdit(doc: string, from: number, to: number): MarkdownEdit {
  const { prefix, suffix } = blockPadding(doc, from, to);
  const header = "Column 1";
  const body = `| ${header} | Column 2 |\n| --- | --- |\n| Cell | Cell |`;
  const headerStart = from + prefix.length + 2;
  return {
    changes: [{ from, to, insert: `${prefix}${body}${suffix}` }],
    selection: range(headerStart, headerStart + header.length),
  };
}

/** closingFenceLength is the longest fence in text that would close a code
 * block: a line of nothing but backticks, indented by no more than three
 * spaces. A run inside a line of prose closes nothing and does not count. */
function closingFenceLength(text: string): number {
  let longest = 0;
  for (const line of text.split("\n")) {
    const fence = /^ {0,3}(`+)[ \t]*$/.exec(line);
    if (fence !== null) longest = Math.max(longest, fence[1].length);
  }
  return longest;
}

/** codeBlockEdit fences the selection, or opens an empty fence with the cursor
 * on the line between the fences. The fence is longer than any fence in the
 * selection, since a shorter one would be closed by the very content it is
 * there to hold — which would leave what follows it outside the block, where
 * the gate sees it as prose rather than as code. */
export function codeBlockEdit(doc: string, from: number, to: number): MarkdownEdit {
  const selected = doc.slice(from, to);
  const { prefix, suffix } = blockPadding(doc, from, to);
  const fence = "`".repeat(Math.max(3, closingFenceLength(selected) + 1));
  const insert = `${prefix}${fence}\n${selected}\n${fence}${suffix}`;
  const selection = selected === ""
    ? cursor(from + prefix.length + fence.length + 1) // past the opening fence
    : cursor(from + insert.length);
  return { changes: [{ from, to, insert }], selection };
}

/** ruleEdit inserts a thematic break on a line of its own. */
export function ruleEdit(doc: string, from: number, to: number): MarkdownEdit {
  const { prefix, suffix } = blockPadding(doc, from, to);
  const insert = `${prefix}---${suffix}`;
  return { changes: [{ from, to, insert }], selection: cursor(from + insert.length) };
}

/** escapeLinkLabel makes text safe to carry as a link label. A label ends at
 * its first unescaped `]`, and a backslash escapes whatever follows it, so a
 * selection holding either would otherwise cut the link short. */
function escapeLinkLabel(text: string): string {
  return text.replace(/[\\[\]]/g, "\\$&");
}

/** linkEdit writes a link, or an image with `image` set. The selection becomes
 * the link text and the destination is seeded and selected so it can be typed
 * over; with nothing selected the text placeholder is the one selected. */
export function linkEdit(doc: string, from: number, to: number, image: boolean): MarkdownEdit {
  const selected = doc.slice(from, to);
  const bang = image ? "!" : "";
  const text = selected === "" ? (image ? "alt" : "text") : escapeLinkLabel(selected);
  const destination = "https://example.com";
  const insert = `${bang}[${text}](${destination})`;
  const textStart = from + bang.length + 1;
  const selection = selected === ""
    ? range(textStart, textStart + text.length)
    : range(textStart + text.length + 2, textStart + text.length + 2 + destination.length);
  return { changes: [{ from, to, insert }], selection };
}

type IconName =
  | "bold" | "italic" | "code" | "strikethrough"
  | "bullet-list" | "numbered-list" | "task-list"
  | "quote" | "code-block" | "table" | "rule"
  | "link" | "image" | "help";

// Drawn on the same 24-unit stroked grid as the interface icons in app.ts, so
// the toolbar sits in the same visual family without a second asset pipeline.
const iconPaths: Record<IconName, string> = {
  "bold": '<path d="M7 5h6a3.5 3.5 0 0 1 0 7H7z"></path><path d="M7 12h7a3.5 3.5 0 0 1 0 7H7z"></path>',
  "italic": '<path d="M15 5h-5M14 19H9M13 5l-2 14"></path>',
  "code": '<path d="m15 8 4 4-4 4M9 8l-4 4 4 4"></path>',
  "strikethrough": '<path d="M16 6a4 4 0 0 0-4-2c-2.2 0-4 1.2-4 3 0 1.3.9 2.2 2.4 2.8"></path><path d="M13.5 14c1.5.6 2.5 1.4 2.5 2.8 0 1.8-1.8 3.2-4 3.2a4 4 0 0 1-4-2"></path><path d="M4 12h16"></path>',
  "bullet-list": '<path d="M9 6h11M9 12h11M9 18h11"></path><path d="M5 6h.01M5 12h.01M5 18h.01"></path>',
  "numbered-list": '<path d="M10 6h10M10 12h10M10 18h10"></path><path d="M4 4.5 5.5 4v4"></path><path d="M4 11h2l-2 3h2"></path><path d="M4 17h2v3H4"></path>',
  "task-list": '<path d="M11 6h9M11 12h9M11 18h9"></path><path d="m4 6 1.5 1.5L8 5"></path><path d="m4 17 1.5 1.5L8 15"></path>',
  "quote": '<path d="M6 5v14"></path><path d="M11 8h9M11 12h9M11 16h6"></path>',
  "code-block": '<rect x="3" y="4" width="18" height="16" rx="2"></rect><path d="m10 10-2 2 2 2M14 14l2-2-2-2"></path>',
  "table": '<rect x="3" y="4" width="18" height="16" rx="2"></rect><path d="M3 10h18M9 10v10"></path>',
  "rule": '<path d="M4 12h16"></path>',
  "link": '<path d="M10 13a5 5 0 0 0 7.1 0l2-2A5 5 0 0 0 12 3.9L10.9 5"></path><path d="M14 11a5 5 0 0 0-7.1 0l-2 2A5 5 0 0 0 12 20.1l1.1-1.1"></path>',
  "image": '<rect x="3" y="4" width="18" height="16" rx="2"></rect><circle cx="9" cy="10" r="1.5"></circle><path d="m5 18 5-5 4 4 2-2 3 3"></path>',
  "help": '<circle cx="12" cy="12" r="9"></circle><path d="M9.6 9.5a2.5 2.5 0 1 1 3.4 2.3c-.6.3-1 .8-1 1.4v.3"></path><path d="M12 17h.01"></path>',
};

function toolbarIcon(name: IconName): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("icon");
  svg.innerHTML = iconPaths[name];
  return svg;
}

function toolbarButton(label: string, icon: IconName, run: () => void): HTMLButtonElement {
  const control = document.createElement("button");
  control.type = "button";
  control.className = "markdown-toolbar-button";
  control.title = label;
  control.setAttribute("aria-label", label);
  control.append(toolbarIcon(icon));
  // A toolbar button must not take the focus off the document it edits: the
  // selection it acts on is the one the editor still holds.
  control.addEventListener("mousedown", (event) => event.preventDefault());
  control.addEventListener("click", run);
  return control;
}

function separator(): HTMLElement {
  const line = document.createElement("span");
  line.className = "markdown-toolbar-separator";
  line.setAttribute("role", "separator");
  line.setAttribute("aria-orientation", "vertical");
  return line;
}

export function createMarkdownToolbar(host: EditorHost): MarkdownToolbar {
  const element = document.createElement("div");
  element.className = "markdown-toolbar";
  // A group rather than role="toolbar": that role promises arrow-key
  // navigation between the controls, and every control here is in the tab
  // order instead.
  element.setAttribute("role", "group");
  element.setAttribute("aria-label", "Markdown formatting");

  const edit = (build: (doc: string, from: number, to: number) => MarkdownEdit) => () => {
    const { from, to } = host.selection();
    host.apply(build(host.doc(), from, to));
  };

  const heading = document.createElement("select");
  heading.className = "markdown-toolbar-heading";
  heading.title = "Heading level";
  heading.setAttribute("aria-label", "Heading level");
  for (const level of [0, 1, 2, 3, 4, 5, 6]) {
    const option = document.createElement("option");
    option.value = String(level);
    option.textContent = level === 0 ? "Normal" : `Heading ${level}`;
    heading.append(option);
  }
  heading.addEventListener("change", () => {
    const level = Number(heading.value);
    const { from, to } = host.selection();
    host.apply(headingEdit(host.doc(), from, to, level));
  });

  element.append(
    heading,
    separator(),
    toolbarButton("Bold", "bold", edit((doc, from, to) => wrapEdit(doc, from, to, "**"))),
    toolbarButton("Italic", "italic", edit((doc, from, to) => wrapEdit(doc, from, to, "*"))),
    toolbarButton("Code", "code", edit((doc, from, to) => wrapEdit(doc, from, to, "`"))),
    toolbarButton("Strikethrough", "strikethrough", edit((doc, from, to) => wrapEdit(doc, from, to, "~~"))),
    separator(),
    toolbarButton("Bullet list", "bullet-list", edit((doc, from, to) => linePrefixEdit(doc, from, to, "- "))),
    toolbarButton("Numbered list", "numbered-list", edit(numberedListEdit)),
    toolbarButton("Task list", "task-list", edit((doc, from, to) => linePrefixEdit(doc, from, to, "- [ ] "))),
    separator(),
    toolbarButton("Blockquote", "quote", edit((doc, from, to) => linePrefixEdit(doc, from, to, "> "))),
    toolbarButton("Code block", "code-block", edit(codeBlockEdit)),
    toolbarButton("Table", "table", edit(tableEdit)),
    toolbarButton("Horizontal rule", "rule", edit(ruleEdit)),
    separator(),
    toolbarButton("Link", "link", edit((doc, from, to) => linkEdit(doc, from, to, false))),
    toolbarButton("Image", "image", edit((doc, from, to) => linkEdit(doc, from, to, true))),
  );

  const spacer = document.createElement("span");
  spacer.className = "markdown-toolbar-spacer";
  const help = toolbarButton("Markdown help", "help", () => openMarkdownHelp(help));
  help.setAttribute("aria-haspopup", "dialog");
  element.append(spacer, help);

  return {
    element,
    sync(): void {
      heading.value = String(headingLevelAt(host.doc(), host.selection().from));
    },
  };
}

interface HelpRow {
  readonly syntax: string;
  readonly description: string;
}

interface HelpSection {
  readonly title: string;
  readonly rows: readonly HelpRow[];
}

// The reference is the dialect and nothing else. It follows the toolbar's
// order so a reader can go from a button to the syntax behind it.
const helpSections: readonly HelpSection[] = [
  {
    title: "Headings",
    rows: [
      { syntax: "# Heading 1", description: "Top-level heading" },
      { syntax: "## Heading 2", description: "Sub-heading, down to ###### for level 6" },
    ],
  },
  {
    title: "Text",
    rows: [
      { syntax: "**bold**", description: "Bold" },
      { syntax: "*italic*", description: "Italic" },
      { syntax: "`code`", description: "Inline code" },
      { syntax: "~~struck~~", description: "Strikethrough" },
      { syntax: "\\*literal\\*", description: "A backslash escapes a marker so it reads as itself" },
    ],
  },
  {
    title: "Lists",
    rows: [
      { syntax: "- Item", description: "Bullet list" },
      { syntax: "1. Item", description: "Numbered list" },
      { syntax: "- [ ] To do", description: "Task list, unchecked" },
      { syntax: "- [x] Done", description: "Task list, checked" },
      { syntax: "- Item\n  - Nested", description: "Indent by two spaces to nest" },
    ],
  },
  {
    title: "Blocks",
    rows: [
      { syntax: "> Quoted", description: "Blockquote" },
      { syntax: "```\ncode\n```", description: "Fenced code block" },
      { syntax: "| A | B |\n| --- | --- |\n| 1 | 2 |", description: "Table" },
      { syntax: "| :-- | :-: | --: |", description: "A colon in the divider aligns that column" },
      { syntax: "---", description: "Horizontal rule" },
    ],
  },
  {
    title: "Links & images",
    rows: [
      { syntax: "[text](https://example.com)", description: "Link" },
      { syntax: "https://example.com", description: "A bare URL becomes a link" },
      { syntax: "[mail](mailto:a@example.com)", description: "A link may be http, https or mailto, and nothing else" },
      { syntax: "![alt](https://example.com/a.png)", description: "Image, over http or https" },
    ],
  },
];

const helpNote = "Raw HTML is refused rather than escaped, so a tag written by hand — <b>, <script>, an HTML comment, or a bare <word> — has to be written as `<word>` or \\<word>.";

/** openMarkdownHelp shows the syntax reference. A fresh dialog per opening
 * ties its lifetime to the button that asked for it and hands the focus back
 * there, as the confirmation dialogs do. */
export function openMarkdownHelp(trigger: HTMLElement): void {
  const dialog = document.createElement("dialog");
  dialog.className = "markdown-help-dialog";
  dialog.setAttribute("aria-labelledby", "markdown-help-title");

  const heading = document.createElement("h2");
  heading.id = "markdown-help-title";
  heading.textContent = "Markdown syntax";
  const close = document.createElement("button");
  close.type = "button";
  close.className = "secondary-button";
  close.textContent = "Close";
  const header = document.createElement("div");
  header.className = "markdown-help-header";
  header.append(heading, close);
  dialog.append(header);

  const body = document.createElement("div");
  body.className = "markdown-help-body";
  for (const section of helpSections) {
    const title = document.createElement("h3");
    title.textContent = section.title;
    const list = document.createElement("dl");
    list.className = "markdown-help-list";
    for (const row of section.rows) {
      const term = document.createElement("dt");
      const syntax = document.createElement("code");
      syntax.textContent = row.syntax;
      term.append(syntax);
      const description = document.createElement("dd");
      description.textContent = row.description;
      list.append(term, description);
    }
    const group = document.createElement("section");
    group.className = "markdown-help-section";
    group.append(title, list);
    body.append(group);
  }
  const note = document.createElement("p");
  note.className = "markdown-help-note";
  note.textContent = helpNote;
  body.append(note);
  dialog.append(body);

  document.body.append(dialog);
  close.addEventListener("click", () => dialog.close());
  // A click on the backdrop lands on the dialog itself rather than on anything
  // inside it, which is how a modal dialog reports one.
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener("close", () => {
    dialog.remove();
    if (trigger.isConnected) trigger.focus();
  }, { once: true });
  dialog.showModal();
  close.focus();
}
