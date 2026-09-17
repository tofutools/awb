export interface PendingComment {
  body: string;
  key: string;
}

/** Mint the same 16-byte hexadecimal key as the CLI without requiring HTTPS. */
export function newCommentKey(): string {
  return Array.from(
    crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, "0"),
  ).join("");
}

/** Reuse the key while retrying unchanged content; edited content is a new comment. */
export function pendingComment(
  previous: PendingComment | null,
  body: string,
  makeKey: () => string = newCommentKey,
): PendingComment {
  return previous?.body === body ? previous : { body, key: makeKey() };
}
