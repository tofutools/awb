import { useLayoutEffect, useRef, useState } from "preact/hooks";
import { markdownEditorKeymap } from "../markdown-editor.js";
import { MarkdownToolbar } from "./markdown-toolbar.js";
import type { EditorHost } from "../markdown-toolbar.js";

/** Preact owns the editor shell and toolbar. CodeMirror owns only its mount;
 * late module loads cannot create an editor after the component unmounts. */
export function MarkdownInput({
  value,
  name,
  label,
  onInput,
}: {
  value: string;
  name?: string;
  label: string;
  onInput?: (value: string) => void;
}) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  const mount = useRef<HTMLDivElement>(null);
  const callback = useRef(onInput);
  callback.current = onInput;
  const [host, setHost] = useState<EditorHost>();
  const [selection, setSelection] = useState(0);
  useLayoutEffect(() => {
    let disposed = false;
    let view:
      InstanceType<(typeof import("codemirror"))["EditorView"]> | undefined;
    void import("codemirror")
      .then(
        ({
          EditorView,
          GFM,
          classHighlighter,
          defaultKeymap,
          history,
          historyKeymap,
          keymap,
          markdown,
          syntaxHighlighting,
          tagHighlighter,
          tags,
        }) => {
          if (disposed) return;
          const input = textarea.current!;
          const focused = document.activeElement === input;
          view = new EditorView({
            doc: input.value,
            selection: {
              anchor: input.selectionStart,
              head: input.selectionEnd,
            },
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
              syntaxHighlighting(
                tagHighlighter([
                  { tag: tags.strikethrough, class: "tok-strikethrough" },
                ]),
              ),
              markdown({ extensions: [GFM] }),
              EditorView.updateListener.of((update) => {
                if (update.docChanged) {
                  input.value = update.state.doc.toString();
                  callback.current?.(input.value);
                  input.dispatchEvent(new Event("input", { bubbles: true }));
                }
                if (update.docChanged || update.selectionSet)
                  setSelection((value) => value + 1);
              }),
            ],
            parent: mount.current!,
          });
          setHost({
            doc: () => view!.state.doc.toString(),
            lineAt: (pos) => view!.state.doc.lineAt(pos).text,
            selection: () => view!.state.selection.main,
            apply: (edit) => {
              view!.dispatch({
                changes: edit.changes,
                selection: edit.selection,
              });
              view!.focus();
            },
          });
          if (focused) view.focus();
        },
      )
      .catch(() => {
        /* The textarea remains a functional editor if loading fails. */
      });
    return () => {
      disposed = true;
      view?.destroy();
    };
  }, []);
  return (
    <div class="markdown-editor">
      {host && <MarkdownToolbar host={host} revision={selection} />}
      <textarea
        ref={textarea}
        defaultValue={value}
        name={name}
        rows={10}
        aria-label={label}
        hidden={!!host}
        onInput={(e) => callback.current?.(e.currentTarget.value)}
      />
      <div ref={mount} class="markdown-editor-mount" hidden={!host} />
    </div>
  );
}
