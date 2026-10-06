import type { Diagnostic, SyntaxTree } from "codemirror";
import { issueMetadata } from "./issue-create.js";

/** The editor uses the same validation as submit, including optional empty input. */
export function metadataDiagnostics(raw: string, tree?: SyntaxTree): Diagnostic[] {
  try {
    issueMetadata(raw);
    return [];
  } catch (error) {
    // Syntax errors point to the parser's first error; value-level errors cover the object.
    let range: { from: number; to: number } | undefined;
    tree?.iterate({
      enter(node) {
        if (!range && node.type.isError) {
          range = { from: node.from, to: Math.min(raw.length, Math.max(node.to, node.from + 1)) };
        }
      },
    });
    return [{
      from: range?.from ?? 0,
      to: range?.to ?? raw.length,
      severity: "error",
      message: error instanceof Error ? error.message : String(error),
    }];
  }
}
