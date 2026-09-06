package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaDocumentIsCurrent keeps the readable schema snapshot in sync with
// the schema produced by every migration. SQLite creates the FTS shadow tables,
// sqlite_sequence and automatic indexes itself, so they are not part of the
// application-owned DDL in the document.
func TestSchemaDocumentIsCurrent(t *testing.T) {
	db, err := Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	rows, err := db.SQL().QueryContext(t.Context(), `
		SELECT sql
		FROM sqlite_schema
		WHERE sql IS NOT NULL
		  AND name NOT LIKE 'sqlite_%'
		  AND (type <> 'table' OR name = 'issues_fts' OR name NOT GLOB 'issues_fts_*')
		ORDER BY rowid`)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck

	statements := make([]string, 0)
	for rows.Next() {
		var statement string
		require.NoError(t, rows.Scan(&statement))
		statements = append(statements, statement+";")
	}
	require.NoError(t, rows.Err())

	want, err := os.ReadFile(filepath.Join("..", "..", "spec", "schema.sql"))
	require.NoError(t, err)
	got := "-- Current SQLite schema produced by the migrations in internal/storage.\n" +
		"-- SQLite-managed objects such as FTS shadow tables are intentionally omitted.\n\n" +
		strings.Join(statements, "\n\n") + "\n"
	assert.Equal(t, string(want), got,
		"spec/schema.sql is stale; update it whenever the database schema changes")
}
