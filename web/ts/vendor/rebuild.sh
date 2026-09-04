#!/usr/bin/env bash
# Maintainer-only script. Rebuilds every vendored browser resource under
# web/static/vendor/ from upstream npm releases: it installs the versions
# package.json pins, bundles each library into one self-contained ESM file with
# esbuild, regenerates that bundle's LICENSE and PROVENANCE files from the
# packages that actually went into it, and rewrites the import map in
# web/static/index.html to name the new files.
#
# NOT part of build.sh, Taskfile.yml or CI, and it must not become part of any
# of them: the bundles are committed artifacts precisely so that building awb
# needs no package manager and reaches no network. Run this by hand when a
# vendored library is added or updated, then commit what it wrote.
#
# To update a library, change its version in package.json and run this script.
#
# Bundle filenames are version-stamped (markdown-it-15.0.1.js), so a file's name
# records the upstream release it was built from and a bump changes the name
# rather than overwriting. The import map is rewritten from those names, so the
# only thing left by hand is the version in package.json.
#
# npm only ever touches the throwaway node_modules in this directory, installed
# with --ignore-scripts so no install-time lifecycle script runs.
#
# Requires on $PATH: npm, node, esbuild.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

VENDOR_DIR="$(pwd)"
OUT_DIR="$VENDOR_DIR/../../static/vendor"
INDEX_HTML="$VENDOR_DIR/../../static/index.html"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# Reconcile package-lock.json with package.json first — a no-op producing no
# diff when they are already in sync, and what adds the new entries after a
# version bump. `npm ci` alone aborts on an out-of-sync lock.
npm install --package-lock-only --ignore-scripts
npm ci --ignore-scripts

# The version stamped into a bundle's filename, read from the freshly installed
# tree. CodeMirror is a composite of several independently versioned packages;
# its bundle is stamped with the core @codemirror/view.
pkgver() { node -p "require('$VENDOR_DIR/node_modules/$1/package.json').version"; }

CODEMIRROR_VER="$(pkgver @codemirror/view)"
MARKDOWNIT_VER="$(pkgver markdown-it)"
DOMPURIFY_VER="$(pkgver dompurify)"

# The symbol surface of each bundle. Keep these minimal: what is exported here
# is what ends up in the shipped file, and the matching web/ts/vendor/*.d.ts is
# what the frontend is allowed to use.
#
# The CodeMirror set is web/ts/markdown-editor.ts's dynamic import plus the
# EditorState its test drives. Syntax highlighting is @lezer/highlight's
# classHighlighter rather than @codemirror/language's defaultHighlightStyle
# because the former only tags spans with stable `tok-*` class names and ships
# no colours of its own, leaving the whole palette to app.css and the theme.
cat > "$WORK_DIR/codemirror-entry.mjs" <<'EOF'
export { EditorView, keymap } from "@codemirror/view";
export { EditorState } from "@codemirror/state";
export { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
export { syntaxHighlighting } from "@codemirror/language";
export { classHighlighter } from "@lezer/highlight";
export { markdown } from "@codemirror/lang-markdown";
EOF

cat > "$WORK_DIR/markdown-it-entry.mjs" <<'EOF'
import MarkdownIt from "markdown-it";
export default MarkdownIt;
EOF

cat > "$WORK_DIR/dompurify-entry.mjs" <<'EOF'
import DOMPurify from "dompurify";
export default DOMPurify;
EOF

# The entry files live in $WORK_DIR, so esbuild's upward search for node_modules
# never reaches the install tree in this directory. Point it there explicitly.
export NODE_PATH="$VENDOR_DIR/node_modules"

mkdir -p "$OUT_DIR"

# Drop the previously vendored bundles, whatever they were stamped with, so a
# superseded version cannot linger in the tree next to its replacement.
rm -f "$OUT_DIR"/{codemirror,markdown-it,dompurify}-*.js

# bundle NAME VERSION — build one ESM bundle and write the LICENSE and
# PROVENANCE that go with it.
#
# Which packages a bundle contains is not written down anywhere by hand: it is
# read back out of esbuild's metafile, so the two attribution files describe the
# file that was actually produced and cannot drift from it as a dependency
# closure changes upstream.
bundle() {
  local name="$1" version="$2"
  local meta="$WORK_DIR/$name-meta.json"

  esbuild "$WORK_DIR/$name-entry.mjs" \
    --bundle --format=esm --platform=browser --minify \
    --metafile="$meta" \
    --outfile="$OUT_DIR/$name-$version.js"

  node "$VENDOR_DIR/gen-attribution.mjs" \
    "$meta" "$VENDOR_DIR" \
    "$name-$version.js" \
    "$OUT_DIR/$name-PROVENANCE.txt" \
    "$OUT_DIR/$name-LICENSE.txt"
}

bundle codemirror  "$CODEMIRROR_VER"
bundle markdown-it "$MARKDOWNIT_VER"
bundle dompurify   "$DOMPURIFY_VER"

# The import map is the one place the version-stamped filenames are referenced
# by name, so rewrite it here rather than leaving a bump half-applied. The
# frontend tests and internal/cli/serve_test.go resolve the bundles by prefix
# and need no edit.
node "$VENDOR_DIR/gen-importmap.mjs" "$INDEX_HTML" \
  "codemirror=./vendor/codemirror-$CODEMIRROR_VER.js" \
  "markdown-it=./vendor/markdown-it-$MARKDOWNIT_VER.js" \
  "dompurify=./vendor/dompurify-$DOMPURIFY_VER.js"

cat <<EOF

Wrote to web/static/vendor/:
  codemirror-$CODEMIRROR_VER.js
  markdown-it-$MARKDOWNIT_VER.js
  dompurify-$DOMPURIFY_VER.js
plus each one's -LICENSE.txt and -PROVENANCE.txt, and the import map in
web/static/index.html. Run ./build.sh, then commit.
EOF
