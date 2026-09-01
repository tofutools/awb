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

/** inspectorDismissShortcut leaves an inner control's Escape handling intact:
 * autocomplete dismisses its suggestions first, then a second Escape closes
 * the field editor. */
export function inspectorDismissShortcut(
  event: Pick<KeyboardEvent, "key" | "defaultPrevented">,
): boolean {
  return event.key === "Escape" && !event.defaultPrevented;
}

export type ConfirmationDecision = "confirm" | "cancel";

/** confirmationDecision gives every mutation confirmation the same keyboard
 * contract without treating an input-method composition as a decision. */
export function confirmationDecision(
  event: Pick<KeyboardEvent, "key" | "isComposing">,
): ConfirmationDecision | undefined {
  if (event.isComposing) return undefined;
  if (event.key === "Enter") return "confirm";
  if (event.key === "Escape") return "cancel";
  return undefined;
}
