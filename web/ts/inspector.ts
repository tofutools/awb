export type InspectorStatus = "open" | "in_progress" | "closed";
export type InspectorStatusAction = "none" | "close" | "claim" | "release" | "reopen";

interface InspectorRelation {
  type: string;
  other: string;
  direction: string;
}

/** Parent is the outgoing end of has-parent. Incoming has-parent relations are
 * children and remain part of the general relation list. */
export function inspectorParent(relations: InspectorRelation[]): string | undefined {
  return relations.find((relation) =>
    relation.type === "has-parent" && relation.direction === "out")?.other;
}

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

/** A native select can finish its activation after dispatching change. Opening
 * synchronously lets that activation light-dismiss the new popover on some
 * browsers, so the close editor waits for the next task. */
export function deferInspectorPopoverOpen(open: () => void): void {
  setTimeout(open);
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
