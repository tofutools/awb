import type { ComponentChildren, JSX } from "preact";
import { useState } from "preact/hooks";

import type { Activity } from "../api.js";
import {
  historyDiff,
  historyDiffPreview,
  type HistoryDiffPart,
} from "../history-diff.js";
import { activityValues } from "../presentation.js";
import { Button, Modal } from "./ui.js";

type ActivityChange = Activity["changes"][number];

function DiffParts({
  parts,
}: {
  parts: readonly HistoryDiffPart[];
}): JSX.Element {
  const children: ComponentChildren[] = parts.map((part, index) => {
    if (part.kind === "same") return part.text;
    if (part.kind === "omitted") {
      return (
        <span
          class="history-diff-omitted"
          aria-label="omitted content"
          key={`${part.kind}-${index}`}
        >
          {part.text}
        </span>
      );
    }

    const content = (
      <>
        <span class="visually-hidden">
          {part.kind === "remove" ? "Removed: " : "Added: "}
        </span>
        <span class="history-diff-indicator" aria-hidden="true">
          {part.kind === "remove" ? "−" : "+"}
        </span>
        {part.text}
      </>
    );
    return part.kind === "remove" ? (
      <del class="history-diff-remove" key={`${part.kind}-${index}`}>
        {content}
      </del>
    ) : (
      <ins class="history-diff-add" key={`${part.kind}-${index}`}>
        {content}
      </ins>
    );
  });
  return <>{children}</>;
}

function FullDiff({
  field,
  parts,
  onClose,
}: {
  field: string;
  parts: readonly HistoryDiffPart[];
  onClose: () => void;
}): JSX.Element {
  return (
    <Modal
      title={`${field} change`}
      className="history-diff-dialog"
      onClose={onClose}
    >
      <header class="history-diff-dialog-header">
        <p class="muted">
          Full source diff. − marks removed text, + marks added text, and
          unchanged text provides context.
        </p>
      </header>
      <pre class="history-diff-full" tabIndex={0}>
        <DiffParts parts={parts} />
      </pre>
      <footer class="history-diff-dialog-footer">
        <Button
          class="primary-button history-diff-close"
          autofocus
          onClick={onClose}
        >
          Close
        </Button>
      </footer>
    </Modal>
  );
}

/** One activity field change. String edits get a compact source diff whose
 * button name exposes the same removal/addition semantics without relying on
 * colour. Other values retain the ordinary before/after presentation. */
export function HistoryChange({
  change,
}: {
  change: ActivityChange;
}): JSX.Element {
  const [open, setOpen] = useState(false);

  if (
    typeof change.from === "string" &&
    typeof change.to === "string" &&
    change.from !== change.to
  ) {
    const parts = historyDiff(change.from, change.to);
    return (
      <>
        <span class="activity-field">{change.field}</span>
        <button
          type="button"
          class="history-diff-preview"
          title={`View full ${change.field} diff`}
          onClick={() => setOpen(true)}
        >
          <span class="visually-hidden">View full {change.field} diff. </span>
          <DiffParts parts={historyDiffPreview(parts)} />
        </button>
        {open && (
          <FullDiff
            field={change.field}
            parts={parts}
            onClose={() => setOpen(false)}
          />
        )}
      </>
    );
  }

  const [from, to] = activityValues(change.from, change.to);
  return (
    <>
      <span class="activity-field">{change.field}</span>
      <code>{from}</code>
      <span class="activity-arrow">→</span>
      <code>{to}</code>
    </>
  );
}
