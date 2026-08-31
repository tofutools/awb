export type InspectorStatus = "open" | "in_progress" | "closed";
export type InspectorStatusAction = "none" | "close" | "claim" | "release" | "reopen";

/** Status is presented as one native control, but each change remains one of
 * the domain transitions that keeps status and assignees consistent. */
export function inspectorStatusAction(
  current: InspectorStatus,
  target: InspectorStatus,
): InspectorStatusAction {
  if (current === target) return "none";
  if (target === "closed") return "close";
  if (target === "in_progress") return "claim";
  return current === "closed" ? "reopen" : "release";
}

export interface InspectorRect {
  top: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

/** Place a top-layer editor against its trigger, flipping above when the
 * viewport has no room below and always retaining an edge gutter. */
export function inspectorPopoverPosition(
  anchor: InspectorRect,
  popover: Pick<InspectorRect, "width" | "height">,
  viewport: { width: number; height: number; left?: number; top?: number },
  gap = 6,
  gutter = 8,
): { left: number; top: number } {
  const viewportLeft = viewport.left ?? 0;
  const viewportTop = viewport.top ?? 0;
  const viewportRight = viewportLeft + viewport.width;
  const viewportBottom = viewportTop + viewport.height;
  const left = Math.max(
    viewportLeft + gutter,
    Math.min(viewportRight - popover.width - gutter, anchor.right - popover.width),
  );
  const below = anchor.bottom + gap;
  const top = below + popover.height <= viewportBottom - gutter
    ? below
    : Math.max(viewportTop + gutter, anchor.top - popover.height - gap);
  return { left, top };
}
