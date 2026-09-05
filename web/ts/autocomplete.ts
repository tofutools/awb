export const autocompleteDebounceMs = 200;

export interface Suggestion {
  value: string;
  label: string;
  detail?: string;
}

export type SuggestionState = "idle" | "loading" | "ready" | "empty" | "error";

export function nextActiveIndex(current: number, count: number, direction: 1 | -1): number {
  if (count === 0) return -1;
  if (current < 0) return direction === 1 ? 0 : count - 1;
  return (current + direction + count) % count;
}

export type AutocompleteKeyAction = "next" | "previous" | "select" | "dismiss" | "submit" | "none";

export function autocompleteKeyAction(
  key: string,
  open: boolean,
  active: number,
  count: number,
): AutocompleteKeyAction {
  if (key === "ArrowDown" && open && count > 0) return "next";
  if (key === "ArrowUp" && open && count > 0) return "previous";
  if (key === "Enter") return open && active >= 0 ? "select" : "submit";
  if (key === "Escape" && open) return "dismiss";
  return "none";
}

/**
 * Runs one debounced suggestion request at a time. Aborting the old request
 * saves backend work; the generation check also ignores a stale response from
 * a transport that completed despite cancellation.
 */
export class SuggestionSearch<T = Suggestion> {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private request: AbortController | undefined;
  private generation = 0;

  constructor(
    private readonly load: (query: string, signal: AbortSignal) => Promise<T[]>,
    private readonly update: (state: SuggestionState, rows: T[]) => void,
  ) {}

  query(raw: string): void {
    const query = raw.trim();
    this.generation++;
    const generation = this.generation;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.request?.abort();
    this.request = undefined;
    if (query === "") {
      this.update("idle", []);
      return;
    }
    this.update("loading", []);
    this.timer = setTimeout(() => {
      const request = new AbortController();
      this.request = request;
      void this.load(query, request.signal).then((rows) => {
        if (generation !== this.generation || request.signal.aborted) return;
        this.update(rows.length === 0 ? "empty" : "ready", rows);
      }).catch(() => {
        if (generation !== this.generation || request.signal.aborted) return;
        this.update("error", []);
      });
    }, autocompleteDebounceMs);
  }

  close(): void {
    this.generation++;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.request?.abort();
  }
}

