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
  if (key === "ArrowDown" && count > 0) return "next";
  if (key === "ArrowUp" && count > 0) return "previous";
  if (key === "Enter") return open && active >= 0 ? "select" : "submit";
  if (key === "Escape" && open) return "dismiss";
  return "none";
}

/**
 * Runs one debounced suggestion request at a time. Aborting the old request
 * saves backend work; the generation check also ignores a stale response from
 * a transport that completed despite cancellation.
 */
export class SuggestionSearch {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private request: AbortController | undefined;
  private generation = 0;

  constructor(
    private readonly load: (query: string, signal: AbortSignal) => Promise<Suggestion[]>,
    private readonly update: (state: SuggestionState, rows: Suggestion[]) => void,
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

let autocompleteID = 0;

export function attachAutocomplete(
  input: HTMLInputElement,
  load: (query: string, signal: AbortSignal) => Promise<Suggestion[]>,
): HTMLElement {
  const host = document.createElement("div");
  host.className = "autocomplete";
  input.before(host);
  host.append(input);

  const list = document.createElement("div");
  list.className = "autocomplete-list";
  list.id = `autocomplete-${++autocompleteID}`;
  list.setAttribute("role", "listbox");
  list.hidden = true;
  host.append(list);

  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-controls", list.id);
  input.setAttribute("aria-expanded", "false");
  input.autocomplete = "off";

  let rows: Suggestion[] = [];
  let active = -1;

  const setOpen = (open: boolean): void => {
    list.hidden = !open;
    input.setAttribute("aria-expanded", String(open));
    if (!open) {
      active = -1;
      input.removeAttribute("aria-activedescendant");
    }
  };

  const drawActive = (): void => {
    for (const [index, option] of Array.from(list.querySelectorAll<HTMLElement>("[role=option]")).entries()) {
      const selected = index === active;
      option.setAttribute("aria-selected", String(selected));
      option.classList.toggle("active", selected);
      if (selected) {
        input.setAttribute("aria-activedescendant", option.id);
        option.scrollIntoView({ block: "nearest" });
      }
    }
  };

  const choose = (index: number): void => {
    const row = rows[index];
    if (row === undefined) return;
    input.value = row.value;
    input.dispatchEvent(new Event("change", { bubbles: true }));
    search.close();
    setOpen(false);
  };

  const search = new SuggestionSearch(load, (state, suggestions) => {
    rows = suggestions;
    active = -1;
    list.replaceChildren();
    if (state === "idle") {
      setOpen(false);
      return;
    }
    if (state !== "ready") {
      const messages: Record<Exclude<SuggestionState, "ready">, string> = {
        idle: "Type to search",
        loading: "Searching…",
        empty: "No suggestions",
        error: "Suggestions unavailable; manual entry still works",
      };
      const status = document.createElement("div");
      status.className = "autocomplete-status";
      status.setAttribute("role", "status");
      status.textContent = messages[state];
      list.append(status);
      setOpen(true);
      return;
    }
    suggestions.forEach((row, index) => {
      const option = document.createElement("div");
      option.id = `${list.id}-option-${index}`;
      option.className = "autocomplete-option";
      option.setAttribute("role", "option");
      option.setAttribute("aria-selected", "false");
      const label = document.createElement("span");
      label.className = "autocomplete-option-label";
      label.textContent = row.label;
      option.append(label);
      if (row.detail !== undefined) {
        const detail = document.createElement("span");
        detail.className = "autocomplete-option-detail";
        detail.textContent = row.detail;
        option.append(detail);
      }
      option.addEventListener("mousedown", (event) => event.preventDefault());
      option.addEventListener("click", () => choose(index));
      list.append(option);
    });
    setOpen(true);
  });

  input.addEventListener("input", () => search.query(input.value));
  input.addEventListener("keydown", (event) => {
    const action = autocompleteKeyAction(event.key, !list.hidden, active, rows.length);
    if (action === "next" || action === "previous") {
      event.preventDefault();
      active = nextActiveIndex(active, rows.length, action === "next" ? 1 : -1);
      drawActive();
    } else if (action === "select") {
      event.preventDefault();
      choose(active);
    } else if (action === "dismiss") {
      event.preventDefault();
      search.close();
      setOpen(false);
    }
  });
  input.addEventListener("focus", () => {
    if (input.value.trim() !== "") search.query(input.value);
  });
  host.addEventListener("focusout", () => {
    setTimeout(() => {
      if (!host.contains(document.activeElement)) setOpen(false);
    });
  });
  return host;
}
