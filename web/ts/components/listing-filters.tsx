import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import type { Issue } from "../api.js";
import {
  epicSelectionFrom,
  initialFilterSuggestions,
  noEpicSelection,
  rankEpicFilterSuggestions,
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
import {
  defaultIssueTypes,
  issueTypeLabel,
  issueTypeVocabulary,
  selectedIssueTypes,
  withIssueTypes,
  type IssueTypeValue,
} from "../type-filter.js";
import { Autocomplete } from "./autocomplete.js";
import { Button, Popover } from "./ui.js";

export interface DynamicFilterChoice {
  value: string;
  label: string;
  detail?: string;
}

type DynamicFilterName = "label" | "epic" | "assignee";

const dynamicFilterCopy: Record<
  DynamicFilterName,
  { label: string; button: string; search: string; placeholder: string }
> = {
  label: {
    label: "Add label filter",
    button: "+ Add label",
    search: "Search labels",
    placeholder: "Search labels…",
  },
  epic: {
    label: "Choose epic filter",
    button: "+ Choose epic",
    search: "Search epics",
    placeholder: "Search epics…",
  },
  assignee: {
    label: "Add assignee filter",
    button: "+ Add assignee",
    search: "Search assignees",
    placeholder: "Search assignees…",
  },
};

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
  name: DynamicFilterName;
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
    let query: URLSearchParams;
    if (name === "epic") {
      query = withEpicSelection(route.query, value);
    } else {
      query = new URLSearchParams(route.query);
      query.delete("page");
      query.append(name, value);
    }
    setDraft("");
    location.hash = routeHref(route, query);
  };
  const remove = (value: string): void => {
    let query: URLSearchParams;
    if (name === "epic") {
      query = withEpicSelection(route.query, null);
    } else {
      query = new URLSearchParams(route.query);
      query.delete(name);
      query.delete("page");
      for (const item of selected.filter((item) => item !== value))
        query.append(name, item);
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
              (name === "epic"
                ? "Unavailable epic"
                : `${name === "label" ? "#" : "@"}${value}`);
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
          label={dynamicFilterCopy[name].label}
          panelLabel={dynamicFilterCopy[name].label}
          className="dynamic-filter-trigger"
          panelClassName="inspector-popover dynamic-filter-popover"
          align="start"
          buttonLabel={dynamicFilterCopy[name].button}
        >
          <Autocomplete
            value={draft}
            onValue={setDraft}
            onSuggestion={(item) => add(item.value)}
            onDismiss={() => setDraft("")}
            suggestOnEmpty
            aria-label={dynamicFilterCopy[name].search}
            placeholder={dynamicFilterCopy[name].placeholder}
            load={async (query) => {
              const match = query.toLocaleLowerCase();
              const matches = available
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
              return initialFilterSuggestions(query, matches);
            }}
          />
        </Popover>
      </div>
      {trailing}
    </div>
  );
}

export function ListingFilters({ route }: { route: Route }) {
  const [stagedStatuses, setStagedStatuses] = useState<IssueStatusValue[]>(
    selectedIssueStatuses(route.query),
  );
  const [stagedTypes, setStagedTypes] = useState<IssueTypeValue[]>(
    selectedIssueTypes(route.query),
  );
  const firstChoice = useRef<HTMLInputElement>(null);
  const routeState = route.query.toString();

  useEffect(() => {
    setStagedStatuses(selectedIssueStatuses(route.query));
    setStagedTypes(selectedIssueTypes(route.query));
  }, [routeState]);

  const toggleStatus = (status: IssueStatusValue, checked: boolean): void => {
    setStagedStatuses((current) =>
      issueStatusVocabulary.filter((candidate) =>
        candidate === status ? checked : current.includes(candidate),
      ),
    );
  };
  const toggleType = (type: IssueTypeValue, checked: boolean): void => {
    setStagedTypes((current) =>
      issueTypeVocabulary.filter((candidate) =>
        candidate === type ? checked : current.includes(candidate),
      ),
    );
  };
  const statusHelp =
    stagedStatuses.length === 0
      ? "No statuses selected; the list will be empty."
      : `${stagedStatuses.length} status${stagedStatuses.length === 1 ? "" : "es"} selected.`;
  const typeHelp =
    stagedTypes.length === 0
      ? "No types selected; the list will be empty."
      : `${stagedTypes.length} type${stagedTypes.length === 1 ? "" : "s"} selected.`;

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
          <div class="issue-view-choices" role="group" aria-label="Statuses">
            {issueStatusVocabulary.map((status, index) => (
              <label class="issue-view-option" key={status}>
                <input
                  ref={index === 0 ? firstChoice : undefined}
                  type="checkbox"
                  value={status}
                  data-status={status}
                  checked={stagedStatuses.includes(status)}
                  onChange={(event) =>
                    toggleStatus(status, event.currentTarget.checked)
                  }
                />
                {issueStatusLabel(status)}
              </label>
            ))}
          </div>
          <p class="issue-view-help" aria-live="polite">
            {statusHelp}
          </p>
          <strong class="issue-view-section-title">Types</strong>
          <div class="issue-view-choices" role="group" aria-label="Types">
            {issueTypeVocabulary.map((type) => (
              <label class="issue-view-option" key={type}>
                <input
                  type="checkbox"
                  value={type}
                  data-type={type}
                  checked={stagedTypes.includes(type)}
                  onChange={(event) =>
                    toggleType(type, event.currentTarget.checked)
                  }
                />
                {issueTypeLabel(type)}
              </label>
            ))}
          </div>
          <p class="issue-view-help" aria-live="polite">
            {typeHelp}
          </p>
          <div class="issue-view-actions">
            <Button
              class="secondary-button"
              onClick={() => {
                setStagedStatuses([...defaultIssueStatuses]);
                setStagedTypes([...defaultIssueTypes]);
                firstChoice.current?.focus();
              }}
            >
              Reset
            </Button>
            <Button
              class="primary-button"
              onClick={(event) => {
                const next = withIssueTypes(
                  withIssueStatuses(route.query, stagedStatuses),
                  stagedTypes,
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

export function EpicFilterRow({
  route,
  epics,
}: {
  route: Route;
  epics: Issue[];
}) {
  const selected = epicSelectionFrom(route.query);
  const ranked = rankEpicFilterSuggestions(epics);
  return (
    <DynamicFilterRow
      route={route}
      title="epic"
      name="epic"
      selected={selected === null ? [] : [selected]}
      choices={[
        { value: noEpicSelection, label: "No epic" },
        ...ranked.map((epic) => ({
          value: epic.id,
          label: epic.title,
          detail: epic.id,
        })),
      ]}
    />
  );
}
