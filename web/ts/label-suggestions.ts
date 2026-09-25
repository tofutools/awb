import type { Facet, FacetFilters } from "./api.js";
import type { Suggestion } from "./autocomplete.js";

/** A label editor suggests every label in use in the issue's workspace,
 * including those only closed issues carry: a label stays worth reusing after
 * the work that introduced it is done. */
export function labelSuggestionFilters(workspace: string): FacetFilters {
  return { workspace: [workspace], "include-closed": true };
}

/** The labels matching the typed text, case-insensitively, that the issue does
 * not already carry. */
export function labelSuggestions(
  facets: readonly Facet[],
  taken: readonly string[],
  query: string,
): Suggestion[] {
  const needle = query.toLocaleLowerCase();
  return facets
    .filter(
      (f) =>
        !taken.includes(f.value) &&
        f.value.toLocaleLowerCase().includes(needle),
    )
    .map((f) => ({ value: f.value, label: f.value }));
}
