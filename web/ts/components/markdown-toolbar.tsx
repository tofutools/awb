import { Fragment } from "preact";
import { useState } from "preact/hooks";
import { Button, Modal } from "./ui.js";
import {
  iconPaths,
  helpSections,
  helpNote,
  headingLevel,
  headingEdit,
  wrapEdit,
  linePrefixEdit,
  numberedListEdit,
  codeBlockEdit,
  tableEdit,
  ruleEdit,
  linkEdit,
  type EditorHost,
  type IconName,
  type MarkdownEdit,
} from "../markdown-toolbar.js";
export function MarkdownToolbar({
  host,
}: {
  host: EditorHost;
  revision: number;
}) {
  const [help, setHelp] = useState(false);
  const edit =
    (build: (doc: string, from: number, to: number) => MarkdownEdit) => () => {
      const { from, to } = host.selection();
      host.apply(build(host.doc(), from, to));
    };
  const control = (label: string, icon: IconName, run: () => void) => (
    <Button
      class="markdown-toolbar-button"
      title={label}
      aria-label={label}
      onMouseDown={(e) => e.preventDefault()}
      onClick={run}
    >
      <svg
        viewBox="0 0 24 24"
        aria-hidden="true"
        class="icon"
        dangerouslySetInnerHTML={{ __html: iconPaths[icon] }}
      />
    </Button>
  );
  const separator = (
    <span
      class="markdown-toolbar-separator"
      role="separator"
      aria-orientation="vertical"
    />
  );
  return (
    <>
      <div
        class="markdown-toolbar"
        role="group"
        aria-label="Markdown formatting"
      >
        <select
          class="markdown-toolbar-heading"
          title="Heading level"
          aria-label="Heading level"
          value={headingLevel(host.lineAt(host.selection().from))}
          onChange={(e) => {
            const { from, to } = host.selection();
            host.apply(
              headingEdit(host.doc(), from, to, Number(e.currentTarget.value)),
            );
          }}
        >
          {[0, 1, 2, 3, 4, 5, 6].map((level) => (
            <option key={level} value={level}>
              {level ? `Heading ${level}` : "Normal"}
            </option>
          ))}
        </select>
        {separator}
        {control(
          "Bold",
          "bold",
          edit((d, f, t) => wrapEdit(d, f, t, "**")),
        )}
        {control(
          "Italic",
          "italic",
          edit((d, f, t) => wrapEdit(d, f, t, "*")),
        )}
        {control(
          "Code",
          "code",
          edit((d, f, t) => wrapEdit(d, f, t, "`")),
        )}
        {control(
          "Strikethrough",
          "strikethrough",
          edit((d, f, t) => wrapEdit(d, f, t, "~~")),
        )}
        {separator}
        {control(
          "Bullet list",
          "bullet-list",
          edit((d, f, t) => linePrefixEdit(d, f, t, "- ")),
        )}
        {control("Numbered list", "numbered-list", edit(numberedListEdit))}
        {control(
          "Task list",
          "task-list",
          edit((d, f, t) => linePrefixEdit(d, f, t, "- [ ] ")),
        )}
        {separator}
        {control(
          "Blockquote",
          "quote",
          edit((d, f, t) => linePrefixEdit(d, f, t, "> ")),
        )}
        {control("Code block", "code-block", edit(codeBlockEdit))}
        {control("Table", "table", edit(tableEdit))}
        {control("Horizontal rule", "rule", edit(ruleEdit))}
        {separator}
        {control(
          "Link",
          "link",
          edit((d, f, t) => linkEdit(d, f, t, false)),
        )}
        {control(
          "Image",
          "image",
          edit((d, f, t) => linkEdit(d, f, t, true)),
        )}
        <span class="markdown-toolbar-spacer" />
        {control("Markdown help", "help", () => setHelp(true))}
      </div>
      {help && (
        <Modal
          title="Markdown syntax"
          className="markdown-help-dialog"
          onClose={() => setHelp(false)}
        >
          <div class="markdown-help-header">
            <Button autofocus onClick={() => setHelp(false)}>
              Close
            </Button>
          </div>
          <div class="markdown-help-body">
            {helpSections.map((section) => (
              <section class="markdown-help-section" key={section.title}>
                <h3>{section.title}</h3>
                <dl class="markdown-help-list">
                  {section.rows.map((row) => (
                    <Fragment key={row.syntax}>
                      <dt>
                        <code>{row.syntax}</code>
                      </dt>
                      <dd>{row.description}</dd>
                    </Fragment>
                  ))}
                </dl>
              </section>
            ))}
            <p class="markdown-help-note">{helpNote}</p>
          </div>
        </Modal>
      )}
    </>
  );
}
