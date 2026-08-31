/** commentSubmitShortcut recognizes the textarea shortcut without treating an
 * Enter used to confirm an in-progress input-method composition as submission. */
export function commentSubmitShortcut(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "isComposing">,
): boolean {
  return event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing;
}

export type IssueEditorShortcut = "save" | "hide";

/** issueEditorShortcut recognizes edit-form shortcuts without treating a key
 * used during an input-method composition as an editor command. */
export function issueEditorShortcut(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "isComposing">,
): IssueEditorShortcut | undefined {
  if (event.isComposing) return undefined;
  if (event.key === "Escape") return "hide";
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) return "save";
  return undefined;
}
