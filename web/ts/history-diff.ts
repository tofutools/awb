export type HistoryDiffKind = "same" | "remove" | "add" | "omitted";

export interface HistoryDiffPart {
  kind: HistoryDiffKind;
  text: string;
}

const maximumEditDistance = 512;

/** historyDiff returns an exact, word-oriented edit script. Long descriptions
 * with a small edit stay cheap because their common edges are removed before
 * the middle is compared. A wholesale very large replacement deliberately
 * falls back to one removal and addition instead of doing unbounded work. */
export function historyDiff(before: string, after: string): HistoryDiffPart[] {
  if (before === after) return before === "" ? [] : [{ kind: "same", text: before }];

  const left = tokens(before);
  const right = tokens(after);
  let prefix = 0;
  while (prefix < left.length && prefix < right.length && left[prefix] === right[prefix]) prefix++;
  let suffix = 0;
  while (
    suffix < left.length - prefix && suffix < right.length - prefix &&
    left[left.length - suffix - 1] === right[right.length - suffix - 1]
  ) suffix++;

  const result: HistoryDiffPart[] = [];
  append(result, "same", left.slice(0, prefix).join(""));
  const middleBefore = left.slice(prefix, left.length - suffix);
  const middleAfter = right.slice(prefix, right.length - suffix);
  const middle = shortestEditScript(middleBefore, middleAfter);
  if (middle === null) {
    append(result, "remove", middleBefore.join(""));
    append(result, "add", middleAfter.join(""));
  } else {
    for (const part of middle) append(result, part.kind, part.text);
  }
  append(result, "same", left.slice(left.length - suffix).join(""));
  return result;
}

/** historyDiffPreview keeps every changed region while abbreviating unchanged
 * stretches around them. This makes an edit after a long common prefix visible
 * in the timeline, and explicitly marks every place where context was omitted. */
export function historyDiffPreview(
  parts: readonly HistoryDiffPart[],
  contextCharacters = 44,
  changedCharacters = 120,
): HistoryDiffPart[] {
  const result: HistoryDiffPart[] = [];
  for (let index = 0; index < parts.length; index++) {
    const part = parts[index];
    if (part.kind === "same") {
      const leading = index === 0;
      const trailing = index === parts.length - 1;
      const limit = leading || trailing ? contextCharacters : contextCharacters * 2;
      if (characters(part.text).length <= limit) {
        append(result, "same", part.text);
      } else if (leading) {
        append(result, "omitted", "…");
        append(result, "same", lastCharacters(part.text, contextCharacters));
      } else if (trailing) {
        append(result, "same", firstCharacters(part.text, contextCharacters));
        append(result, "omitted", "…");
      } else {
        append(result, "same", firstCharacters(part.text, contextCharacters));
        append(result, "omitted", "…");
        append(result, "same", lastCharacters(part.text, contextCharacters));
      }
      continue;
    }
    if (part.kind === "omitted" || characters(part.text).length <= changedCharacters) {
      append(result, part.kind, part.text);
      continue;
    }
    const edge = Math.floor(changedCharacters / 2);
    append(result, part.kind, firstCharacters(part.text, edge));
    append(result, "omitted", "…");
    append(result, part.kind, lastCharacters(part.text, edge));
  }
  return result;
}

function tokens(value: string): string[] {
  // Keep punctuation attached to its source word. Treating every full stop as
  // a reusable token can align a sentence's punctuation with a later line and
  // misleadingly show the full stop as the thing that moved.
  return value.match(/\r\n|\n|[^\S\r\n]+|[^\s]+/g) ?? [];
}

function shortestEditScript(before: readonly string[], after: readonly string[]): HistoryDiffPart[] | null {
  if (before.length === 0) return after.length === 0 ? [] : [{ kind: "add", text: after.join("") }];
  if (after.length === 0) return [{ kind: "remove", text: before.join("") }];

  const maximum = before.length + after.length;
  const distanceLimit = Math.min(maximum, maximumEditDistance);
  let frontier = new Map<number, number>([[1, 0]]);
  const trace: Map<number, number>[] = [];
  for (let distance = 0; distance <= distanceLimit; distance++) {
    trace.push(new Map(frontier));
    for (let diagonal = -distance; diagonal <= distance; diagonal += 2) {
      const down = frontier.get(diagonal + 1) ?? Number.NEGATIVE_INFINITY;
      const right = frontier.get(diagonal - 1) ?? Number.NEGATIVE_INFINITY;
      let x = diagonal === -distance || (diagonal !== distance && right < down) ? down : right + 1;
      if (!Number.isFinite(x)) x = 0;
      let y = x - diagonal;
      while (x < before.length && y < after.length && before[x] === after[y]) {
        x++;
        y++;
      }
      frontier.set(diagonal, x);
      if (x >= before.length && y >= after.length) return backtrack(trace, before, after);
    }
  }
  return null;
}

function backtrack(
  trace: readonly Map<number, number>[],
  before: readonly string[],
  after: readonly string[],
): HistoryDiffPart[] {
  let x = before.length;
  let y = after.length;
  const reversed: HistoryDiffPart[] = [];
  for (let distance = trace.length - 1; distance >= 0; distance--) {
    const frontier = trace[distance];
    const diagonal = x - y;
    const down = frontier.get(diagonal + 1) ?? Number.NEGATIVE_INFINITY;
    const right = frontier.get(diagonal - 1) ?? Number.NEGATIVE_INFINITY;
    const previousDiagonal = diagonal === -distance || (diagonal !== distance && right < down)
      ? diagonal + 1
      : diagonal - 1;
    const previousX = frontier.get(previousDiagonal) ?? 0;
    const previousY = previousX - previousDiagonal;
    while (x > previousX && y > previousY) {
      reversed.push({ kind: "same", text: before[x - 1] });
      x--;
      y--;
    }
    if (distance === 0) break;
    if (x === previousX) {
      reversed.push({ kind: "add", text: after[y - 1] });
      y--;
    } else {
      reversed.push({ kind: "remove", text: before[x - 1] });
      x--;
    }
  }
  const result: HistoryDiffPart[] = [];
  for (const part of reversed.reverse()) append(result, part.kind, part.text);
  return result;
}

function append(result: HistoryDiffPart[], kind: HistoryDiffKind, text: string): void {
  if (text === "") return;
  const previous = result[result.length - 1];
  if (previous?.kind === kind) previous.text += text;
  else result.push({ kind, text });
}

function characters(value: string): string[] {
  return [...value];
}

function firstCharacters(value: string, count: number): string {
  return characters(value).slice(0, count).join("");
}

function lastCharacters(value: string, count: number): string {
  return characters(value).slice(-count).join("");
}
