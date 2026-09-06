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

export function ListingFilters({
  route,
  epics,
}: {
  route: Route;
  epics: Issue[];
}) {
  const selected = selectedIssueStatuses(route.query);
  const [staged, setStaged] = useState<IssueStatusValue[]>(selected);
  const selectedEpic = epicSelectionFrom(route.query);
  const [stagedEpic, setStagedEpic] = useState(selectedEpic ?? "");
  const firstChoice = useRef<HTMLInputElement>(null);
  const routeState = route.query.toString();

  useEffect(() => {
    setStaged(selectedIssueStatuses(route.query));
    setStagedEpic(epicSelectionFrom(route.query) ?? "");
  }, [routeState]);

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
  const unavailable =
    selectedEpic !== null &&
    selectedEpic !== noEpicSelection &&
    !epics.some((epic) => epic.id === selectedEpic);

  return (
    <div class="listing-selection-controls">
      <span class="issue-view-control">
        <Popover
          label="Configure issue view"
          panelLabel="Issue view options"
          className="issue-view-button"
          panelClassName="issue-view-popover"
          buttonLabel={<>▾ View</>}
        >
          <strong class="issue-view-title">Filters</strong>
          <label class="issue-view-field">
            Epic
            <select
              aria-label="Filter by epic"
              value={stagedEpic}
              onChange={(event) => setStagedEpic(event.currentTarget.value)}
            >
              <option value="">All</option>
              <option value={noEpicSelection}>No epic</option>
              {epics.map((epic) => (
                <option value={epic.id} key={epic.id}>
                  {epic.title} ({epic.id})
                </option>
              ))}
              {unavailable && (
                <option value={selectedEpic ?? ""} disabled>
                  Unavailable epic
                </option>
              )}
            </select>
          </label>
          <strong class="issue-view-section-title">Statuses</strong>
          <div class="issue-view-choices">
            {issueStatusVocabulary.map((status, index) => (
              <label class="issue-view-option" key={status}>
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
          <p class="issue-view-help" aria-live="polite">
            {help}
          </p>
          <div class="issue-view-actions">
            <Button
              class="secondary-button"
              onClick={() => {
                setStagedEpic("");
                setStaged([...defaultIssueStatuses]);
                firstChoice.current?.focus();
              }}
            >
              Reset
            </Button>
            <Button
              class="primary-button"
              onClick={(event) => {
                const statusQuery = withIssueStatuses(route.query, staged);
                const next = withEpicSelection(
                  statusQuery,
                  stagedEpic || null,
                );
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
    </div>
  );
}
