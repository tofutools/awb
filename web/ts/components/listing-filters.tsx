import type { ComponentChildren } from "preact";
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
import { Autocomplete } from "./autocomplete.js";
import { Button, Popover } from "./ui.js";

export interface DynamicFilterChoice {
  value: string;
  label: string;
  detail?: string;
}

/** DynamicFilterRow keeps long facet vocabularies out of the listing while
 * preserving selected values as removable, shareable URL state. */
export function DynamicFilterRow({
  route,
  title,
  name,
  choices,
  selected,
  trailing,
}: {
  route: Route;
  title: string;
  name: "label" | "epic";
  choices: DynamicFilterChoice[];
  selected: string[];
  trailing?: ComponentChildren;
}) {
  const [draft, setDraft] = useState("");
  const routeState = route.query.toString();
  useEffect(() => setDraft(""), [routeState]);

  const choice = (value: string) =>
    choices.find((item) => item.value === value);
  const available = choices.filter((item) => !selected.includes(item.value));
  const add = (value: string): void => {
    const query =
      name === "epic"
        ? withEpicSelection(route.query, value)
        : new URLSearchParams(route.query);
    if (name === "label") {
      query.delete("page");
      query.append("label", value);
    }
    setDraft("");
    location.hash = routeHref(route, query);
  };
  const remove = (value: string): void => {
    const query =
      name === "epic"
        ? withEpicSelection(route.query, null)
        : new URLSearchParams(route.query);
    if (name === "label") {
      query.delete("label");
      query.delete("page");
      for (const item of selected.filter((item) => item !== value))
        query.append("label", item);
    }
    location.hash = routeHref(route, query);
  };

  return (
    <div
      class={`facet-group dynamic-filter-group dynamic-filter-${name} ${trailing ? "with-pagination" : ""}`}
    >
      <span class="facet-title">{title}</span>
      <div class="dynamic-filter-values">
        <span class="facet-values dynamic-filter-selected">
          {selected.map((value) => {
            const item = choice(value);
            const display =
              item?.label ??
              (name === "epic" ? "Unavailable epic" : `#${value}`);
            return (
              <span class="editable-chip" key={value} title={item?.detail}>
                <span class="facet active">{display}</span>
                <Button
                  class="chip-remove"
                  aria-label={`Remove ${name} ${value}`}
                  onClick={() => remove(value)}
                >
                  ×
                </Button>
              </span>
            );
          })}
        </span>
        <Popover
          label={
            name === "label" ? "Add label filter" : "Choose epic filter"
          }
          panelLabel={
            name === "label" ? "Add label filter" : "Choose epic filter"
          }
          className="dynamic-filter-trigger"
          panelClassName="inspector-popover dynamic-filter-popover"
          align="start"
          buttonLabel={name === "label" ? "+ Add label" : "+ Choose epic"}
        >
          <Autocomplete
            value={draft}
            onValue={setDraft}
            onSuggestion={(item) => add(item.value)}
            aria-label={name === "label" ? "Search labels" : "Search epics"}
            placeholder={name === "label" ? "Search labels…" : "Search epics…"}
            load={async (query) => {
              const match = query.toLocaleLowerCase();
              return available
                .filter((item) =>
                  `${item.label} ${item.value} ${item.detail ?? ""}`
                    .toLocaleLowerCase()
                    .includes(match),
                )
                .map((item) => ({
                  value: item.value,
                  label: item.label,
                  detail: item.detail,
                }));
            }}
          />
        </Popover>
      </div>
      {trailing}
    </div>
  );
}

export function ListingFilters({ route }: { route: Route }) {
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
    <div class="listing-selection-controls">
      <span class="issue-view-control">
        <Popover
          label="Configure issue view"
          panelLabel="Issue view options"
          className="issue-view-button"
          panelClassName="issue-view-popover"
          buttonLabel={<>▾ View</>}
        >
          <strong class="issue-view-title">Statuses</strong>
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
    </div>
  );
}

export function EpicFilterRow({
  route,
  epics,
}: {
  route: Route;
  epics: Issue[];
}) {
  const selected = epicSelectionFrom(route.query);
  return (
    <DynamicFilterRow
      route={route}
      title="epic"
      name="epic"
      selected={selected === null ? [] : [selected]}
      choices={[
        { value: noEpicSelection, label: "No epic" },
        ...epics.map((epic) => ({
          value: epic.id,
          label: epic.title,
          detail: epic.id,
        })),
      ]}
    />
  );
}
