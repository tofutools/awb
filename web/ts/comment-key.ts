export interface PendingComment {
  body: string;
  key: string;
}

/** Reuse the key while retrying unchanged content; edited content is a new comment. */
export function pendingComment(
  previous: PendingComment | null,
  body: string,
  makeKey: () => string = () => crypto.randomUUID(),
): PendingComment {
  return previous?.body === body ? previous : { body, key: makeKey() };
}
