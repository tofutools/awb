import type { Diagnostic } from "codemirror";
import { issueMetadata } from "./issue-create.js";

/** The editor uses the same validation as submit, including optional empty input. */
export function metadataDiagnostics(raw: string): Diagnostic[] {
  try {
    issueMetadata(raw);
    return [];
  } catch (error) {
    return [{
      from: 0,
      to: raw.length,
      severity: "error",
      message: error instanceof Error ? error.message : String(error),
    }];
  }
}
