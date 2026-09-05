import { useDragSurface } from "./drag.js";
import { useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { api, type Issue } from "../api.js";
import { inspectorParent } from "../inspector.js";
import { listingParentTitle, listingRelationshipRole } from "../listings.js";
import {
  NameLink,
  UpdatedTime,
  UpdatedDisplayControl,
  useApp,
  useMutation,
  ErrorMessage,
} from "./ui.js";
export type ListingKind = "issues" | "ready" | "blocked";
export function Badge({
  className = "",
  children,
}: {
  className?: string;
  children: ComponentChildren;
}) {
  return <span class={`listing-badge ${className}`}>{children}</span>;
}
export function IssueBadges({ issue }: { issue: Issue }) {
  const { identity } = useApp();
  return (
    <span class="badges">
      <Badge className={`priority p${issue.priority}`}>P{issue.priority}</Badge>
      <Badge className={`status status-${issue.status}`}>{issue.status}</Badge>
      {issue.blocked && <Badge className="blocked">blocked</Badge>}
      {issue.assignees.map((name) => (
        <Badge
          key={name}
          className={name === identity ? "assignee mine" : "assignee"}
        >
          @{name}
        </Badge>
      ))}
      {issue.labels.map((label) => (
        <Badge key={label} className="label">
          #{label}
        </Badge>
      ))}
    </span>
  );
}
export const issueSortKeys = [
  "id",
  "type",
  "priority",
  "status",
  "assignee",
  "updated",
  "created",
  "order",
  "blockers",
];
export function issueColumns(kind: ListingKind, children = false) {
  if (children)
    return [
      ["id", "Issue"],
      ["type", "Type"],
      ["priority", "Priority"],
      ["status", "Status"],
      ["assignee", "Assignees"],
    ];
  const base = [
    ["id", "Issue"],
    ["type", "Type"],
    ["parent", "Parent"],
    ["priority", "Priority"],
  ];
  return kind === "ready"
    ? [...base, ["updated", "Updated"]]
    : kind === "blocked"
      ? [...base, ["assignee", "Assignees"], ["blockers", "Blocked by"]]
      : [
          ...base,
          ["status", "Status"],
          ["assignee", "Assignees"],
          ["updated", "Updated"],
        ];
}
export function IssueTable({
  issues,
  kind = "issues",
  sortKey = "order",
  direction = "asc",
  onSort,
  children = false,
  actions,
  onReload,
  mutable = true,
}: {
  issues: Issue[];
  kind?: ListingKind;
  sortKey?: string;
  direction?: string;
  onSort?: (key: string) => void;
  children?: boolean;
  actions?: (issue: Issue) => ComponentChildren;
  onReload?: () => Promise<void>;
  mutable?: boolean;
}) {
  const { identity } = useApp();
  const surface = useDragSurface();
  const [hovered, setHovered] = useState<{ id: string; parent: string } | null>(
    null,
  );
  const [focused, setFocused] = useState<{ id: string; parent: string } | null>(
    null,
  );
  const family = hovered ?? focused;
  const [drag, setDrag] = useState<Issue | null>(null);
  const [target, setTarget] = useState<{ id: string; after: boolean } | null>(
    null,
  );
  const mutation = useMutation();
  const columns = issueColumns(kind, children);
  if (actions) columns.push(["actions", ""]);
  const cell = (issue: Issue, key: string) => {
    if (key === "id")
      return (
        <span class="issue-name-cell">
          <NameLink
            href={`#/issues/${issue.id}`}
            id={issue.id}
            title={issue.title}
          />
          {issue.labels.length > 0 && (
            <span class="row-labels">
              {issue.labels.map((label) => (
                <span class="row-label" key={label}>
                  #{label}
                </span>
              ))}
            </span>
          )}
          <span class="listing-family-marker" aria-hidden="true" />
        </span>
      );
    if (key === "parent") {
      const relation = issue.relations.find(
        (r) => r.type === "has-parent" && r.direction === "out",
      );
      if (!relation) return <span class="muted">—</span>;
      const parent = relation.other;
      const title = relation.other_title ?? "";
      const full = title ? `${title} (${parent})` : parent;
      return (
        <a
          class="parent-link"
          href={`#/issues/${parent}`}
          title={full}
          aria-label={`Parent ${full}`}
          onPointerEnter={() => setHovered({ id: issue.id, parent })}
          onPointerLeave={() => setHovered(null)}
          onFocus={() => setFocused({ id: issue.id, parent })}
          onBlur={() => setFocused(null)}
        >
          <span class="parent-marker">↳</span>
          {title ? listingParentTitle(title) : parent}
        </a>
      );
    }
    if (key === "type") return <Badge className="type">{issue.type}</Badge>;
    if (key === "priority")
      return (
        <Badge className={`priority p${issue.priority}`}>
          P{issue.priority}
        </Badge>
      );
    if (key === "status")
      return (
        <Badge className={`status status-${issue.status}`}>
          {issue.status}
        </Badge>
      );
    if (key === "assignee")
      return issue.assignees.length ? (
        <span class="badges">
          {issue.assignees.map((name) => (
            <Badge
              key={name}
              className={name === identity ? "assignee mine" : "assignee"}
            >
              @{name}
            </Badge>
          ))}
        </span>
      ) : (
        <span class="muted">—</span>
      );
    if (key === "updated") return <UpdatedTime timestamp={issue.updated_at} />;
    if (key === "blockers")
      return (
        <span class="blocker-list">
          {issue.blockers.length
            ? issue.blockers.join(", ")
            : issue.blocked
              ? "hidden work"
              : ""}
        </span>
      );
    return actions?.(issue);
  };
  return (
    <>
      <ErrorMessage error={mutation.error} />
      <table
        class={`listing-table issue-table ${children ? "child-issues-table" : `listing-${kind}`}`}
      >
        <thead>
          <tr>
            {columns.map(([key, label]) => (
              <th
                key={key}
                class={`listing-col-${key}`}
                scope="col"
                aria-sort={
                  sortKey === key
                    ? direction === "asc"
                      ? "ascending"
                      : "descending"
                    : undefined
                }
              >
                <div class="column-heading">
                  {key === "parent" || key === "actions" ? (
                    <span class="column-label">{label}</span>
                  ) : (
                    <button
                      type="button"
                      class="sort-button"
                      onClick={() => onSort?.(key)}
                    >
                      {label}
                      {sortKey === key ? (
                        <span class="sort-arrow">
                          {direction === "asc" ? "↑" : "↓"}
                        </span>
                      ) : null}
                    </button>
                  )}
                  {key === "updated" && <UpdatedDisplayControl />}
                </div>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {issues.map((issue) => {
            const role = family
              ? listingRelationshipRole(
                  issue.id,
                  inspectorParent(issue.relations),
                  family.id,
                  family.parent,
                )
              : null;
            const reorder =
              mutable &&
              !!onReload &&
              sortKey === "order" &&
              direction === "asc";
            return (
              <tr
                key={issue.id}
                data-issue={issue.id}
                class={`${role ? `listing-family-${role}` : ""} ${drag?.id === issue.id ? "dragging" : ""} ${target?.id === issue.id ? (target.after ? "drop-after" : "drop-before") : ""}`}
                draggable={reorder && surface.draggable}
                onPointerDown={surface.onPointerDown}
                onDragStart={(e) => {
                  if (
                    (e.target as HTMLElement).closest(
                      "a,button,input,select,textarea",
                    )
                  ) {
                    e.preventDefault();
                    return;
                  }
                  setDrag(issue);
                  e.dataTransfer?.setData("text/plain", issue.id);
                }}
                onDragOver={(e) => {
                  if (
                    drag &&
                    drag.id !== issue.id &&
                    drag.workspace === issue.workspace
                  ) {
                    e.preventDefault();
                    setTarget({
                      id: issue.id,
                      after:
                        e.clientY >
                        e.currentTarget.getBoundingClientRect().top +
                          e.currentTarget.offsetHeight / 2,
                    });
                  }
                }}
                onDragEnd={() => {
                  setDrag(null);
                  setTarget(null);
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const moving = drag;
                  setDrag(null);
                  setTarget(null);
                  if (moving && moving.id !== issue.id) {
                    const after =
                      e.clientY >
                      e.currentTarget.getBoundingClientRect().top +
                        e.currentTarget.offsetHeight / 2;
                    void mutation.run(async () => {
                      const current = await api.issue(moving.id);
                      await api.moveIssue(moving.id, {
                        status: current.status,
                        ...(after ? { after: issue.id } : { before: issue.id }),
                      });
                      await onReload?.();
                    });
                  }
                }}
              >
                {columns.map(([key, label]) => (
                  <td key={key} class={`listing-col-${key}`} data-label={label}>
                    {cell(issue, key)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}
