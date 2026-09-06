import { useEffect, useRef, useState } from "preact/hooks";

import type { Issue } from "../api.js";
import {
  epicSelectionFrom,
  noEpicSelection,
  withEpicSelection,
} from "../listings.js";
import { routeHref, type Route } from "../routing/route.js";
import {
  defaultIssueStatuses,
  issueStatusLabel,
  issueStatusVocabulary,
  selectedIssueStatuses,
  withIssueStatuses,
  type IssueStatusValue,
} from "../status-filter.js";
import { Button, Popover } from "./ui.js";

function StatusFilter({ route }: { route: Route }) {
  const selected = selectedIssueStatuses(route.query);
  const [staged, setStaged] = useState<IssueStatusValue[]>(selected);
  const firstChoice = useRef<HTMLInputElement>(null);
  const routeState = route.query.toString();

  useEffect(() => setStaged(selectedIssueStatuses(route.query)), [routeState]);

  const toggle = (status: IssueStatusValue, checked: boolean): void => {
    setStaged((current) =>
      issueStatusVocabulary.filter((candidate) =>
        candidate === status ? checked : current.includes(candidate),
      ),
    );
  };
  const help =
    staged.length === 0
      ? "No statuses selected; the list will be empty."
      : `${staged.length} status${staged.length === 1 ? "" : "es"} selected.`;

  return (
    <span class="status-filter-control">
      <Popover
        label="Choose visible issue statuses"
        panelLabel="Visible issue statuses"
        className="status-filter-button"
        panelClassName="status-filter-popover"
        buttonLabel={<>Statuses · {selected.length} ▾</>}
      >
        <strong class="status-filter-title">Visible statuses</strong>
        <div class="status-filter-choices">
          {issueStatusVocabulary.map((status, index) => (
            <label class="status-filter-option" key={status}>
              <input
                ref={index === 0 ? firstChoice : undefined}
                type="checkbox"
                value={status}
                data-status={status}
                checked={staged.includes(status)}
                onChange={(event) =>
                  toggle(status, event.currentTarget.checked)
                }
              />
              {issueStatusLabel(status)}
            </label>
          ))}
        </div>
        <p class="status-filter-help" aria-live="polite">
          {help}
        </p>
        <div class="status-filter-actions">
          <Button
            class="secondary-button"
            onClick={() => {
              setStaged([...defaultIssueStatuses]);
              firstChoice.current?.focus();
            }}
          >
            Reset
          </Button>
          <Button
            class="primary-button"
            onClick={(event) => {
              const next = withIssueStatuses(route.query, staged);
              event.currentTarget
                .closest<HTMLElement>("[popover]")
                ?.hidePopover();
              const href = routeHref(route, next);
              if (href !== location.hash) location.hash = href;
            }}
          >
            Done
          </Button>
        </div>
      </Popover>
    </span>
  );
}

function EpicFilter({ route, epics }: { route: Route; epics: Issue[] }) {
  const selected = epicSelectionFrom(route.query);
  const unavailable =
    selected !== null &&
    selected !== noEpicSelection &&
    !epics.some((epic) => epic.id === selected);

  return (
    <label class="epic-filter-control">
      Epic
      <select
        aria-label="Filter by epic"
        value={selected ?? ""}
        onChange={(event) => {
          const epic = event.currentTarget.value || null;
          location.hash = routeHref(
            route,
            withEpicSelection(route.query, epic),
          );
        }}
      >
        <option value="">All</option>
        <option value={noEpicSelection}>No epic</option>
        {epics.map((epic) => (
          <option value={epic.id} key={epic.id}>
            {epic.title} ({epic.id})
          </option>
        ))}
        {unavailable && (
          <option value={selected ?? ""} disabled>
            Unavailable epic
          </option>
        )}
      </select>
    </label>
  );
}

export function ListingFilters({
  route,
  epics,
}: {
  route: Route;
  epics: Issue[];
}) {
  return (
    <div class="listing-selection-controls">
      <EpicFilter route={route} epics={epics} />
      <StatusFilter route={route} />
    </div>
  );
}
