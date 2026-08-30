/** commentSubmitShortcut recognizes the textarea shortcut without treating an
 * Enter used to confirm an in-progress input-method composition as submission. */
export function commentSubmitShortcut(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "isComposing">,
): boolean {
  return event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing;
}
