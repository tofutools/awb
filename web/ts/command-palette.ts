import type { NavigationResults } from "./api.js";
import { nextActiveIndex } from "./autocomplete.js";

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
      keywords: `${issue.id} ${issue.workspace}`,
      group: "Issues",
      run: () => navigate(`#/issues/${encodeURIComponent(issue.id)}`),
    })),
    ...results.workspaces.map((workspace) => ({
      id: `workspace:${workspace.key}`,
      label: workspace.name,
      hint: workspace.key,
      keywords: workspace.key,
      group: "Workspaces",
      run: () => navigate(`#/workspaces/${encodeURIComponent(workspace.key)}`),
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

