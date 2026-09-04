// Writes the PROVENANCE and LICENSE files that accompany one vendored bundle.
//
// The list of packages is read back out of esbuild's metafile rather than
// maintained by hand, so it describes the bundle that was actually produced:
// every module esbuild pulled in, at the version installed when it did. A
// transitive dependency that appears or disappears upstream therefore shows up
// in the committed attribution the next time rebuild.sh runs.
//
// Invoked only by rebuild.sh. Reads and writes files; no network.

import { readFileSync, readdirSync, writeFileSync } from "node:fs";

const [metaFile, modulesDir, bundleName, provenanceOut, licenseOut] = process.argv.slice(2);

const meta = JSON.parse(readFileSync(metaFile, "utf8"));

/** packageOf maps an esbuild input path to the npm package that owns it. */
function packageOf(input) {
  const marker = "node_modules/";
  const at = input.lastIndexOf(marker);
  if (at < 0) return null;
  const rest = input.slice(at + marker.length).split("/");
  return rest[0].startsWith("@") ? `${rest[0]}/${rest[1]}` : rest[0];
}

const packages = [...new Set(Object.keys(meta.inputs).map(packageOf).filter((p) => p !== null))]
  .sort();
if (packages.length === 0) {
  throw new Error(`${metaFile}: no node_modules inputs — nothing to attribute`);
}

const versions = new Map(
  packages.map((p) => [p, JSON.parse(readFileSync(`${modulesDir}/${p}/package.json`, "utf8")).version]),
);

writeFileSync(
  provenanceOut,
  `The ${bundleName} ESM bundle was built by web/ts/vendor/rebuild.sh with\n` +
    `esbuild, from these npm packages:\n\n` +
    packages.map((p) => `${p} ${versions.get(p)}\n`).join("") +
    `\nTheir license texts are in ${licenseOut.split("/").pop()}.\n`,
);

// One section per package, in the same order, each naming what it covers. The
// texts are not deduplicated even when identical: a license has to stay
// attached to the copyright holder it was granted by.
const sections = [];
for (const pkg of packages) {
  const dir = `${modulesDir}/${pkg}`;
  const files = readdirSync(dir).filter((f) => /^(LICENSE|LICENCE|COPYING)/i.test(f)).sort();
  if (files.length === 0) {
    throw new Error(`${pkg} ships no license file — vendoring it would drop its terms`);
  }
  for (const file of files) {
    sections.push(
      `${pkg} ${versions.get(pkg)} (${file})\n` +
        "=".repeat(72) + "\n\n" +
        readFileSync(`${dir}/${file}`, "utf8").trimEnd() + "\n",
    );
  }
}

writeFileSync(
  licenseOut,
  `Licenses of the packages bundled into ${bundleName}.\n\n` + sections.join("\n\n"),
);
