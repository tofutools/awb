import type { IssueCreate } from "./api.js";

const labelPattern = /^[a-z0-9._/-]+$/;

export type StagedLabelResult =
  | { label: string; error?: never }
  | { label?: never; error: string };

/** Validate a label before staging it on a new issue. */
export function stagedLabel(raw: string, existing: readonly string[]): StagedLabelResult {
  const label = raw.trim();
  if (label === "") return { error: "Enter a label." };
  if (label.length > 64 || !labelPattern.test(label)) {
    return { error: "Use at most 64 lowercase letters, digits, hyphens, underscores, dots or slashes." };
  }
  if (existing.includes(label)) return { error: "That label is already staged." };
  return { label };
}

/** Build the deliberately minimal create request used by rapid child entry.
 * The backend supplies the ordinary issue defaults, just as it does for a
 * title-only CLI create. Whitespace alone is not a title and never starts a
 * request. */
export function inlineChildIssueCreate(
  workspace: string,
  parent: string,
  rawTitle: string,
): IssueCreate | undefined {
  const title = rawTitle.trim();
  if (title === "") return undefined;
  return {
    workspace,
    title,
    relations: [{ type: "has-parent", other: parent }],
  };
}

/** Metadata is optional on creation, but a supplied value must be a JSON object. */
export function issueMetadata(raw: string): NonNullable<IssueCreate["metadata"]> {
  if (!raw.trim()) return {};
  let unsafeNumber = false;
  let value: unknown;
  try {
    value = JSON.parse(raw, (_key, item: unknown) => {
      if (typeof item === "number" &&
          (!Number.isFinite(item) || (Number.isInteger(item) && !Number.isSafeInteger(item)))) {
        unsafeNumber = true;
      }
      return item;
    });
  } catch {
    throw new Error("Metadata must be valid JSON.");
  }
  if (unsafeNumber) {
    throw new Error("Metadata numbers must be finite and integers must be within the safe range. Use strings for large IDs.");
  }
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Metadata must be a JSON object.");
  }
  return value as NonNullable<IssueCreate["metadata"]>;
}
