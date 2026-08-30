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

  const difference = Math.round((then - now) / 1000);
  const absolute = Math.abs(difference);
  if (absolute < 10) return "just now";

  const minute = 60;
  const hour = 60 * minute;
  const day = 24 * hour;
  const month = 30 * day;
  const year = 365 * day;
  let duration: string;
  if (absolute < minute) duration = `${absolute}s`;
  else if (absolute < hour) duration = `${Math.floor(absolute / minute)}m`;
  else if (absolute < day) duration = compoundDuration(absolute, hour, "h", minute, "m");
  else if (absolute < month) duration = compoundDuration(absolute, day, "d", hour, "h");
  else if (absolute < year) duration = compoundDuration(absolute, month, "mo", day, "d");
  else duration = compoundDuration(absolute, year, "y", month, "mo");
  return difference < 0 ? `${duration} ago` : `in ${duration}`;
}

function compoundDuration(value: number, majorSize: number, majorUnit: string, minorSize: number, minorUnit: string): string {
  const major = Math.floor(value / majorSize);
  const minor = Math.floor((value % majorSize) / minorSize);
  return `${major}${majorUnit}${minor === 0 ? "" : ` ${minor}${minorUnit}`}`;
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

/** activityValues extracts a one-item array delta when possible. This makes a
 * relation or label audit entry name what changed rather than only comparing
 * the sizes of the before and after snapshots. */
export function activityValues(from: unknown, to: unknown): readonly [string, string] {
  if (Array.isArray(from) && Array.isArray(to)) {
    const before = new Map(from.map((item) => [JSON.stringify(item), item]));
    const after = new Map(to.map((item) => [JSON.stringify(item), item]));
    const removed = [...before].filter(([key]) => !after.has(key)).map(([, item]) => item);
    const added = [...after].filter(([key]) => !before.has(key)).map(([, item]) => item);
    if (removed.length <= 1 && added.length <= 1 && removed.length + added.length > 0) {
      return [activityValue(removed[0]), activityValue(added[0])];
    }
  }
  return [activityValue(from), activityValue(to)];
}

function isRelation(value: unknown): value is { type: string; other: string } {
  if (value === null || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return typeof record.type === "string" && typeof record.other === "string";
}
