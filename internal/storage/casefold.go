package storage

import (
	"database/sql/driver"

	"golang.org/x/text/cases"
	"modernc.org/sqlite"
)

const caseFoldSQL = "awb_casefold"

func init() {
	// SQLite's built-in lower() only folds ASCII. Listing filters promise
	// case-insensitive matching for user-authored Unicode text, so expose Go's
	// Unicode case folding to every connection opened by the sqlite driver.
	sqlite.MustRegisterDeterministicScalarFunction(caseFoldSQL, 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return nil, nil
			}
			return cases.Fold().String(args[0].(string)), nil
		})
}
