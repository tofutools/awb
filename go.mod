module github.com/tofutools/awb

go 1.25.0

// TEMPORARY: awb needs three additions to go-server-common that are not in
// v1.8.1 — sqlite.MigrateStrict, csrf.MiddlewareOrigins and
// auth.LoadHtpasswdStrict. Drop this once they are released upstream.
replace github.com/mikaelstaldal/go-server-common => ../go-server-common

require (
	github.com/mikaelstaldal/go-server-common v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.12.1
	github.com/yuin/goldmark v1.8.5
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.45.0 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.52.0 // indirect
)
