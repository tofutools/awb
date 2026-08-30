/**
 * configureSearchBox keeps the JavaScript-only global search out of browser
 * form history. It deliberately has no name: a generic name such as "q" can
 * make browsers offer values saved by unrelated search forms.
 */
export function configureSearchBox(
  form: Pick<HTMLFormElement, "autocomplete">,
  input: Pick<HTMLInputElement, "autocomplete" | "placeholder" | "type">,
): void {
  form.autocomplete = "off";
  input.type = "search";
  input.autocomplete = "off";
  input.placeholder = "Search…";
}
