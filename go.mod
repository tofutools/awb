module github.com/tofutools/awb

go 1.25.0

// TEMPORARY: awb needs three additions to go-server-common that are not in
// v1.8.1 — sqlite.MigrateStrict, csrf.MiddlewareOrigins and
// auth.LoadHtpasswdStrict. Drop this once they are released upstream.
replace github.com/mikaelstaldal/go-server-common => ../go-server-common

require (
	github.com/stretchr/testify v1.12.1
	github.com/yuin/goldmark v1.8.5
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect
