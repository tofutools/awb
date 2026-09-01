package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/tofutools/awb/internal/domain"
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

func TestV8AddsAnEmptyFullNameToExistingUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 7)
	_, err := raw.ExecContext(t.Context(), `
		INSERT INTO users (name, password_hash, created_at, updated_at)
		VALUES ('alice', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		user, readErr := tx.GetUser("alice")
		require.NoError(t, readErr)
		assert.Empty(t, user.FullName)
		return nil
	}))
}

func TestV11AddsBoardViewsWithoutChangingExistingWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 10)
	_, err := raw.ExecContext(t.Context(), `INSERT INTO projects
		(key, name, description, created_at, updated_at)
		VALUES ('awb', 'AWB', '', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Write(t.Context(), func(tx *Tx) error {
		view := &domain.BoardView{ID: "view-aaaaaaaaaaaaaaaaaaaaaaaa", Name: "Release", Owner: "alice",
			AllWorkspaces: false, Workspaces: []string{"awb"}, PriorityMax: 4}
		return tx.InsertBoardView(view)
	}))
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		workspace, readErr := tx.GetWorkspace("awb")
		require.NoError(t, readErr)
		assert.Equal(t, "AWB", workspace.Name)
		view, readErr := tx.GetBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, []string{"awb"}, view.Workspaces)
		return nil
	}))
}

func TestV12AddsAutomaticOrderWithoutChangingExistingIssues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 11)
	_, err := raw.ExecContext(t.Context(), `INSERT INTO projects
		(key, name, description, created_at, updated_at)
		VALUES ('awb', 'AWB', '', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		       ('web', 'WEB', '', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(), `INSERT INTO issues
		(id, project, title, description, type, status, priority, created_at, updated_at)
		VALUES ('awb-123456', 'awb', 'Existing', '', 'task', 'open', 2,
		        '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		issue, readErr := tx.GetIssue("awb-123456")
		require.NoError(t, readErr)
		assert.Zero(t, issue.Order)
		return nil
	}))
}

func TestV14MakesExistingBoardViewsSelectEveryEpicLane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 13)
	_, err := raw.ExecContext(t.Context(), `INSERT INTO board_views
		(id, name, owner, shared, all_workspaces, priority_max, created_at, updated_at)
		VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'Existing', 'alice', 0, 1, 4,
		        '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		view, readErr := tx.GetBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa")
		require.NoError(t, readErr)
		assert.True(t, view.AllEpics)
		assert.True(t, view.IncludeNoEpic)
		assert.Empty(t, view.Epics)
		return nil
	}))
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
	assert.NotContains(t, columns, "assignee")

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
		`SELECT count(*) FROM issues`:                                                               2,
		`SELECT count(*) FROM issue_labels WHERE issue = 'awb-closed01'`:                            1,
		`SELECT count(*) FROM relations WHERE subject = 'awb-open0001'`:                             1,
		`SELECT count(*) FROM attachments WHERE issue = 'awb-closed01'`:                             1,
		`SELECT count(*) FROM issue_activity WHERE issue = 'awb-closed01'`:                          2,
		`SELECT count(*) FROM issue_activity WHERE id = 7 AND body = 'existing comment'`:            1,
		`SELECT count(*) FROM issue_assignees WHERE issue = 'awb-closed01' AND assignee = 'mikael'`: 1,
		`SELECT count(*) FROM users WHERE name = 'alice'`:                                           1,
		`SELECT count(*) FROM workspace_members WHERE user = 'alice'`:                               1,
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

// Version 13 is the sole compatibility boundary for the Workspace rename: it
// accepts the released Project-shaped schema, retains every row, and leaves no
// Project-shaped table, column or index in the live schema.
func TestV13RenamesWorkspaceSchemaWithoutLosingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 12)
	const timestamp = "2026-08-31T12:00:00.000000Z"
	statements := []string{
		`INSERT INTO projects (key, name, description, state, archived_at, archived_by, created_at, updated_at)
		 VALUES ('awb', 'Agent Work Board', 'tracker', 'active', '', '', '` + timestamp + `', '` + timestamp + `')`,
		`INSERT INTO users (name, full_name, password_hash, project_admin, user_admin, created_at, updated_at)
		 VALUES ('alice', 'Alice', 'hash', 1, 0, '` + timestamp + `', '` + timestamp + `')`,
		`INSERT INTO project_members (project, user, access) VALUES ('awb', 'alice', 'admin')`,
		`INSERT INTO ignored_projects (user, project) VALUES ('alice', 'awb')`,
		`INSERT INTO project_activity (project, action, actor, created_at)
		 VALUES ('awb', 'archived', 'alice', '` + timestamp + `')`,
		`INSERT INTO issues (id, project, title, description, type, status, priority, created_at, updated_at, board_order)
		 VALUES ('awb-123456', 'awb', 'Keep me', '', 'task', 'open', 2, '` + timestamp + `', '` + timestamp + `', 1024)`,
		`INSERT INTO board_views (id, name, owner, shared, all_projects, priority_max, created_at, updated_at)
		 VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'Release', 'alice', 1, 0, 3, '` + timestamp + `', '` + timestamp + `')`,
		`INSERT INTO board_view_projects (view, project)
		 VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'awb')`,
	}
	for _, statement := range statements {
		_, err := raw.ExecContext(t.Context(), statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		workspace, readErr := tx.GetWorkspace("awb")
		require.NoError(t, readErr)
		assert.Equal(t, "Agent Work Board", workspace.Name)
		issue, readErr := tx.GetIssue("awb-123456")
		require.NoError(t, readErr)
		assert.Equal(t, "awb", issue.Workspace)
		assert.Equal(t, 1024, issue.Order)
		user, readErr := tx.GetUser("alice")
		require.NoError(t, readErr)
		assert.True(t, user.WorkspaceAdmin)
		view, readErr := tx.GetBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, []string{"awb"}, view.Workspaces)
		return nil
	}))

	for query, want := range map[string]int{
		`SELECT count(*) FROM workspace_members WHERE workspace = 'awb' AND user = 'alice'`:   1,
		`SELECT count(*) FROM ignored_workspaces WHERE workspace = 'awb' AND user = 'alice'`:  1,
		`SELECT count(*) FROM workspace_activity WHERE workspace = 'awb' AND actor = 'alice'`: 1,
		`SELECT count(*) FROM board_view_workspaces WHERE workspace = 'awb'`:                  1,
	} {
		var got int
		require.NoError(t, db.SQL().QueryRowContext(t.Context(), query).Scan(&got), query)
		assert.Equal(t, want, got, query)
	}

	rows, err := db.SQL().QueryContext(t.Context(), `
		SELECT name, sql FROM sqlite_schema
		 WHERE sql IS NOT NULL
		   AND (lower(name) LIKE '%project%' OR lower(sql) LIKE '%project%')`)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var name, statement string
		require.NoError(t, rows.Scan(&name, &statement))
		assert.Failf(t, "legacy schema name remains", "%s: %s", name, statement)
	}
	require.NoError(t, rows.Err())
}
