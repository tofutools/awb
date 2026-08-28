#!/usr/bin/env bash
# Full build: generate the code openapi.yaml specifies, compile the frontend,
# build the binary, then test and lint.
# Prerequisites on $PATH: go, ogen, openapi-typescript, tsc, golangci-lint.
#
# NOTE: no npm/npx/yarn/pnpm/bun — the browser vendor bundles under
# web/static/vendor/ are pre-built committed artifacts.
#
# On success this script is silent (no stdout/stderr) and exits 0.
# On failure it prints the failing step's output to stderr and exits non-zero.
set -euo pipefail

OUTPUT_DIR="."
while getopts "o:" opt; do
  case $opt in
    o) OUTPUT_DIR="$OPTARG" ;;
    \?) echo "Invalid option: -$OPTARG" >&2; exit 1 ;;
  esac
done

cd "$(dirname "$0")"

# Run a build step silently; on failure, print its combined output to stderr
# and abort with a non-zero exit code.
run() {
  local output
  if ! output=$("$@" 2>&1); then
    printf 'build.sh: step failed: %s\n' "$*" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
}

# 1. Generate the Go server from openapi.yaml into internal/api/ (the directive
#    is in internal/generate.go).
run go generate ./...

# 2. Generate the TypeScript types from the same document.
run openapi-typescript openapi.yaml -o web/ts/api-types.ts

# 3. Compile the TypeScript frontend into web/static/.
run tsc --project web/ts/tsconfig.json

# 4. Run the frontend tests.
run node --test 'web/ts/tests/*.test.mjs'

# 5. Build the single binary; the frontend is embedded via web/embed.go.
run env CGO_ENABLED=0 go build -trimpath -buildvcs=true \
  -o "$OUTPUT_DIR/awb" .

# 6. Run the Go tests.
run go test ./...

# 7. Lint.
run golangci-lint run ./...
