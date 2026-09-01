package storage

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A vocabulary value carrying an apostrophe stays one value rather than
// closing its SQL literal early. No value in either vocabulary carries one
// today, which is exactly why this is worth pinning: the escaping is what
// keeps that from being something to remember when one does.
//
// It runs the rendered expression through SQLite rather than comparing it to
// expected SQL text, so it fails on a literal that does not parse as well as on
// one that ranks the wrong value.
func TestVocabularyRankEscapesItsLiterals(t *testing.T) {
	type quoted string
	rank := vocabularyRank("status", []quoted{"it's", "plain"})

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // an in-memory database has nothing to fail at

	var got int
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT `+rank+` FROM (SELECT 'it''s' AS status)`).Scan(&got))
	assert.Equal(t, 0, got, "the apostrophe is part of the value, not the end of the literal")
}
