// Rewrites the import map in web/static/index.html to the bare-specifier →
// bundle mapping given on the command line, as "specifier=url" pairs.
//
// The vendored bundle filenames carry their version, so every bump changes them
// and the import map is the one place the shipped UI names them. rebuild.sh
// therefore rewrites it rather than printing a reminder to.
//
// Invoked only by rebuild.sh. Reads and writes files; no network.

import { readFileSync, writeFileSync } from "node:fs";

const [indexHtml, ...pairs] = process.argv.slice(2);

const imports = Object.fromEntries(pairs.map((pair) => {
  const at = pair.indexOf("=");
  if (at < 0) throw new Error(`expected specifier=url, got ${pair}`);
  return [pair.slice(0, at), pair.slice(at + 1)];
}));

const html = readFileSync(indexHtml, "utf8");

// Anchored on the element rather than on its contents, so a malformed or
// hand-edited map is replaced wholesale instead of silently left alone.
const importMap = /(<script type="importmap">\n)[\s\S]*?(\n<\/script>)/;
if (!importMap.test(html)) {
  throw new Error(`${indexHtml}: no <script type="importmap"> element to rewrite`);
}

writeFileSync(indexHtml, html.replace(importMap, `$1${JSON.stringify({ imports })}$2`));
