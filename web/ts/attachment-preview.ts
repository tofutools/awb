// Which attachments the issue page can preview in place, and as what.
//
// The server always answers attachment content as an opaque download (see
// GetAttachmentContent), so a preview never navigates to it. The page fetches
// the bytes itself and decides from the recorded metadata what they are:
// images are shown in an <img> — a raster image from a blob: URL, an SVG from a
// data: URL so that opening it in a tab of its own cannot run it at this
// origin (see useAttachmentContent); Markdown is rendered through the same
// sanitising renderer as a description; other text is shown as text. Anything
// else stays a download only.
//
// The stored content type is often a sniffed one — awb attach add without
// --content-type records `text/plain` for a Markdown file and `text/xml` for an
// SVG — so the name's extension decides wherever the type alone would not.

export type PreviewKind = "image" | "markdown" | "text";

export interface PreviewTarget {
  name: string;
  content_type: string;
  size: number;
}

/** The largest image the page fetches whole to show. */
export const MAX_IMAGE_PREVIEW = 25 * 1024 * 1024;
/**
 * The largest text or Markdown document the page fetches whole to show, and
 * the largest SVG, which is text inlined into a data: URL.
 */
export const MAX_TEXT_PREVIEW = 2 * 1024 * 1024;

const imageTypes: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  bmp: "image/bmp",
  svg: "image/svg+xml",
};

const markdownExtensions = new Set(["md", "markdown", "mdown", "mkd"]);

const textExtensions = new Set([
  "txt", "log", "csv", "tsv", "json", "jsonl", "ndjson", "yaml", "yml",
  "toml", "ini", "conf", "cfg", "xml", "html", "htm", "css", "diff", "patch",
  "sh", "bash", "zsh", "fish", "ps1", "bat", "go", "mod", "sum", "py", "rb",
  "rs", "js", "mjs", "cjs", "ts", "tsx", "jsx", "java", "kt", "c", "h", "cc",
  "cpp", "hpp", "cs", "swift", "sql", "proto", "graphql", "tf", "env",
]);

const textTypes = new Set([
  "application/json",
  "application/x-ndjson",
  "application/xml",
  "application/yaml",
  "application/x-yaml",
  "application/toml",
  "application/javascript",
  "application/sql",
]);

function extension(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
}

function mediaType(contentType: string): string {
  return contentType.split(";")[0]!.trim().toLowerCase();
}

/**
 * imageType is the image media type an attachment's bytes are shown as, or
 * undefined when it is not an image the page shows. It is what the preview
 * Blob is typed with, so it is only ever one of the types above.
 */
export function imageType(a: PreviewTarget): string | undefined {
  const type = mediaType(a.content_type);
  if (Object.values(imageTypes).includes(type)) return type;
  return imageTypes[extension(a.name)];
}

/** previewKind says how an attachment can be previewed, if at all. */
export function previewKind(a: PreviewTarget): PreviewKind | undefined {
  const image = imageType(a);
  if (image) {
    const limit = image === "image/svg+xml" ? MAX_TEXT_PREVIEW : MAX_IMAGE_PREVIEW;
    return a.size <= limit ? "image" : undefined;
  }
  if (a.size > MAX_TEXT_PREVIEW) return undefined;
  const type = mediaType(a.content_type);
  const ext = extension(a.name);
  if (type === "text/markdown" || markdownExtensions.has(ext)) return "markdown";
  if (type.startsWith("text/") || textTypes.has(type) || textExtensions.has(ext))
    return "text";
  return undefined;
}

/** attachmentContentHref is where an attachment's bytes are downloaded. */
export function attachmentContentHref(issue: string, name: string): string {
  return `api/issues/${encodeURIComponent(issue)}/attachments/${encodeURIComponent(name)}/content`;
}
