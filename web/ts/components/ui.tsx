import { Icon } from "./icon.js";
import {
  createContext,
  render,
  type ComponentChildren,
  type JSX,
  type Ref,
} from "preact";
import {
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useId,
  useState,
} from "preact/hooks";
import { renderMarkdown } from "../markdown.js";
export { MarkdownInput } from "./markdown-input.js";
import { initialFor, relativeTime } from "../presentation.js";
import {
  formatUpdated,
  readUpdatedDisplay,
  rememberUpdatedDisplay,
  updatedStorage,
} from "../updated.js";
import {
  pageNumber,
  pageSizeFrom,
  pageSizeStorage,
  rememberedPageSize,
  pageWindow,
  pageSizes,
  rememberPageSize,
  withPage,
  withPageSize,
  listingFilterMaxLength,
} from "../listings.js";
import {
  preferenceStorage,
  readPaginationAutoHide,
  showPagination,
} from "../preferences.js";
import { confirmationDecision } from "../keyboard.js";
import { routeHref, type Route } from "../routing/route.js";

export interface AppContextValue {
  identity: string;
  mayManageUsers: boolean;
  refreshCaller: () => Promise<void>;
  notify: (message: string, error?: boolean) => void;
}
export const AppContext = createContext<AppContextValue>({
  identity: "",
  mayManageUsers: false,
  refreshCaller: async () => {},
  notify: () => {},
});
export const useApp = () => useContext(AppContext);

/** Data refreshes retain the mounted component and its drafts. A late request
 * cannot replace newer data or write into an unmounted route. */
export function useResource<T>(load: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const loader = useRef(load);
  loader.current = load;
  const generation = useRef(0);
  const reload = async () => {
    const current = ++generation.current;
    try {
      const next = await loader.current();
      if (current === generation.current) {
        setData(next);
        setError(undefined);
      }
    } catch (failure) {
      if (current === generation.current) setError(failure);
    }
  };
  useEffect(() => {
    void reload();
    return () => {
      generation.current++;
    };
  }, deps);
  return { data, error, reload };
}
export function useMutation() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const running = useRef(false);
  const run = async (operation: () => Promise<unknown>) => {
    if (running.current) return false;
    running.current = true;
    setBusy(true);
    setError(undefined);
    try {
      await operation();
      return true;
    } catch (failure) {
      setError(failure);
      return false;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { busy, error, run };
}
export function Button({
  children,
  buttonRef,
  ...props
}: JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  return (
    <button type="button" class="secondary-button" ref={buttonRef} {...props}>
      {children}
    </button>
  );
}
export function Field({
  label,
  children,
}: {
  label: string;
  children: ComponentChildren;
}) {
  return (
    <label class="edit-field">
      <span class="edit-field-label">{label}</span>
      {children}
    </label>
  );
}
export function ErrorMessage({ error }: { error: unknown }) {
  return error ? (
    <p class="edit-error" role="alert">
      {error instanceof Error ? error.message : String(error)}
    </p>
  ) : null;
}
export function Loading() {
  return (
    <p class="route-loading" role="status">
      Loading…
    </p>
  );
}
export function Markdown({
  text,
  className = "",
}: {
  text: string;
  className?: string;
}) {
  return (
    <div
      class={`markdown ${className}`}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(text) }}
    />
  );
}
export function NameLink({
  href,
  id,
  title,
}: {
  href: string;
  id: string;
  title: string;
}) {
  return (
    <a href={href} class="name">
      <span class="id">{id}</span>
      {title && <span class="title">{title}</span>}
    </a>
  );
}
export function Avatar({
  name,
  className = "",
}: {
  name: string;
  className?: string;
}) {
  return (
    <span class={`avatar ${className}`} aria-hidden="true">
      {initialFor(name)}
    </span>
  );
}
export function Time({ timestamp }: { timestamp: string }) {
  return (
    <time class="timestamp" dateTime={timestamp} title={timestamp}>
      {relativeTime(timestamp)}
    </time>
  );
}
function useUpdatedDisplay() {
  const [display, setDisplay] = useState(() =>
    readUpdatedDisplay(updatedStorage(window)),
  );
  useEffect(() => {
    const refresh = () =>
      setDisplay(readUpdatedDisplay(updatedStorage(window)));
    window.addEventListener("awb:updated", refresh);
    return () => window.removeEventListener("awb:updated", refresh);
  }, []);
  return display;
}
export function UpdatedTime({ timestamp }: { timestamp: string }) {
  const display = useUpdatedDisplay();
  return (
    <time
      class="timestamp"
      dateTime={timestamp}
      title={timestamp}
      data-updated-timestamp={timestamp}
    >
      {formatUpdated(timestamp, display)}
    </time>
  );
}
export function UpdatedDisplayControl() {
  const display = useUpdatedDisplay();
  return (
    <span class="updated-display-control">
      <Popover
        label="Choose how Updated is displayed"
        panelLabel="Updated display format"
        className="updated-display-button"
        panelClassName="updated-display-popover"
        buttonLabel={
          <>
            <Icon name="clock" />
            <span class="updated-display-chevron">▾</span>
          </>
        }
      >
        <strong class="updated-display-title">Show as</strong>
        {(
          [
            ["relative", "Time since change", "2h 18m ago"],
            ["date", "Date", "2026-08-30"],
            ["datetime", "Date & time", "2026-08-30 16:42"],
          ] as const
        ).map(([value, label, example]) => (
          <label class="updated-display-option" key={value}>
            <input
              type="radio"
              checked={display === value}
              onChange={(e) => {
                rememberUpdatedDisplay(updatedStorage(window), value);
                window.dispatchEvent(new Event("awb:updated"));
                e.currentTarget
                  .closest<HTMLElement>("[popover]")
                  ?.hidePopover();
              }}
            />
            {label}
            <code>{example}</code>
          </label>
        ))}
      </Popover>
    </span>
  );
}
export function SearchInput({
  value,
  onInput,
  placeholder = "Filter…",
  className = "listing-filter",
}: {
  value: string;
  onInput: (value: string) => void;
  placeholder?: string;
  className?: string;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <div class={`search-control ${className}`}>
      <input
        type="search"
        ref={input}
        value={value}
        maxLength={listingFilterMaxLength}
        placeholder={placeholder}
        aria-label={placeholder}
        onInput={(e) => onInput(e.currentTarget.value)}
      />
      <Button
        class="search-clear"
        aria-label="Clear filter"
        title="Clear filter"
        hidden={!value}
        onClick={() => {
          onInput("");
          input.current?.focus();
        }}
      >
        ×
      </Button>
    </div>
  );
}
export function listingPageSize(query: URLSearchParams) {
  return pageSizeFrom(query, rememberedPageSize(pageSizeStorage(window)));
}
export function Pagination({ route, total }: { route: Route; total: number }) {
  const size = listingPageSize(route.query);
  const page = pageWindow(total, pageNumber(route.query), size);
  const item = (label: string, target: number, disabled: boolean) => (
    <a
      class="pagination-link"
      href={
        disabled ? undefined : routeHref(route, withPage(route.query, target))
      }
      aria-disabled={disabled || undefined}
    >
      {label}
    </a>
  );
  return (
    <div
      class="pagination"
      role="navigation"
      aria-label="Pagination"
      hidden={
        !showPagination(
          total,
          readPaginationAutoHide(preferenceStorage(window)),
        )
      }
    >
      {item("First", 1, page.page === 1)}
      {item("Previous", page.page - 1, page.page === 1)}
      <span class="pagination-status">
        {total === 0 ? "0 of 0" : `${page.first}–${page.last} of ${total}`}
      </span>
      {item("Next", page.page + 1, page.page === page.pages)}
      {item("Last", page.pages, page.page === page.pages)}
      <label class="pagination-size">
        <select
          aria-label="Rows per page"
          value={size}
          onChange={(e) => {
            const next = Number(e.currentTarget.value);
            rememberPageSize(pageSizeStorage(window), next);
            location.hash = routeHref(route, withPageSize(route.query, next));
          }}
        >
          {pageSizes.map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
export function Modal({
  title,
  onClose,
  children,
  className = "",
}: {
  title: string;
  onClose: () => void;
  children: ComponentChildren;
  className?: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  useLayoutEffect(() => {
    const active = document.activeElement;
    dialog.current!.showModal();
    return () => {
      dialog.current?.close();
      if (active instanceof HTMLElement && active.isConnected)
        active.focus({ preventScroll: true });
    };
  }, []);
  return (
    <dialog
      ref={dialog}
      class={className}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        close.current();
      }}
    >
      <h2>{title}</h2>
      {children}
    </dialog>
  );
}
export function confirmMutation(
  title: string,
  description: string,
  trigger?: HTMLElement,
  destructive = false,
): Promise<boolean> {
  const host = document.createElement("div");
  document.body.append(host);
  const active = document.activeElement;
  return new Promise((resolve) => {
    const done = (answer: boolean) => {
      render(null, host);
      host.remove();
      const focus = active instanceof HTMLElement ? active : trigger;
      if (focus?.isConnected) focus.focus({ preventScroll: true });
      resolve(answer);
    };
    render(
      <Modal
        title={title}
        className="confirmation-dialog"
        onClose={() => done(false)}
      >
        <div
          onKeyDown={(e) => {
            const decision = confirmationDecision(e);
            if (decision) {
              e.preventDefault();
              done(decision === "confirm");
            }
          }}
        >
          <p class="confirmation-description">{description}</p>
          <div class="confirmation-actions">
            <span class="confirmation-shortcut-hint">Enter: Yes · Esc: No</span>
            <Button autofocus onClick={() => done(false)}>
              No
            </Button>
            <Button
              class={destructive ? "danger-button" : "primary-button"}
              onClick={() => done(true)}
            >
              Yes
            </Button>
          </div>
        </div>
      </Modal>,
      host,
    );
  });
}

export function Popover({
  label,
  children,
  buttonLabel = "+",
  className = "inspector-add",
  panelClassName = "inspector-popover",
  panelLabel,
}: {
  label: string;
  children: ComponentChildren;
  buttonLabel?: ComponentChildren;
  className?: string;
  panelClassName?: string;
  panelLabel?: string;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const node = panel.current!;
    node.showPopover();
    const reposition = () => {
      const anchor = trigger.current!.getBoundingClientRect();
      const bounds = node.getBoundingClientRect();
      node.style.left = `${Math.max(8, Math.min(innerWidth - bounds.width - 8, anchor.right - bounds.width))}px`;
      node.style.top = `${Math.max(8, Math.min(innerHeight - bounds.height - 8, anchor.bottom + 6))}px`;
      node.style.maxHeight = `${Math.max(0, innerHeight - 16)}px`;
    };
    reposition();
    node
      .querySelector<HTMLElement>("input,select,button")
      ?.focus({ preventScroll: true });
    const close = () => {
      if (!node.matches(":popover-open")) setOpen(false);
    };
    node.addEventListener("toggle", close);
    const navigate = () => setOpen(false);
    window.addEventListener("hashchange", navigate);
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    const observer = new ResizeObserver(reposition);
    observer.observe(node);
    return () => {
      node.removeEventListener("toggle", close);
      window.removeEventListener("hashchange", navigate);
      node.hidePopover();
      observer.disconnect();
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [open]);
  return (
    <>
      <Button
        buttonRef={trigger}
        class={className}
        aria-label={label}
        aria-haspopup="dialog"
        aria-controls={id}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {buttonLabel}
      </Button>
      {open && (
        <div
          ref={panel}
          popover="auto"
          role="dialog"
          id={id}
          aria-label={panelLabel ?? label}
          class={panelClassName}
        >
          {children}
        </div>
      )}
    </>
  );
}
