package storage

// schemaV21 widens the status CHECK. Rebuild dependants before replacing issues
// to preserve cascading relations, attachments, activity and saved epic selections.
var schemaV21 = []string{
	`CREATE TEMP TABLE migration_v21_assignees AS
		SELECT issue, assignee, position FROM issue_assignees`,
	`CREATE TEMP TABLE migration_v21_labels AS SELECT issue, label FROM issue_labels`,
	`CREATE TEMP TABLE migration_v21_relations AS SELECT subject, type, other FROM relations`,
	`CREATE TEMP TABLE migration_v21_attachments AS
		SELECT issue, name, content_type, size, sha256, created_at FROM attachments`,
	`CREATE TEMP TABLE migration_v21_activity AS
		SELECT id, issue, kind, actor, body, action, changes, created_at FROM issue_activity`,

	`CREATE TEMP TABLE migration_v21_epics AS SELECT view, epic FROM board_view_epics`,
	`DROP TABLE board_view_epics`,
	`DROP TABLE issue_assignees`,
	`DROP TABLE issue_labels`,
	`DROP TABLE relations`,
	`DROP TABLE attachments`,
	`DROP TABLE issue_activity`,
	`DROP TRIGGER issues_fts_ai`,
	`DROP TRIGGER issues_fts_ad`,
	`DROP TRIGGER issues_fts_au`,

	`CREATE TABLE issues_new (
		id          TEXT PRIMARY KEY,
		workspace     TEXT NOT NULL REFERENCES workspaces(key) ON DELETE RESTRICT,
		title       TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		type        TEXT NOT NULL,
		status      TEXT NOT NULL,
		priority    INTEGER NOT NULL,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL,
		issue_order INTEGER NOT NULL DEFAULT 0 CHECK (issue_order >= 0),
		closed_at TEXT NOT NULL DEFAULT '',
		commit_hash TEXT NOT NULL DEFAULT '',
		pull_request_url TEXT NOT NULL DEFAULT '',
		CHECK (type IN ('epic', 'feature', 'bug', 'task', 'chore')),
		CHECK (status IN ('backlog', 'open', 'in_progress', 'closed')),
		CHECK (priority BETWEEN 0 AND 4)
	) STRICT`,
	`INSERT INTO issues_new (rowid, id, workspace, title, description, type, status,
		priority, created_at, updated_at, issue_order, closed_at, commit_hash, pull_request_url)
		SELECT rowid, id, workspace, title, description, type, status, priority,
		       created_at, updated_at, issue_order, closed_at, commit_hash, pull_request_url FROM issues`,
	`DROP TABLE issues`,
	`ALTER TABLE issues_new RENAME TO issues`,
	`CREATE INDEX idx_issues_workspace ON issues (workspace)`,
	`CREATE INDEX idx_issues_issue_order ON issues (issue_order, priority, updated_at, id)`,
	`CREATE INDEX idx_issues_status ON issues (status)`,
	`CREATE INDEX idx_issues_order ON issues (priority, created_at, id)`,
	`CREATE TRIGGER issues_fts_ai AFTER INSERT ON issues BEGIN
		INSERT INTO issues_fts (rowid, title, description)
		VALUES (new.rowid, new.title, new.description);
	END`,
	`CREATE TRIGGER issues_fts_ad AFTER DELETE ON issues BEGIN
		INSERT INTO issues_fts (issues_fts, rowid, title, description)
		VALUES ('delete', old.rowid, old.title, old.description);
	END`,
	`CREATE TRIGGER issues_fts_au AFTER UPDATE ON issues BEGIN
		INSERT INTO issues_fts (issues_fts, rowid, title, description)
		VALUES ('delete', old.rowid, old.title, old.description);
		INSERT INTO issues_fts (rowid, title, description)
		VALUES (new.rowid, new.title, new.description);
	END`,
	`INSERT INTO issues_fts (issues_fts) VALUES ('rebuild')`,

	`CREATE TABLE issue_assignees (
		issue    TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		assignee TEXT NOT NULL,
		position INTEGER NOT NULL,
		PRIMARY KEY (issue, assignee),
		UNIQUE (issue, position),
		CHECK (assignee <> ''),
		CHECK (position >= 0)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO issue_assignees (issue, assignee, position)
		SELECT issue, assignee, position FROM migration_v21_assignees`,
	`CREATE INDEX idx_issue_assignees_assignee ON issue_assignees (assignee, issue)`,

	`CREATE TABLE issue_labels (
		issue TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		PRIMARY KEY (issue, label)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO issue_labels SELECT issue, label FROM migration_v21_labels`,
	`CREATE INDEX idx_issue_labels_label ON issue_labels (label)`,
	`CREATE TABLE relations (
		subject TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		type    TEXT NOT NULL,
		other   TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		PRIMARY KEY (subject, type, other),
		CHECK (type IN ('blocked-by', 'has-parent', 'discovered-from', 'related')),
		CHECK (subject <> other)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO relations SELECT subject, type, other FROM migration_v21_relations`,
	`CREATE INDEX idx_relations_other ON relations (type, other)`,
	`CREATE UNIQUE INDEX idx_relations_one_parent
		ON relations (subject) WHERE type = 'has-parent'`,
	`CREATE TABLE attachments (
		issue        TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		name         TEXT NOT NULL,
		content_type TEXT NOT NULL,
		size         INTEGER NOT NULL,
		sha256       TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		PRIMARY KEY (issue, name),
		CHECK (name <> ''),
		CHECK (content_type <> ''),
		CHECK (size >= 0),
		CHECK (length(sha256) = 64)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO attachments
		SELECT issue, name, content_type, size, sha256, created_at FROM migration_v21_attachments`,
	`CREATE INDEX idx_attachments_order ON attachments (issue, created_at, name)`,
	`CREATE INDEX idx_attachments_sha256 ON attachments (sha256)`,
	`CREATE TABLE issue_activity (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		issue      TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		kind       TEXT NOT NULL,
		actor      TEXT NOT NULL DEFAULT '',
		body       TEXT NOT NULL DEFAULT '',
		action     TEXT NOT NULL DEFAULT '',
		changes    TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL,
		CHECK (kind IN ('comment', 'change')),
		CHECK ((kind = 'comment' AND body <> '' AND action IN ('', 'closed')) OR
		       (kind = 'change' AND body = '' AND action <> '')),
		CHECK (json_valid(changes) AND json_type(changes) = 'array')
	) STRICT`,
	`INSERT INTO issue_activity (id, issue, kind, actor, body, action, changes, created_at)
		SELECT id, issue, kind, actor, body, action, changes, created_at FROM migration_v21_activity`,
	`CREATE INDEX idx_issue_activity_order
		ON issue_activity (issue, created_at DESC, id DESC)`,

	`CREATE TABLE board_view_epics (
        view TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
        epic TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
        PRIMARY KEY (view, epic)
    ) STRICT, WITHOUT ROWID`,
	`INSERT INTO board_view_epics SELECT view, epic FROM migration_v21_epics`,
	`DROP TABLE migration_v21_epics`,
	`DROP TABLE migration_v21_assignees`,
	`DROP TABLE migration_v21_labels`,
	`DROP TABLE migration_v21_relations`,
	`DROP TABLE migration_v21_attachments`,
	`DROP TABLE migration_v21_activity`,
}
