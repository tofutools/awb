import type { NavigationResults } from "./api.js";
import { nextActiveIndex, SuggestionSearch, type SuggestionState } from "./autocomplete.js";
import { attachSearchClear, type SearchClearControl } from "./search-control.js";

export interface PaletteCommand {
  id: string;
  label: string;
  hint: string;
  keywords?: string;
  group: string;
  run: () => void;
}

export type CommandProvider = () => PaletteCommand[];

export const paletteTrigger = {
  label: "Commands",
  title: "Open command palette (Ctrl/Cmd+K)",
  keyShortcuts: "Control+K Meta+K",
} as const;

export function paletteShortcutHint(
  macOS = navigator.platform.toLocaleLowerCase().includes("mac"),
): "⌘K" | "Ctrl K" {
  return macOS ? "⌘K" : "Ctrl K";
}

/** CommandRegistry is the extension seam: features contribute commands while
 * one palette owns shortcut handling, ranking and accessible interaction. */
export class CommandRegistry {
  private readonly providers = new Map<string, CommandProvider>();

  register(key: string, provider: CommandProvider): () => void {
    if (key === "") throw new TypeError("command provider needs a key");
    this.providers.set(key, provider);
    return () => {
      if (this.providers.get(key) === provider) this.providers.delete(key);
    };
  }

  commands(): PaletteCommand[] {
    return [...this.providers.values()].flatMap((provider) => provider());
  }
}

export function isPaletteShortcut(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey" | "repeat" | "isComposing">,
  macOS = navigator.platform.toLocaleLowerCase().includes("mac"),
): boolean {
  return !event.repeat && !event.isComposing && !event.altKey && !event.shiftKey
    && (macOS ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey)
    && event.key.toLowerCase() === "k";
}

export function filterCommands(commands: PaletteCommand[], query: string): PaletteCommand[] {
  const terms = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return commands;
  return commands.filter((command) => {
    const haystack = `${command.label} ${command.hint} ${command.keywords ?? ""}`.toLocaleLowerCase();
    return terms.every((term) => haystack.includes(term));
  });
}

export type NavigationSearch = (query: string, signal: AbortSignal) => Promise<NavigationResults>;

export function navigationResultCommands(results: NavigationResults, navigate: (href: string) => void): PaletteCommand[] {
  return [
    ...results.issues.map((issue) => ({
      id: `issue:${issue.id}`,
      label: issue.title,
      hint: issue.id,
      keywords: `${issue.id} ${issue.project}`,
      group: "Issues",
      run: () => navigate(`#/issues/${encodeURIComponent(issue.id)}`),
    })),
    ...results.projects.map((project) => ({
      id: `project:${project.key}`,
      label: project.name,
      hint: project.key,
      keywords: project.key,
      group: "Projects",
      run: () => navigate(`#/projects/${encodeURIComponent(project.key)}`),
    })),
    ...results.users.map((user) => ({
      id: `user:${user.name}`,
      label: user.full_name || `@${user.name}`,
      hint: `@${user.name}`,
      keywords: `${user.name} ${user.full_name}`,
      group: "Users",
      run: () => navigate(`#/users?user=${encodeURIComponent(user.name)}`),
    })),
  ];
}

export type PaletteNavigationKey = "ArrowDown" | "ArrowUp" | "PageDown" | "PageUp" | "Home" | "End";

export function isPlainPaletteBoundaryKey(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey">,
): event is Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey"> & { key: "Home" | "End" } {
  return (event.key === "Home" || event.key === "End")
    && !event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey;
}

/** Return the next selected command for a keyboard navigation key. Arrow keys
 * retain their wrapping behaviour, while viewport and boundary moves clamp. */
export function nextPaletteSelection(
  key: PaletteNavigationKey,
  selected: number,
  count: number,
  pageSize: number,
): number {
  if (count === 0) return 0;
  if (key === "ArrowDown" || key === "ArrowUp") {
    return nextActiveIndex(selected, count, key === "ArrowDown" ? 1 : -1);
  }
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  const direction = key === "PageDown" ? 1 : -1;
  const size = Math.max(1, Math.floor(pageSize) || 1);
  return Math.min(count - 1, Math.max(0, selected + direction * size));
}

/** Measure how many rendered options span one result viewport from the active
 * option. Their positions include the group headings between them. */
export function visiblePalettePageSize(list: HTMLElement, selected: number, direction: 1 | -1): number {
  const options = [...list.querySelectorAll<HTMLElement>(".command-palette-option")];
  const active = options[selected];
  if (active === undefined || list.clientHeight <= 0) return 1;
  const boundary = active.offsetTop + direction * list.clientHeight;
  let target = selected;
  for (let index = selected + direction; index >= 0 && index < options.length; index += direction) {
    const withinViewport = direction > 0
      ? options[index].offsetTop <= boundary
      : options[index].offsetTop >= boundary;
    if (!withinViewport) break;
    target = index;
  }
  return Math.max(1, Math.abs(target - selected));
}

export class CommandPalette {
  private readonly dialog = document.createElement("dialog");
  private readonly input = document.createElement("input");
  private readonly list = document.createElement("div");
  private readonly status = document.createElement("div");
  private readonly clear: SearchClearControl;
  private commands: PaletteCommand[] = [];
  private selected = 0;
  private restoreFocus: HTMLElement | null = null;
  private readonly remoteSearch: SuggestionSearch<PaletteCommand>;

  constructor(
    private readonly registry: CommandRegistry,
    search: NavigationSearch,
    private readonly navigate: (href: string) => void,
  ) {
    this.dialog.className = "command-palette";
    this.dialog.setAttribute("aria-labelledby", "command-palette-title");
    const title = document.createElement("h2");
    title.id = "command-palette-title";
    title.textContent = "Go to";
    this.input.placeholder = "Search issues, projects, users, and views…";
    this.input.autocomplete = "off";
    this.input.setAttribute("role", "combobox");
    this.input.setAttribute("aria-autocomplete", "list");
    this.input.setAttribute("aria-expanded", "true");
    this.input.setAttribute("aria-controls", "command-palette-results");
    this.list.id = "command-palette-results";
    this.list.className = "command-palette-results";
    this.list.setAttribute("role", "listbox");
    this.status.className = "command-palette-status";
    this.status.setAttribute("aria-live", "polite");
    this.clear = attachSearchClear(this.input);
    const inputControl = document.createElement("div");
    inputControl.className = "search-control command-palette-search";
    inputControl.append(this.input, this.clear.button);
    const help = document.createElement("footer");
    help.innerHTML = "<span><kbd>↑</kbd><kbd>↓</kbd> select</span><span><kbd>Enter</kbd> open</span><span><kbd>Esc</kbd> close</span>";
    this.dialog.append(title, inputControl, this.list, this.status, help);
    document.body.append(this.dialog);

    // The issue editor and palette share one debounce, abort and stale-response
    // guard. Only their result mapping differs.
    this.remoteSearch = new SuggestionSearch<PaletteCommand>(
      async (query, signal) => navigationResultCommands(await search(query, signal), this.navigate),
      (state, remoteCommands) => this.updateSearchResults(state, remoteCommands),
    );

    this.input.addEventListener("input", () => this.update());
    this.input.addEventListener("keydown", (event) => this.onKeydown(event));
    this.dialog.addEventListener("click", (event) => {
      if (event.target === this.dialog) this.close();
    });
    this.dialog.addEventListener("close", () => {
      this.remoteSearch.close();
      this.restoreFocus?.focus();
      this.restoreFocus = null;
    });
    document.addEventListener("keydown", (event) => {
      if (!isPaletteShortcut(event)) return;
      event.preventDefault();
      if (this.dialog.open) this.close(); else this.open();
    });
  }

  open(): void {
    if (this.dialog.open) return;
    this.restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    this.input.value = "";
    this.clear.sync();
    this.status.textContent = "";
    this.commands = this.registry.commands();
    this.selected = 0;
    this.render();
    this.dialog.showModal();
    this.input.focus();
  }

  close(): void {
    if (this.dialog.open) this.dialog.close();
  }

  private update(): void {
    const query = this.input.value.trim();
    this.commands = filterCommands(this.registry.commands(), query);
    this.selected = 0;
    this.render();
    this.remoteSearch.query(query);
  }

  private updateSearchResults(state: SuggestionState, remoteCommands: PaletteCommand[]): void {
    const localCommands = filterCommands(this.registry.commands(), this.input.value.trim());
    this.commands = state === "ready" ? [...localCommands, ...remoteCommands] : localCommands;
    this.selected = 0;
    if (state === "idle") this.status.textContent = "";
    else if (state === "loading") this.status.textContent = "Searching…";
    else if (state === "error") this.status.textContent = "Search unavailable";
    else this.status.textContent = this.commands.length === 0 ? "No matches" : `${this.commands.length} matches`;
    this.render();
  }

  private onKeydown(event: KeyboardEvent): void {
    const navigationKey = event.key === "ArrowDown" || event.key === "ArrowUp"
      || event.key === "PageDown" || event.key === "PageUp" || isPlainPaletteBoundaryKey(event);
    if (navigationKey) {
      event.preventDefault();
      if (this.commands.length === 0) return;
      const pageSize = event.key === "PageDown" || event.key === "PageUp"
        ? visiblePalettePageSize(this.list, this.selected, event.key === "PageDown" ? 1 : -1)
        : 1;
      this.selected = nextPaletteSelection(
        event.key as PaletteNavigationKey,
        this.selected,
        this.commands.length,
        pageSize,
      );
      this.render();
    } else if (event.key === "Enter") {
      event.preventDefault();
      this.runSelected();
    } else if (event.key === "Escape") {
      event.preventDefault();
      this.close();
    }
  }

  private runSelected(): void {
    const command = this.commands[this.selected];
    if (command === undefined) return;
    this.close();
    command.run();
  }

  private render(): void {
    this.list.replaceChildren();
    let previousGroup = "";
    this.commands.forEach((command, index) => {
      if (command.group !== previousGroup) {
        const group = document.createElement("div");
        group.className = "command-palette-group";
        group.textContent = command.group;
        group.setAttribute("role", "presentation");
        this.list.append(group);
        previousGroup = command.group;
      }
      const option = document.createElement("div");
      option.id = `command-palette-option-${index}`;
      option.className = `command-palette-option${index === this.selected ? " selected" : ""}`;
      option.setAttribute("role", "option");
      option.setAttribute("aria-selected", String(index === this.selected));
      const label = document.createElement("span");
      label.className = "command-palette-label";
      label.textContent = command.label;
      const hint = document.createElement("span");
      hint.className = "command-palette-hint";
      hint.textContent = command.hint;
      option.append(label, hint);
      option.addEventListener("mousemove", () => {
        if (this.selected !== index) { this.selected = index; this.render(); }
      });
      option.addEventListener("click", () => { this.selected = index; this.runSelected(); });
      this.list.append(option);
      if (index === this.selected) {
        this.input.setAttribute("aria-activedescendant", option.id);
        queueMicrotask(() => option.scrollIntoView({ block: "nearest" }));
      }
    });
    if (this.commands.length === 0) this.input.removeAttribute("aria-activedescendant");
  }
}
