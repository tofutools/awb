package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	schemaDocumentPath = "../../spec/schema.sql"
	schemaDocumentHead = "-- Current SQLite schema produced by the migrations in internal/storage.\n" +
		"-- SQLite-managed objects such as FTS shadow tables are intentionally omitted.\n\n"
)

// TestSchemaDocumentIsCurrent keeps the schema snapshot in sync with
// the schema produced by every migration. SQLite creates the FTS shadow tables,
// sqlite_sequence and automatic indexes itself, so they are not part of the
// application-owned DDL in the document.
func TestSchemaDocumentIsCurrent(t *testing.T) {
	db, err := Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	rows, err := db.SQL().QueryContext(t.Context(), `
		SELECT sql
		FROM sqlite_schema AS schema_object
		WHERE sql IS NOT NULL
		  AND name NOT LIKE 'sqlite_%'
		  AND (type <> 'table' OR sql LIKE 'CREATE VIRTUAL TABLE %' OR NOT EXISTS (
		    SELECT 1
		    FROM sqlite_schema AS virtual_table
		    WHERE virtual_table.type = 'table'
		      AND virtual_table.sql LIKE 'CREATE VIRTUAL TABLE %'
		      AND schema_object.name GLOB virtual_table.name || '_*'
		  ))
		ORDER BY schema_object.rowid`)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck

	statements := make([]string, 0)
	for rows.Next() {
		var statement string
		require.NoError(t, rows.Scan(&statement))
		statements = append(statements, statement+";")
	}
	require.NoError(t, rows.Err())

	got := schemaDocumentHead + strings.Join(statements, "\n\n") + "\n"
	path := filepath.Clean(schemaDocumentPath)
	if os.Getenv("AWB_UPDATE_SCHEMA") != "" {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got,
		"spec/schema.sql is stale; run task schema:update")
}
