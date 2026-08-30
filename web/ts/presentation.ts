/** initialFor returns the compact identity marker used by the app chrome and
 * activity timeline. It deliberately handles the empty system actor too. */
export function initialFor(value: string): string {
  const name = value.trim().replace(/^@/, "");
  return name === "" ? "?" : [...name][0].toLocaleUpperCase();
}

/** relativeTime turns an API timestamp into the terse language used throughout
 * the issue view. The exact timestamp remains on the <time> element itself. */
export function relativeTime(timestamp: string, now = Date.now()): string {
  const then = Date.parse(timestamp);
  if (!Number.isFinite(then)) return timestamp;

  const seconds = (then - now) / 1000;
  const absolute = Math.abs(seconds);
  if (absolute < 45) return "just now";

  const formatter = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  const choices: readonly [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "minute"],
    [60, "hour"],
    [24, "day"],
    [30, "month"],
    [12, "year"],
  ];
  let value = seconds;
  let unit: Intl.RelativeTimeFormatUnit = "second";
  for (const [size, next] of choices) {
    if (Math.abs(value) < size) break;
    value /= size;
    unit = next;
  }
  return formatter.format(Math.round(value), unit);
}

/** activityValue keeps structural audit values useful without spilling whole
 * JSON snapshots across the timeline. Primitive changes remain exact. */
export function activityValue(value: unknown): string {
  if (typeof value === "string") return value === "" ? "(empty)" : value;
  if (value === null || value === undefined) return "(none)";
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) {
    if (value.length === 0) return "(none)";
    if (value.every((item) => typeof item === "string")) return value.join(", ");
    if (value.every(isRelation)) return `${value.length} relation${value.length === 1 ? "" : "s"}`;
    return `${value.length} item${value.length === 1 ? "" : "s"}`;
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    if (typeof record.name === "string") return record.name;
    if (isRelation(record)) return `${record.type} ${record.other}`;
  }
  return "changed";
}

function isRelation(value: unknown): value is { type: string; other: string } {
  if (value === null || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return typeof record.type === "string" && typeof record.other === "string";
}
