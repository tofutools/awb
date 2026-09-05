import { useEffect, useId, useRef, useState } from "preact/hooks";
import type { JSX } from "preact";
import {
  autocompleteKeyAction,
  nextActiveIndex,
  SuggestionSearch,
  type Suggestion,
  type SuggestionState,
} from "../autocomplete.js";
export function Autocomplete({
  load,
  value,
  onValue,
  ...props
}: Omit<JSX.InputHTMLAttributes<HTMLInputElement>, "value" | "onInput"> & {
  load: (query: string, signal: AbortSignal) => Promise<Suggestion[]>;
  value: string;
  onValue: (value: string) => void;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [rows, setRows] = useState<Suggestion[]>([]);
  const [status, setStatus] = useState<SuggestionState>("idle");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const loader = useRef(load);
  loader.current = load;
  const search = useRef<SuggestionSearch>();
  useEffect(() => {
    const next = new SuggestionSearch(
      (query, signal) => loader.current(query, signal),
      (state, rows) => {
        setStatus(state);
        setRows(rows);
        setActive(-1);
      },
    );
    search.current = next;
    return () => next.close();
  }, []);
  const select = (row: Suggestion) => {
    onValue(row.value);
    setOpen(false);
    search.current?.close();
    input.current?.focus();
  };
  return (
    <div class="autocomplete">
      <input
        {...props}
        ref={input}
        value={value}
        role="combobox"
        aria-autocomplete="list"
        aria-controls={id}
        aria-expanded={open && status !== "idle"}
        aria-activedescendant={
          open && active >= 0 ? `${id}-${active}` : undefined
        }
        onInput={(e) => {
          onValue(e.currentTarget.value);
          setOpen(true);
          search.current?.query(e.currentTarget.value);
        }}
        onFocus={() => {
          setOpen(true);
          search.current?.query(value);
        }}
        onBlur={() => setOpen(false)}
        onKeyDown={(e) => {
          const action = autocompleteKeyAction(
            e.key,
            open,
            active,
            rows.length,
          );
          if (["next", "previous", "select", "dismiss"].includes(action))
            e.preventDefault();
          if (action === "next" || action === "previous")
            setActive(
              nextActiveIndex(active, rows.length, action === "next" ? 1 : -1),
            );
          if (action === "select" && rows[active]) select(rows[active]);
          if (action === "dismiss") {
            setOpen(false);
            e.stopPropagation();
          }
        }}
      />
      <div
        class="autocomplete-list"
        id={id}
        role="listbox"
        hidden={!open || status === "idle"}
      >
        {rows.map((row, index) => (
          <div
            key={row.value}
            id={`${id}-${index}`}
            class={`autocomplete-option ${active === index ? "active" : ""}`}
            role="option"
            aria-selected={active === index}
            onPointerDown={(e) => {
              e.preventDefault();
              select(row);
            }}
          >
            <span>{row.label}</span>
            {row.detail && <small>{row.detail}</small>}
          </div>
        ))}
        {rows.length === 0 && (
          <div class="autocomplete-message" role="status">
            {status === "loading"
              ? "Loading…"
              : status === "error"
                ? "Suggestions unavailable"
                : "No matches"}
          </div>
        )}
      </div>
    </div>
  );
}
