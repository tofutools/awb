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
