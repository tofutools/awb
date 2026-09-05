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
				existed, err := tx.UsersWithPasswordHaveExisted()
				require.NoError(t, err)
				assert.Equal(t, tc.existed, existed)
				return nil
			}))
		})
	}
}

func TestV20GeneralizesIssueOrderAndDropsBoardHidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 19)
	const timestamp = "2026-09-03T12:00:00.000Z"
	_, err := raw.ExecContext(t.Context(), `
		INSERT INTO workspaces (key, name, description, state, archived_at, archived_by, created_at, updated_at)
		VALUES ('awb', 'AWB', '', 'active', '', '', ?, ?);
		INSERT INTO issues (id, workspace, title, description, type, status, priority, board_order, board_hidden, created_at, updated_at)
		VALUES ('awb-aaaaaa', 'awb', 'ordered child', '', 'task', 'open', 2, 1024, 1, ?, ?)`,
		timestamp, timestamp, timestamp, timestamp)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		issue, readErr := tx.GetIssue("awb-aaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, 1024, issue.Order)
		return nil
	}))

	columns, err := db.SQL().QueryContext(t.Context(), `PRAGMA table_info(issues)`)
	require.NoError(t, err)
	defer columns.Close() //nolint:errcheck
	names := map[string]bool{}
	for columns.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		require.NoError(t, columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		names[name] = true
	}
	require.NoError(t, columns.Err())
	require.True(t, names["issue_order"])
	assert.False(t, names["board_order"])
	assert.False(t, names["board_hidden"])
	_, err = db.SQL().ExecContext(t.Context(), `UPDATE issues SET issue_order = -1 WHERE id = 'awb-aaaaaa'`)
	require.Error(t, err, "the renamed column retains its non-negative constraint")
	var indexes string
	require.NoError(t, db.SQL().QueryRowContext(t.Context(), `
		SELECT group_concat(name, ',' ORDER BY name) FROM sqlite_schema
		WHERE type = 'index' AND tbl_name = 'issues'`).Scan(&indexes))
	assert.Contains(t, indexes, "idx_issues_issue_order")
	assert.NotContains(t, indexes, "idx_issues_board_order")
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

func TestV15AddsBoardVisibilityAndClosedRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 14)
	const (
		closedAt  = "2026-01-01T00:00:00.000Z"
		updatedAt = "2026-02-01T00:00:00.000Z"
	)
	_, err := raw.ExecContext(t.Context(), `
		INSERT INTO workspaces (key, name, description, state, archived_at, archived_by, created_at, updated_at)
		VALUES ('awb', 'AWB', '', 'active', '', '', ?, ?);
		INSERT INTO issues (id, workspace, title, description, type, status, priority, board_order, created_at, updated_at)
		VALUES ('awb-aaaaaa', 'awb', 'done', '', 'task', 'closed', 2, 0, ?, ?);
		INSERT INTO issue_activity (issue, kind, actor, action, changes, created_at)
		VALUES ('awb-aaaaaa', 'change', 'alice', 'closed',
			'[{"field":"status","from":"in_progress","to":"closed"}]', ?);
		INSERT INTO board_views (id, name, owner, shared, all_workspaces, priority_max, created_at, updated_at, all_epics, include_no_epic)
		VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'Release', 'alice', 0, 1, 4, ?, ?, 1, 1)`,
		closedAt, updatedAt, closedAt, updatedAt, closedAt, updatedAt, updatedAt)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		issue, readErr := tx.GetIssue("awb-aaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, closedAt, issue.ClosedAt, "later issue updates do not become its close time")
		view, readErr := tx.GetBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, 30, view.ClosedDays)
		assert.Zero(t, view.EpicClosedDays)
		return nil
	}))
}

func TestV16AddsEmptyImplementationLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 15)
	_, err := raw.ExecContext(t.Context(), `
		INSERT INTO workspaces (key, name, description, state, archived_at, archived_by, created_at, updated_at)
		VALUES ('awb', 'AWB', '', 'active', '', '', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');
		INSERT INTO issues (id, workspace, title, description, type, status, priority, board_order, created_at, updated_at)
		VALUES ('awb-aaaaaa', 'awb', 'existing', '', 'task', 'open', 2, 0,
		        '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		issue, readErr := tx.GetIssue("awb-aaaaaa")
		require.NoError(t, readErr)
		assert.Empty(t, issue.CommitHash)
		assert.Empty(t, issue.PullRequestURL)
		return nil
	}))
}

func TestV18AddsThePreviousBoardCardLimitToExistingViews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 17)
	_, err := raw.ExecContext(t.Context(), `INSERT INTO board_views
		(id, name, owner, created_at, updated_at)
		VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'Release', 'alice',
		        '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		view, readErr := tx.GetBoardView("view-aaaaaaaaaaaaaaaaaaaaaaaa")
		require.NoError(t, readErr)
		assert.Equal(t, 8, view.CardLimit)
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
			AllWorkspaces: false, Workspaces: []string{"awb"}, PriorityMax: 4, CardLimit: 8}
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

// Rebuilding the users table is the only way to lose the CHECK that made a
// password mandatory, and a rebuild is the one migration shape that can take
// its dependants with it: both reference users and cascade on delete, and the
// board-view trigger lives on the table itself. What must survive is every row
// on both sides and the cascade that ties them together.
func TestV19MakesThePasswordOptionalWithoutLosingWhatDependsOnAUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 18)
	const timestamp = "2026-08-31T12:00:00.000000Z"
	for _, statement := range []string{
		`INSERT INTO workspaces (key, name, description, state, archived_at, archived_by, created_at, updated_at)
		 VALUES ('awb', 'AWB', '', 'active', '', '', '` + timestamp + `', '` + timestamp + `')`,
		`INSERT INTO users (name, full_name, password_hash, workspace_admin, user_admin, created_at, updated_at)
		 VALUES ('alice', 'Alice', 'hash', 1, 0, '` + timestamp + `', '` + timestamp + `')`,
		`INSERT INTO workspace_members (workspace, user, access) VALUES ('awb', 'alice', 'admin')`,
		`INSERT INTO ignored_workspaces (user, workspace) VALUES ('alice', 'awb')`,
		`INSERT INTO board_views (id, name, owner, shared, all_workspaces, priority_max,
			created_at, updated_at, all_epics, include_no_epic, card_limit)
		 VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa', 'Release', 'alice', 1, 1, 3,
			'` + timestamp + `', '` + timestamp + `', 1, 1, 8)`,
	} {
		_, err := raw.ExecContext(t.Context(), statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, raw.Close())

	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// The existing account is untouched, and still one a server authenticates.
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		user, readErr := tx.GetUser("alice")
		require.NoError(t, readErr)
		assert.Equal(t, "Alice", user.FullName)
		assert.True(t, user.WorkspaceAdmin)
		require.Len(t, user.Workspaces, 1)
		assert.Equal(t, domain.AccessAdmin, user.Workspaces[0].Access)

		any, readErr := tx.AnyUsersWithPassword()
		require.NoError(t, readErr)
		assert.True(t, any)
		return nil
	}))

	for query, want := range map[string]int{
		`SELECT count(*) FROM workspace_members WHERE user = 'alice'`:  1,
		`SELECT count(*) FROM ignored_workspaces WHERE user = 'alice'`: 1,
		`SELECT count(*) FROM board_views WHERE owner = 'alice'`:       1,
	} {
		var got int
		require.NoError(t, db.SQL().QueryRowContext(t.Context(), query).Scan(&got), query)
		assert.Equal(t, want, got, query)
	}

	// A password is now optional, and such an account is not one a server
	// authenticates.
	require.NoError(t, db.Write(t.Context(), func(tx *Tx) error {
		require.NoError(t, tx.InsertUser("claude-1", "", "", false, false))
		any, readErr := tx.AnyUsersWithPassword()
		require.NoError(t, readErr)
		assert.True(t, any, "alice still has one")
		return nil
	}))

	// And the cascades the rebuild recreated still fire. What is left is a
	// directory with a name in it and nothing to authenticate.
	require.NoError(t, db.Write(t.Context(), func(tx *Tx) error {
		return tx.DeleteUser("alice")
	}))
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		any, readErr := tx.AnyUsers()
		require.NoError(t, readErr)
		assert.True(t, any, "claude-1 is still an assignee")

		withPassword, readErr := tx.AnyUsersWithPassword()
		require.NoError(t, readErr)
		assert.False(t, withPassword)

		existed, readErr := tx.UsersWithPasswordHaveExisted()
		require.NoError(t, readErr)
		assert.True(t, existed, "the server locks rather than falling open")
		return nil
	}))
	for _, query := range []string{
		`SELECT count(*) FROM workspace_members WHERE user = 'alice'`,
		`SELECT count(*) FROM ignored_workspaces WHERE user = 'alice'`,
		`SELECT count(*) FROM board_views WHERE owner = 'alice'`,
	} {
		var got int
		require.NoError(t, db.SQL().QueryRowContext(t.Context(), query).Scan(&got), query)
		assert.Zero(t, got, query)
	}

	var check string
	require.NoError(t, db.SQL().QueryRowContext(t.Context(),
		`PRAGMA integrity_check`).Scan(&check))
	assert.Equal(t, "ok", check)

	// The rebuilt tables reference the rebuilt users by name, so a copy that
	// dropped or renamed a row would leave a key pointing at nothing.
	violations, err := db.SQL().QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer violations.Close() //nolint:errcheck
	assert.False(t, violations.Next(), "a foreign key survived the rebuild pointing at nothing")
	require.NoError(t, violations.Err())
}

func TestV21BacklogPreservesIssueDependants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	raw := openAtVersion(t, path, 20)
	_, err := raw.ExecContext(t.Context(), `
 INSERT INTO workspaces (key,name,created_at,updated_at) VALUES ('awb','AWB','2026-09-05T12:00:00.000Z','2026-09-05T12:00:00.000Z');
 INSERT INTO issues (id,workspace,title,type,status,priority,created_at,updated_at,issue_order,closed_at,commit_hash,pull_request_url)
 VALUES ('awb-aaaaaa','awb','Future epic','epic','closed',2,'2026-09-05T12:00:00.000Z','2026-09-05T12:00:00.000Z',1024,'2026-09-05T12:00:00.000Z','abc','https://example.com/pr/1'),
 ('awb-bbbbbb','awb','Child','task','open',2,'2026-09-05T12:00:00.000Z','2026-09-05T12:00:00.000Z',0,'','','');
 INSERT INTO issue_assignees VALUES ('awb-aaaaaa','alice',0),('awb-aaaaaa','bob',1);
 INSERT INTO issue_labels VALUES ('awb-aaaaaa','future');
 INSERT INTO relations VALUES ('awb-bbbbbb','has-parent','awb-aaaaaa');
 INSERT INTO attachments VALUES ('awb-aaaaaa','note.txt','text/plain',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-09-05T12:00:00.000Z');
 INSERT INTO issue_activity (id,issue,kind,body,created_at) VALUES (123,'awb-aaaaaa','comment','Keep this history','2026-09-05T12:00:00.000Z');
 INSERT INTO board_views (id,name,owner,shared,created_at,updated_at) VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa','Future','alice',0,'2026-09-05T12:00:00.000Z','2026-09-05T12:00:00.000Z');
 INSERT INTO board_view_epics VALUES ('view-aaaaaaaaaaaaaaaaaaaaaaaa','awb-aaaaaa');`)
	require.NoError(t, err)
	// Row counts alone miss lost fields; compare every persisted value, including row IDs.
	snapshot := func(db *sql.DB) map[string][][]any {
		result := map[string][][]any{}
		for _, table := range []string{"issues", "issue_assignees", "issue_labels", "relations", "attachments", "issue_activity", "board_view_epics"} {
			rows, err := db.QueryContext(t.Context(), "SELECT * FROM "+table)
			require.NoError(t, err)
			columns, err := rows.Columns()
			require.NoError(t, err)
			for rows.Next() {
				values := make([]any, len(columns))
				ptrs := make([]any, len(columns))
				for i := range values {
					ptrs[i] = &values[i]
				}
				require.NoError(t, rows.Scan(ptrs...))
				result[table] = append(result[table], values)
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}
		return result
	}
	before := snapshot(raw)
	require.NoError(t, raw.Close())
	db, err := Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	raw, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()
	// Columns are preserved in order by this table rebuild.
	assert.Equal(t, before, snapshot(raw))
	_, err = raw.ExecContext(t.Context(), "UPDATE issues SET status='backlog' WHERE id='awb-aaaaaa'")
	require.NoError(t, err)
	_, err = raw.ExecContext(t.Context(), "UPDATE issues SET status='unknown' WHERE id='awb-aaaaaa'")
	require.Error(t, err)
	var count int
	require.NoError(t, raw.QueryRowContext(t.Context(), "SELECT count(*) FROM issues_fts WHERE issues_fts MATCH 'Future'").Scan(&count))
	assert.Equal(t, 1, count)
	rows, err := raw.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next())
	require.NoError(t, rows.Err())
}
