// Minimal declarations for the committed CodeMirror bundle. Only the surface
// used by markdown-editor.ts is exposed here.
declare module "codemirror" {
  interface TextDocument { toString(): string; }
  interface EditorState { readonly doc: TextDocument; }
  interface ViewUpdate {
    readonly docChanged: boolean;
    readonly state: EditorState;
  }

  interface EditorViewConfig {
    doc: string;
    selection?: { anchor: number; head: number };
    extensions: readonly unknown[];
    parent: HTMLElement;
  }

  class EditorView {
    static readonly lineWrapping: unknown;
    static readonly contentAttributes: { of(attributes: Readonly<Record<string, string>>): unknown };
    static readonly updateListener: { of(listener: (update: ViewUpdate) => void): unknown };
    readonly contentDOM: HTMLElement;
    constructor(config: EditorViewConfig);
    focus(): void;
    destroy(): void;
  }

  const keymap: { of(bindings: readonly unknown[]): unknown };
  const defaultKeymap: readonly unknown[];
  const historyKeymap: readonly unknown[];
  function history(): unknown;
  function syntaxHighlighting(highlighter: unknown): unknown;
  const classHighlighter: unknown;
  function markdown(): unknown;
}
