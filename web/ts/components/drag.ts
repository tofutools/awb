import { useEffect, useState } from "preact/hooks";
const interactive =
  "a, button, input, select, textarea, label, [contenteditable], [role='button']";
/** Native dragging must not steal link activation or text selection. */
export function useDragSurface() {
  const [interacting, setInteracting] = useState(false);
  useEffect(() => {
    if (!interacting) return;
    const release = () => setInteracting(false);
    window.addEventListener("pointerup", release);
    window.addEventListener("pointercancel", release);
    window.addEventListener("dragend", release);
    return () => {
      window.removeEventListener("pointerup", release);
      window.removeEventListener("pointercancel", release);
      window.removeEventListener("dragend", release);
    };
  }, [interacting]);
  return {
    draggable: !interacting,
    onPointerDown: (event: PointerEvent) =>
      setInteracting((event.target as Element).closest(interactive) !== null),
  };
}
