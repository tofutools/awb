import type { BoardView, Issue } from "../api.js";
import type { Route } from "../routing/route.js";
export function boardLaneCollapseKey(ref: string): string {
  return `awb.board.${ref}.collapsed-lanes`;
}

export function collapsedBoardLanes(ref: string): Set<string> {
  try {
    const stored: unknown = JSON.parse(
      localStorage.getItem(boardLaneCollapseKey(ref)) ?? "[]",
    );
    return new Set(
      Array.isArray(stored)
        ? stored.filter((value): value is string => typeof value === "string")
        : [],
    );
  } catch {
    return new Set();
  }
}

export function saveCollapsedBoardLanes(
  ref: string,
  workspaces: Set<string>,
): void {
  try {
    localStorage.setItem(
      boardLaneCollapseKey(ref),
      JSON.stringify([...workspaces].sort()),
    );
  } catch {
    /* presentation state is best-effort */
  }
}

export function boardHiddenEpicsKey(identity: string, ref: string): string {
  return `awb.board.${ref}.${identity}.hidden-epics`;
}

type HiddenBoardEpic = Pick<Issue, "id" | "title">;

export function hiddenBoardEpicEntries(
  identity: string,
  ref: string,
): HiddenBoardEpic[] {
  const validID = (value: string): boolean =>
    /^[a-z][a-z0-9-]*-[0-9a-f]{6}$/.test(value);
  const entries = new Map<string, HiddenBoardEpic>();
  try {
    const stored: unknown = JSON.parse(
      localStorage.getItem(boardHiddenEpicsKey(identity, ref)) ?? "[]",
    );
    if (!Array.isArray(stored)) return [];
    for (const value of stored) {
      if (typeof value === "string" && validID(value))
        entries.set(value, { id: value, title: "" });
      else if (value !== null && typeof value === "object") {
        const candidate = value as Partial<HiddenBoardEpic>;
        if (
          typeof candidate.id === "string" &&
          validID(candidate.id) &&
          typeof candidate.title === "string"
        ) {
          entries.set(candidate.id, {
            id: candidate.id,
            title: candidate.title,
          });
        }
      }
    }
  } catch {
    /* presentation state is best-effort */
  }
  return [...entries.values()].sort((left, right) =>
    left.id.localeCompare(right.id),
  );
}

export function hiddenBoardEpics(identity: string, ref: string): Set<string> {
  return new Set(
    hiddenBoardEpicEntries(identity, ref).map((entry) => entry.id),
  );
}

export function saveHiddenBoardEpics(
  identity: string,
  ref: string,
  epics: Set<string>,
  added?: HiddenBoardEpic,
): void {
  const entries = new Map(
    hiddenBoardEpicEntries(identity, ref).map((entry) => [entry.id, entry]),
  );
  if (added !== undefined)
    entries.set(added.id, { id: added.id, title: added.title });
  const stored = [...epics]
    .sort()
    .map((id) => entries.get(id) ?? { id, title: "" });
  try {
    localStorage.setItem(
      boardHiddenEpicsKey(identity, ref),
      JSON.stringify(stored),
    );
  } catch {
    /* presentation state is best-effort */
  }
}

const defaultBoardCardPageSize = 8;
const defaultBoardClosedDaysFallback = 30;
const boardLabelLikePattern = /^[a-z0-9._/-]+$/;

type DefaultBoardPreferences = Pick<
  BoardView,
  | "all_workspaces"
  | "workspaces"
  | "all_epics"
  | "epics"
  | "include_no_epic"
  | "labels"
  | "assignees"
  | "priority_max"
  | "card_limit"
  | "closed_days"
  | "epic_closed_days"
>;

export function defaultBoardPreferencesKey(identity: string): string {
  return `awb.board.default.${identity}.view`;
}

export function defaultBoardPreferences(
  identity: string,
): DefaultBoardPreferences {
  let legacyClosedDays = defaultBoardClosedDaysFallback;
  try {
    const stored = localStorage.getItem(
      `awb.board.default.${identity}.closed-days`,
    );
    if (stored !== null) {
      const value = Number(stored);
      if (Number.isInteger(value) && value >= 0 && value <= 3650)
        legacyClosedDays = value;
    }
  } catch {
    /* use the fallback */
  }
  const fallback: DefaultBoardPreferences = {
    all_workspaces: true,
    workspaces: [],
    all_epics: true,
    epics: [],
    include_no_epic: true,
    labels: [],
    assignees: [],
    priority_max: 4,
    card_limit: defaultBoardCardPageSize,
    closed_days: legacyClosedDays,
    epic_closed_days: 0,
  };
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(defaultBoardPreferencesKey(identity)) ?? "null",
    );
    if (parsed === null || typeof parsed !== "object") return fallback;
    const value = parsed as Partial<DefaultBoardPreferences>;
    const strings = (candidate: unknown): string[] =>
      Array.isArray(candidate)
        ? candidate.filter((item): item is string => typeof item === "string")
        : [];
    const priority = Number(value.priority_max);
    const cardLimit = Number(value.card_limit);
    const closedDays = Number(value.closed_days);
    const epicClosedDays = Number(value.epic_closed_days);
    return {
      all_workspaces:
        typeof value.all_workspaces === "boolean"
          ? value.all_workspaces
          : fallback.all_workspaces,
      workspaces: strings(value.workspaces).filter(
        (item) => item.length <= 16 && /^[a-z][a-z0-9-]*$/.test(item),
      ),
      all_epics:
        typeof value.all_epics === "boolean"
          ? value.all_epics
          : fallback.all_epics,
      epics: strings(value.epics).filter((item) =>
        /^[a-z][a-z0-9-]*-[0-9a-f]{6}$/.test(item),
      ),
      include_no_epic:
        typeof value.include_no_epic === "boolean"
          ? value.include_no_epic
          : fallback.include_no_epic,
      labels: strings(value.labels).filter(
        (item) => item.length <= 64 && boardLabelLikePattern.test(item),
      ),
      assignees: strings(value.assignees).filter(
        (item) => item.length <= 64 && boardLabelLikePattern.test(item),
      ),
      priority_max:
        Number.isInteger(priority) && priority >= 0 && priority <= 4
          ? (priority as 0 | 1 | 2 | 3 | 4)
          : fallback.priority_max,
      card_limit:
        Number.isInteger(cardLimit) && cardLimit >= 1 && cardLimit <= 50
          ? cardLimit
          : fallback.card_limit,
      closed_days:
        Number.isInteger(closedDays) && closedDays >= 0 && closedDays <= 3650
          ? closedDays
          : fallback.closed_days,
      epic_closed_days:
        Number.isInteger(epicClosedDays) &&
        epicClosedDays >= 0 &&
        epicClosedDays <= 3650
          ? epicClosedDays
          : fallback.epic_closed_days,
    };
  } catch {
    return fallback;
  }
}

export function saveDefaultBoardPreferences(
  identity: string,
  value: DefaultBoardPreferences,
): void {
  try {
    localStorage.setItem(
      defaultBoardPreferencesKey(identity),
      JSON.stringify(value),
    );
  } catch {
    /* preference is best-effort */
  }
}

export function defaultBoardView(identity: string): BoardView {
  return {
    id: "default",
    name: "Default board",
    owner: identity,
    shared: false,
    ...defaultBoardPreferences(identity),
    created_at: "",
    updated_at: "",
  };
}

export function effectiveDefaultBoardView(
  identity: string,
  route: Route,
): BoardView {
  const view = defaultBoardView(identity);
  const workspaces = route.query.getAll("workspace");
  return workspaces.length === 0
    ? view
    : { ...view, all_workspaces: false, workspaces };
}
