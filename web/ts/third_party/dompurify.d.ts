// Minimal declarations for the committed DOMPurify bundle.
declare module "dompurify" {
  export interface Config {
    ALLOWED_TAGS?: string[];
    ALLOWED_ATTR?: string[];
    ALLOW_DATA_ATTR?: boolean;
    ADD_ATTR?: string[];
  }

  interface DOMPurify {
    sanitize(dirty: string, config?: Config): string;
    addHook(entryPoint: string, hook: (node: Element) => void): void;
  }

  const purify: DOMPurify;
  export default purify;
}
