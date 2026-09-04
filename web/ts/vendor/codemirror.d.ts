// Minimal declarations for the committed CodeMirror bundle. Only the surface
// used by markdown-editor.ts is exposed here.
declare module "codemirror" {
  interface TextDocument { toString(): string; }
  interface SelectionRange { readonly from: number; readonly to: number; }
  interface EditorSelection { readonly main: SelectionRange; }
  interface EditorState {
    readonly doc: TextDocument;
    readonly selection: EditorSelection;
  }
  interface ViewUpdate {
    readonly docChanged: boolean;
    readonly selectionSet: boolean;
    readonly state: EditorState;
  }

  interface DocumentChange {
    readonly from: number;
    readonly to?: number;
    readonly insert: string;
  }
  interface TransactionSpec {
    changes: readonly DocumentChange[];
    selection?: { readonly anchor: number; readonly head: number };
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
    readonly state: EditorState;
    constructor(config: EditorViewConfig);
    dispatch(spec: TransactionSpec): void;
    focus(): void;
    destroy(): void;
  }

  const keymap: { of(bindings: readonly unknown[]): unknown };
  const defaultKeymap: readonly unknown[];
  const historyKeymap: readonly unknown[];
  function history(): unknown;
  function syntaxHighlighting(highlighter: unknown): unknown;
  const classHighlighter: unknown;
  function tagHighlighter(specs: readonly { tag: unknown; class: string }[]): unknown;
  const tags: { readonly strikethrough: unknown };
  function markdown(config?: { extensions?: readonly unknown[] }): unknown;
  const GFM: unknown;
}
