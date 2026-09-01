// CodeMirror owns the visible editor, while a hidden textarea preserves the
// native form control contract used by the rest of the UI.

import {
  EditorView,
  classHighlighter,
  defaultKeymap,
  history,
  historyKeymap,
  keymap,
  markdown,
  syntaxHighlighting,
} from "codemirror";

export interface MarkdownEditor {
  readonly element: HTMLElement;
  readonly textarea: HTMLTextAreaElement;
}

/** createMarkdownEditor builds a Markdown-aware editor whose value remains
 * available through a textarea, including input events and form.elements. */
export function createMarkdownEditor(
  value: string,
  name: string | undefined,
  label: string,
): MarkdownEditor {
  const element = document.createElement("div");
  element.className = "markdown-editor";

  const textarea = document.createElement("textarea");
  textarea.value = value;
  if (name !== undefined) textarea.name = name;
  textarea.hidden = true;

  const mount = document.createElement("div");
  mount.className = "markdown-editor-mount";
  element.append(textarea, mount);

  const view = new EditorView({
    doc: value,
    extensions: [
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap]),
      EditorView.lineWrapping,
      syntaxHighlighting(classHighlighter),
      markdown(),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged) return;
        textarea.value = update.state.doc.toString();
        textarea.dispatchEvent(new Event("input", { bubbles: true }));
      }),
    ],
    parent: mount,
  });
  view.contentDOM.setAttribute("aria-label", label);

  return { element, textarea };
}
