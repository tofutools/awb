// Package internal exists only to carry the code generation directive below.
//
// openapi.yaml, at the repository root, is the source of truth for the HTTP
// API: the Go server in internal/api is generated from it by ogen, and the
// TypeScript types in web/ts/api/types.ts by openapi-typescript (see
// build.sh). Neither is committed, and neither is ever edited by hand.
package internal

//go:generate ogen --config .ogen.yml --target ./api --clean --package api ../openapi.yaml
