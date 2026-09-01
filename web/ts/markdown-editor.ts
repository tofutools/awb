// CodeMirror owns the visible editor, while a hidden textarea exposes its
// current value to the existing form code. The editor is loaded only when its
// form becomes visible and must be destroyed before that form leaves the DOM.

export interface MarkdownEditor {
  readonly element: HTMLElement;
  readonly textarea: HTMLTextAreaElement;
  activate(): Promise<void>;
  destroy(): void;
}

interface KeyBinding {
  readonly key: string;
  readonly run: () => boolean;
}

/** markdownEditorKeymap reserves the form's save shortcut before CodeMirror's
 * default binding can insert a blank line. The event still bubbles to the form. */
export function markdownEditorKeymap(
  defaultBindings: readonly unknown[],
  historyBindings: readonly unknown[],
): readonly unknown[] {
  const save: KeyBinding = { key: "Mod-Enter", run: () => true };
  return [save, ...defaultBindings, ...historyBindings];
}

const editors = new WeakMap<HTMLElement, MarkdownEditor>();

/** activateMarkdownEditors loads editors only after their hidden form is shown. */
export function activateMarkdownEditors(root: ParentNode): void {
  for (const element of root.querySelectorAll<HTMLElement>(".markdown-editor")) {
    void editors.get(element)?.activate();
  }
}

/** destroyMarkdownEditors releases CodeMirror's document and window listeners. */
export function destroyMarkdownEditors(root: ParentNode): void {
  for (const element of root.querySelectorAll<HTMLElement>(".markdown-editor")) {
    editors.get(element)?.destroy();
  }
}

export function createMarkdownEditor(
  value: string,
  name: string | undefined,
  label: string,
): MarkdownEditor {
  const element = document.createElement("div");
  element.className = "markdown-editor";

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.defaultValue = value;
  textarea.rows = 10;
  textarea.setAttribute("aria-label", label);
  if (name !== undefined) textarea.name = name;

  const mount = document.createElement("div");
  mount.className = "markdown-editor-mount";
  mount.hidden = true;
  element.append(textarea, mount);

  type EditorViewInstance = InstanceType<(typeof import("codemirror"))["EditorView"]>;
  let view: EditorViewInstance | undefined;
  let loading: Promise<void> | undefined;
  let disposed = false;

  const editor: MarkdownEditor = {
    element,
    textarea,
    activate(): Promise<void> {
      if (view !== undefined) return Promise.resolve();
      if (loading !== undefined) return loading;
      if (!element.isConnected) {
        loading = new Promise((resolve) => {
          requestAnimationFrame(() => {
            loading = undefined;
            if (element.isConnected && !disposed) void editor.activate().then(resolve);
            else resolve();
          });
        });
        return loading;
      }
      loading = import("codemirror").then(({
        EditorView,
        classHighlighter,
        defaultKeymap,
        history,
        historyKeymap,
        keymap,
        markdown,
        syntaxHighlighting,
      }) => {
        if (disposed) return;
        const restoreFocus = document.activeElement === textarea;
        const anchor = textarea.selectionStart;
        const head = textarea.selectionEnd;
        view = new EditorView({
          doc: textarea.value,
          selection: { anchor, head },
          extensions: [
            history(),
            keymap.of(markdownEditorKeymap(defaultKeymap, historyKeymap)),
            EditorView.lineWrapping,
            EditorView.contentAttributes.of({
              "aria-label": label,
              spellcheck: "true",
              autocorrect: "on",
              autocapitalize: "sentences",
              writingsuggestions: "true",
            }),
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
        textarea.hidden = true;
        mount.hidden = false;
        if (restoreFocus) view.focus();
      });
      return loading;
    },
    destroy(): void {
      disposed = true;
      view?.destroy();
      view = undefined;
      loading = undefined;
    },
  };

  editors.set(element, editor);
  return editor;
}
