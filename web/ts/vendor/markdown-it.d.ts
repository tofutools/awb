// Minimal declarations for the committed markdown-it bundle. Only the surface
// awb uses is declared; the bundle itself is the pre-built ESM artifact under
// web/static/vendor/.
declare module "markdown-it" {
  export interface Options {
    html?: boolean;
    linkify?: boolean;
    breaks?: boolean;
    typographer?: boolean;
  }

  export interface Token {
    type: string;
    tag: string;
    attrs: [string, string][] | null;
    content: string;
    children: Token[] | null;
    attrJoin(name: string, value: string): void;
    attrSet(name: string, value: string): void;
  }

  export interface TokenConstructor {
    new (type: string, tag: string, nesting: number): Token;
  }

  export interface StateCore {
    tokens: Token[];
    Token: TokenConstructor;
  }

  export interface Core {
    ruler: {
      push(name: string, fn: (state: StateCore) => void): void;
    };
  }

  export default class MarkdownIt {
    constructor(options?: Options);
    core: Core;
    render(src: string): string;
  }
}
