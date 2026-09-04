// Writes the PROVENANCE and LICENSE files that accompany one vendored bundle.
//
// The list of packages is read back out of esbuild's metafile rather than
// maintained by hand, so it describes the bundle that was actually produced:
// every module esbuild pulled in, at the version installed when it did. A
// transitive dependency that appears or disappears upstream therefore shows up
// in the committed attribution the next time rebuild.sh runs.
//
// A package is identified by the directory it was loaded from rather than by
// its name. npm hoists what it can, but a conflicting requirement leaves a
// second copy nested under its dependent, and that copy is a different release
// under a different copyright year. Keying on the name alone would collapse the
// two and then attribute one of them to the other's version and license.
//
// Invoked only by rebuild.sh. Reads and writes files; no network.

import { readFileSync, readdirSync, writeFileSync } from "node:fs";

const [metaFile, baseDir, bundleName, provenanceOut, licenseOut] = process.argv.slice(2);

const meta = JSON.parse(readFileSync(metaFile, "utf8"));

/**
 * packageRootOf maps an esbuild input path to the directory of the package that
 * owns it, relative to the directory esbuild ran in, or null when the input is
 * not from a package at all — which is what the generated entry file is.
 */
function packageRootOf(input) {
  const marker = "node_modules/";
  const at = input.lastIndexOf(marker);
  if (at < 0) return null;
  const start = at + marker.length;
  const segments = input.slice(start).split("/");
  const depth = segments[0].startsWith("@") ? 2 : 1;
  if (segments.length <= depth) return null;
  return input.slice(0, start) + segments.slice(0, depth).join("/");
}

const roots = [...new Set(Object.keys(meta.inputs).map(packageRootOf).filter((root) => root !== null))];
if (roots.length === 0) {
  throw new Error(`${metaFile}: no node_modules inputs — nothing to attribute`);
}

// Sorted by name and then version so the generated files are stable across
// rebuilds; the root path is the tiebreaker for the same release reached two
// ways, which reads as one entry either way.
const packages = roots
  .map((root) => {
    const manifest = JSON.parse(readFileSync(`${baseDir}/${root}/package.json`, "utf8"));
    return { root, name: manifest.name, version: manifest.version };
  })
  .sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));

writeFileSync(
  provenanceOut,
  `The ${bundleName} ESM bundle was built by web/ts/vendor/rebuild.sh with\n` +
    `esbuild, from these npm packages:\n\n` +
    packages.map((pkg) => `${pkg.name} ${pkg.version}\n`).join("") +
    `\nTheir license texts are in ${licenseOut.split("/").pop()}.\n`,
);

// One section per package, in the same order, each naming what it covers. The
// texts are not deduplicated even when identical: a license has to stay
// attached to the copyright holder it was granted by.
const sections = [];
for (const pkg of packages) {
  const dir = `${baseDir}/${pkg.root}`;
  const files = readdirSync(dir).filter((file) => /^(LICENSE|LICENCE|COPYING)/i.test(file)).sort();
  if (files.length === 0) {
    throw new Error(`${pkg.name} ${pkg.version} ships no license file — vendoring it would drop its terms`);
  }
  for (const file of files) {
    sections.push(
      `${pkg.name} ${pkg.version} (${file})\n` +
        "=".repeat(72) + "\n\n" +
        readFileSync(`${dir}/${file}`, "utf8").trimEnd() + "\n",
    );
  }
}

writeFileSync(
  licenseOut,
  `Licenses of the packages bundled into ${bundleName}.\n\n` + sections.join("\n\n"),
);
