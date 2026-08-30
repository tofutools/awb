import { relativeTime } from "./presentation.js";

export type UpdatedDisplay = "relative" | "date" | "datetime";

const updatedDisplayKey = "awb.updated-display";
const displays: readonly UpdatedDisplay[] = ["relative", "date", "datetime"];

interface UpdatedStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface StorageHost {
  readonly localStorage: UpdatedStorage;
}

export function updatedStorage(host: StorageHost): UpdatedStorage | null {
  try {
    return host.localStorage;
  } catch {
    return null;
  }
}

export function readUpdatedDisplay(storage: UpdatedStorage | null): UpdatedDisplay {
  if (storage === null) return "relative";
  try {
    const value = storage.getItem(updatedDisplayKey);
    return displays.includes(value as UpdatedDisplay) ? value as UpdatedDisplay : "relative";
  } catch {
    return "relative";
  }
}

export function rememberUpdatedDisplay(storage: UpdatedStorage | null, display: UpdatedDisplay): void {
  if (storage === null) return;
  try {
    storage.setItem(updatedDisplayKey, display);
  } catch {
    // The current page still changes even when the preference cannot persist.
  }
}

export function formatUpdated(timestamp: string, display: UpdatedDisplay, now = Date.now()): string {
  if (display === "relative") return relativeTime(timestamp, now);
  const value = new Date(timestamp);
  if (!Number.isFinite(value.getTime())) return timestamp;

  const date = `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}`;
  if (display === "date") return date;
  return `${date} ${pad(value.getHours())}:${pad(value.getMinutes())}`;
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}
