import { useEffect, useState } from "preact/hooks";
import type { Attachment } from "../api.js";
import {
  attachmentContentHref,
  imageType,
  previewKind,
  type PreviewKind,
} from "../attachment-preview.js";
import { Button, Markdown, Modal } from "./ui.js";

type Content =
  | { state: "loading" }
  | { state: "error"; message: string }
  | { state: "image"; url: string }
  | { state: "text"; text: string };

/**
 * useAttachmentContent fetches an attachment's bytes for a preview, and text is
 * decoded as UTF-8. A raster image becomes an object URL typed as the image it
 * is, revoked when the preview closes. An SVG becomes a data: URL instead: an
 * SVG is a document that can carry script, and while an <img> never runs it, a
 * viewer can open the image in a tab of its own. A blob: URL would run there at
 * this origin; a data: URL gets an opaque origin of its own.
 */
function useAttachmentContent(attachment: Attachment, kind: PreviewKind): Content {
  const [content, setContent] = useState<Content>({ state: "loading" });
  useEffect(() => {
    const abort = new AbortController();
    let url = "";
    void (async () => {
      try {
        const resp = await fetch(attachmentContentHref(attachment.issue, attachment.name), {
          signal: abort.signal,
        });
        if (!resp.ok) throw new Error(`The attachment could not be read (${resp.status}).`);
        const type = imageType(attachment);
        if (type === "image/svg+xml") {
          const svg = `data:${type};charset=utf-8,${encodeURIComponent(await resp.text())}`;
          if (!abort.signal.aborted) setContent({ state: "image", url: svg });
        } else if (kind === "image") {
          const blob = await resp.blob();
          url = URL.createObjectURL(new Blob([blob], { type }));
          if (abort.signal.aborted) URL.revokeObjectURL(url);
          else setContent({ state: "image", url });
        } else {
          const text = await resp.text();
          if (!abort.signal.aborted) setContent({ state: "text", text });
        }
      } catch (err) {
        if (!abort.signal.aborted)
          setContent({ state: "error", message: err instanceof Error ? err.message : String(err) });
      }
    })();
    return () => {
      abort.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [attachment.issue, attachment.name, kind]);
  return content;
}

function PreviewDialog({
  attachment,
  kind,
  onClose,
}: {
  attachment: Attachment;
  kind: PreviewKind;
  onClose: () => void;
}) {
  const content = useAttachmentContent(attachment, kind);
  const [source, setSource] = useState(false);
  const [broken, setBroken] = useState(false);
  let body;
  if (content.state === "loading") {
    body = <p class="attachment-preview-state" role="status">Loading…</p>;
  } else if (content.state === "error") {
    body = <p class="attachment-preview-state edit-error" role="alert">{content.message}</p>;
  } else if (content.state === "image") {
    body = broken ? (
      <p class="attachment-preview-state edit-error" role="alert">
        The file could not be shown as an image.
      </p>
    ) : (
      <img
        class="attachment-preview-image"
        src={content.url}
        alt={attachment.name}
        onError={() => setBroken(true)}
      />
    );
  } else if (kind === "markdown" && !source) {
    body = <Markdown text={content.text} className="attachment-preview-markdown" />;
  } else {
    body = <pre class="attachment-preview-text">{content.text}</pre>;
  }
  return (
    <Modal
      title={attachment.name}
      className="attachment-preview-dialog"
      onClose={onClose}
      dismissOnBackdrop
    >
      <div class="attachment-preview-stage" tabIndex={0} role="region" aria-label={attachment.name}>
        {body}
      </div>
      <footer class="attachment-preview-footer">
        {kind === "markdown" && (
          <div class="attachment-preview-modes" role="group" aria-label="Document view">
            <Button
              class="inline-button"
              aria-pressed={!source}
              onClick={() => setSource(false)}
            >
              Rendered
            </Button>
            <Button
              class="inline-button"
              aria-pressed={source}
              onClick={() => setSource(true)}
            >
              Source
            </Button>
          </div>
        )}
        <a
          class="attachment-preview-download"
          href={attachmentContentHref(attachment.issue, attachment.name)}
        >
          Download
        </a>
        <Button class="primary-button" autofocus onClick={onClose}>
          Close
        </Button>
      </footer>
    </Modal>
  );
}

/**
 * AttachmentPreview is the Preview control beside an attachment the page can
 * show in place, and the dialog it opens. It renders nothing for an attachment
 * that is a download only.
 */
export function AttachmentPreview({ attachment }: { attachment: Attachment }) {
  const [open, setOpen] = useState(false);
  const kind = previewKind(attachment);
  if (!kind) return null;
  return (
    <>
      <Button
        class="inline-button attachment-preview-button"
        aria-label={`Preview ${attachment.name}`}
        onClick={() => setOpen(true)}
      >
        Preview
      </Button>
      {open && (
        <PreviewDialog attachment={attachment} kind={kind} onClose={() => setOpen(false)} />
      )}
    </>
  );
}
