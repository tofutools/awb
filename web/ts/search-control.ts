export interface SearchClearControl {
  button: HTMLButtonElement;
  sync: () => void;
}

/** Give every search input the same explicit clear action. Chromium's native
 * cancel button is suppressed in CSS because it otherwise sits beside this
 * control, while Firefox needs the explicit button to offer an equivalent. */
export function attachSearchClear(
  input: HTMLInputElement,
  clear: () => void = () => input.dispatchEvent(new Event("input", { bubbles: true })),
): SearchClearControl {
  input.type = "search";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "search-clear";
  button.textContent = "×";
  button.title = "Clear search";
  button.setAttribute("aria-label", "Clear search");

  const sync = (): void => {
    button.hidden = input.value === "";
  };
  input.addEventListener("input", sync);
  button.addEventListener("click", () => {
    input.value = "";
    sync();
    clear();
    input.focus();
  });
  sync();
  return { button, sync };
}
