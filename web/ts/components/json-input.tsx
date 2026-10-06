import { useLayoutEffect, useRef, useState } from "preact/hooks";
import { markdownEditorKeymap } from "../markdown-editor.js";
import { metadataDiagnostics } from "../json-editor.js";

/** CodeMirror owns its mount; the hidden textarea keeps native form submission.
 * A failed or late import leaves the textarea usable and cannot mount after disposal. */
export function JsonInput({ name, label }: { name: string; label: string }) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  const mount = useRef<HTMLDivElement>(null);
  const [ready, setReady] = useState(false);
  useLayoutEffect(() => {
    let disposed = false;
    let view: InstanceType<(typeof import("codemirror"))["EditorView"]> | undefined;
    void import("codemirror").then((cm) => {
      if (disposed) return;
      const input = textarea.current!;
      const focused = document.activeElement === input;
      view = new cm.EditorView({
        doc: input.value,
        selection: { anchor: input.selectionStart, head: input.selectionEnd },
        extensions: [
          cm.json(),
          cm.lineNumbers(),
          cm.history(),
          cm.indentOnInput(),
          cm.bracketMatching(),
          cm.closeBrackets(),
          cm.foldGutter(),
          cm.keymap.of(markdownEditorKeymap(
            [...cm.closeBracketsKeymap, ...cm.defaultKeymap, ...cm.foldKeymap],
            cm.historyKeymap,
          )),
          cm.EditorView.lineWrapping,
          cm.EditorView.contentAttributes.of({
            "aria-label": label,
            spellcheck: "false",
            autocorrect: "off",
            autocapitalize: "off",
          }),
          cm.syntaxHighlighting(cm.classHighlighter),
          cm.linter((editor) => metadataDiagnostics(editor.state.doc.toString())),
          cm.EditorView.updateListener.of((update) => {
            if (update.docChanged) input.value = update.state.doc.toString();
          }),
        ],
        parent: mount.current!,
      });
      setReady(true);
      if (focused) view.focus();
    }).catch(() => {
      // The textarea remains available if the browser cannot load the bundle.
      view?.destroy();
    });
    return () => {
      disposed = true;
      view?.destroy();
    };
  }, []);
  return (
    <div class="json-editor code-editor">
      <textarea
        ref={textarea}
        name={name}
        rows={4}
        aria-label={label}
        placeholder={'{"key": "value"}'}
        spellcheck={false}
        hidden={ready}
      />
      <div ref={mount} class="json-editor-mount" hidden={!ready} />
    </div>
  );
}
