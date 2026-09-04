// Resolving a vendored browser bundle by name.
//
// The bundles under web/static/vendor/ are version-stamped, so every upgrade
// changes their filenames. Tests therefore find one by its prefix rather than
// spelling out a version that web/ts/vendor/rebuild.sh would have to come back
// and edit — and the exactly-one check means a stale bundle left beside its
// replacement fails the tests instead of being silently picked up.

import { readdirSync } from "node:fs";

const vendorDir = new URL("../../static/vendor/", import.meta.url);

/** vendorBundle returns the import URL of the vendored bundle called name. */
export function vendorBundle(name) {
  const matches = readdirSync(vendorDir)
    .filter((file) => file.startsWith(`${name}-`) && file.endsWith(".js"));
  if (matches.length !== 1) {
    throw new Error(`expected exactly one ${name}-*.js in web/static/vendor, found ${matches.length}`);
  }
  return new URL(matches[0], vendorDir).href;
}
