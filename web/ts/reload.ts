const reloadEvent = "awb:reload";

/** A reload request collects one promise from every listening resource, so
 * the requester can tell when all of the page's data has been fetched again.
 * Resources refetch in place: the page stays mounted and keeps its drafts. */
interface ReloadDetail {
  pending: Promise<unknown>[];
}

export async function requestReload(target: EventTarget): Promise<void> {
  const detail: ReloadDetail = { pending: [] };
  target.dispatchEvent(new CustomEvent(reloadEvent, { detail }));
  await Promise.allSettled(detail.pending);
}

export function onReload(
  target: EventTarget,
  reload: () => Promise<unknown>,
): () => void {
  const listener = (event: Event) => {
    (event as CustomEvent<ReloadDetail>).detail.pending.push(reload());
  };
  target.addEventListener(reloadEvent, listener);
  return () => target.removeEventListener(reloadEvent, listener);
}
