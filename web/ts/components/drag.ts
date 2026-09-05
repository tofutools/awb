import { useEffect, useLayoutEffect, useState } from "preact/hooks";
const interactive =
  "a, button, input, select, textarea, label, [contenteditable], [role='button']";
/** Native dragging must not steal link activation or text selection. */
export function useDragSurface() {
  const [wide, setWide] = useState(() => matchMedia("(min-width: 701px)").matches);
  useEffect(() => {
    const media = matchMedia("(min-width: 701px)");
    const update = () => setWide(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  const [interacting, setInteracting] = useState(false);
  useLayoutEffect(() => {
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
    draggable: wide && !interacting,
    onPointerDown: (event: PointerEvent) =>
      setInteracting((event.target as Element).closest(interactive) !== null),
  };
}
