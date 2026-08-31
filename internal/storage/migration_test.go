package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// The version-6 backfill is what carries an installation that already
// authenticates over the change: its accounts are what says its authentication
// is on, and a migration that only created the table would leave every
// existing server one deletion away from serving everybody again.
//
// The other half matters as much: a database that has never held a user must
// still say so, because that is what a local tracker is and it is what every
// version 1 database still is.
func TestV6RecordsUsersThatAlreadyExist(t *testing.T) {
	for _, tc := range []struct {
		name    string
		user    bool
		existed bool
	}{
		{name: "a server that authenticates", user: true, existed: true},
		{name: "a local tracker", user: false, existed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "awb.db")
			raw := openAtVersion(t, path, 5)
			if tc.user {
				_, err := raw.ExecContext(t.Context(),
					`INSERT INTO users (name, password_hash, created_at, updated_at)
					 VALUES ('alice', 'hash', ?, ?)`,
					"2026-01-01T10:00:00.000000Z", "2026-01-01T10:00:00.000000Z")
				require.NoError(t, err)
			}
			require.NoError(t, raw.Close())

			db, err := Open(t.Context(), path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
				existed, err := tx.UsersHaveExisted()
				require.NoError(t, err)
				assert.Equal(t, tc.existed, existed)
				return nil
			}))
		})
	}
}

// openAtVersion builds a real historical database shape from the batches that
// made it, so a migration is tested against what it will actually meet rather
// than against current code with a pragma set.
func openAtVersion(t *testing.T, path string, version int) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", dsn(path))
	require.NoError(t, err)

	for i, batch := range migrations[:version] {
		tx, txErr := raw.BeginTx(t.Context(), nil)
		require.NoError(t, txErr)
		for _, statement := range batch {
			_, txErr = tx.ExecContext(t.Context(), statement)
			require.NoError(t, txErr, "migration %d: %s", i+1, statement)
		}
		_, txErr = tx.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", i+1))
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit())
	}
	return raw
}

// A real version-4 shape is built from the historical batches, populated, and
// then opened by current code. This pins the lossless part of removing the
// close_reason column: the reason becomes a typed comment and every table that
// had to be rebuilt around issues retains its rows and constraints.
func TestV5MigratesCloseReasonsWithoutLosingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw, err := sql.Open("sqlite", dsn(path))
	require.NoError(t, err)

	for version, batch := range migrations[:4] {
		tx, txErr := raw.BeginTx(t.Context(), nil)
		require.NoError(t, txErr)
		for _, statement := range batch {
			_, txErr = tx.ExecContext(t.Context(), statement)
			require.NoError(t, txErr, "migration %d: %s", version+1, statement)
		}
		_, txErr = tx.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", version+1))
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit())
	}

	const (
		closedID = "awb-closed01"
		openID   = "awb-open0001"
		t0       = "2026-01-01T10:00:00.000000Z"
		t1       = "2026-01-02T11:00:00.000000Z"
	)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO projects (key, name, description, created_at, updated_at)
		 VALUES ('awb', 'AWB', 'tracker', ?, ?)`, t0, t1)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO issues
		 (id, project, title, description, type, status, priority, assignee,
		  close_reason, created_at, updated_at) VALUES
		 (?, 'awb', 'Legacy parser', 'searchable migration text', 'bug', 'closed', 1,
		  'mikael', 'Fixed **carefully**', ?, ?),
		 (?, 'awb', 'Open child', '', 'task', 'open', 2, '', '', ?, ?)`,
		closedID, t0, t1, openID, t0, t0)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO issue_labels VALUES (?, 'migration')`, closedID)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO relations VALUES (?, 'blocked-by', ?)`, openID, closedID)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO attachments VALUES (?, 'proof.txt', 'text/plain', 5, ?, ?)`,
		closedID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", t0)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO issue_activity
		 (id, issue, kind, actor, body, action, changes, created_at)
		 VALUES (7, ?, 'comment', 'alice', 'existing comment', '', '[]', ?)`, closedID, t0)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO users VALUES ('alice', 'hash', 0, 0, ?, ?)`, t0, t0)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(),
		`INSERT INTO project_members VALUES ('awb', 'alice', 'regular')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck

	rows, err := db.SQL().QueryContext(t.Context(), `PRAGMA table_info(issues)`)
	require.NoError(t, err)
	var columns []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Close())
	assert.NotContains(t, columns, "close_reason")

	var kind, actor, body, action, changes, createdAt string
	require.NoError(t, db.SQL().QueryRowContext(t.Context(),
		`SELECT kind, actor, body, action, changes, created_at
		   FROM issue_activity WHERE issue = ? AND action = 'closed'`, closedID).
		Scan(&kind, &actor, &body, &action, &changes, &createdAt))
	assert.Equal(t, "comment", kind)
	assert.Empty(t, actor, "the migration must not invent a historical actor")
	assert.Equal(t, "Fixed **carefully**", body)
	assert.Equal(t, "closed", action)
	assert.Equal(t, "[]", changes)
	assert.Equal(t, t1, createdAt)

	for query, want := range map[string]int{
		`SELECT count(*) FROM issues`:                                                    2,
		`SELECT count(*) FROM issue_labels WHERE issue = 'awb-closed01'`:                 1,
		`SELECT count(*) FROM relations WHERE subject = 'awb-open0001'`:                  1,
		`SELECT count(*) FROM attachments WHERE issue = 'awb-closed01'`:                  1,
		`SELECT count(*) FROM issue_activity WHERE issue = 'awb-closed01'`:               2,
		`SELECT count(*) FROM issue_activity WHERE id = 7 AND body = 'existing comment'`: 1,
		`SELECT count(*) FROM users WHERE name = 'alice'`:                                1,
		`SELECT count(*) FROM project_members WHERE user = 'alice'`:                      1,
	} {
		var got int
		require.NoError(t, db.SQL().QueryRowContext(t.Context(), query).Scan(&got), query)
		assert.Equal(t, want, got, query)
	}
	var shaIndex string
	require.NoError(t, db.SQL().QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_attachments_sha256'`).
		Scan(&shaIndex))
	assert.Equal(t, "idx_attachments_sha256", shaIndex)

	var ftsMatches int
	require.NoError(t, db.SQL().QueryRowContext(t.Context(),
		`SELECT count(*) FROM issues_fts WHERE issues_fts MATCH 'migration'`).Scan(&ftsMatches))
	assert.Equal(t, 1, ftsMatches, "the rebuilt FTS index includes existing descriptions")

	fkRows, err := db.SQL().QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	require.NoError(t, err)
	assert.False(t, fkRows.Next(), "the rebuilt dependent tables retain valid references")
	require.NoError(t, fkRows.Close())
}
