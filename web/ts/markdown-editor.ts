// CodeMirror gives the surrounding form first refusal of its save shortcut.
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
