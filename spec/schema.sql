-- Current SQLite schema produced by the migrations in internal/storage.
-- SQLite-managed objects such as FTS shadow tables are intentionally omitted.

CREATE TABLE "workspaces" (
		key         TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	, state TEXT NOT NULL DEFAULT 'active'
		CHECK (state IN ('active', 'archived')), archived_at TEXT NOT NULL DEFAULT '', archived_by TEXT NOT NULL DEFAULT '') STRICT;

CREATE VIRTUAL TABLE issues_fts USING fts5 (
		title,
		description,
		content = 'issues',
		content_rowid = 'rowid',
		tokenize = 'unicode61'
	);

CREATE TABLE user_history (
		one INTEGER PRIMARY KEY CHECK (one = 1)
	) STRICT;

CREATE TABLE "workspace_activity" (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		workspace    TEXT NOT NULL REFERENCES "workspaces"(key) ON DELETE CASCADE,
		action     TEXT NOT NULL CHECK (action IN ('archived', 'restored')),
		actor      TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	) STRICT;

CREATE TABLE board_views (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		owner        TEXT NOT NULL,
		shared       INTEGER NOT NULL DEFAULT 0,
		all_workspaces INTEGER NOT NULL DEFAULT 1,
		priority_max INTEGER NOT NULL DEFAULT 4,
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL, all_epics INTEGER NOT NULL DEFAULT 1 CHECK (all_epics IN (0, 1)), include_no_epic INTEGER NOT NULL DEFAULT 1 CHECK (include_no_epic IN (0, 1)), closed_days INTEGER NOT NULL DEFAULT 30 CHECK (closed_days BETWEEN 0 AND 3650), epic_closed_days INTEGER NOT NULL DEFAULT 0 CHECK (epic_closed_days BETWEEN 0 AND 3650), card_limit INTEGER NOT NULL DEFAULT 8 CHECK (card_limit BETWEEN 1 AND 50),
		CHECK (substr(id, 1, 5) = 'view-' AND length(id) = 29
		       AND substr(id, 6) NOT GLOB '*[^0-9a-f]*'),
		CHECK (name <> ''),
		CHECK (owner <> ''),
		CHECK (shared IN (0, 1)),
		CHECK (all_workspaces IN (0, 1)),
		CHECK (priority_max BETWEEN 0 AND 4)
	) STRICT;

CREATE INDEX idx_board_views_owner ON board_views (owner, name, id);

CREATE TABLE "board_view_workspaces" (
		view    TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
		workspace TEXT NOT NULL REFERENCES "workspaces"(key) ON DELETE CASCADE,
		PRIMARY KEY (view, workspace)
	) STRICT, WITHOUT ROWID;

CREATE TABLE board_view_labels (
		view  TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		PRIMARY KEY (view, label)
	) STRICT, WITHOUT ROWID;

CREATE TABLE board_view_assignees (
		view     TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
		assignee TEXT NOT NULL,
		PRIMARY KEY (view, assignee)
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_workspace_activity_order
		ON workspace_activity (workspace, created_at DESC, id DESC);

CREATE TABLE "users" (
		name            TEXT PRIMARY KEY,
		full_name       TEXT NOT NULL DEFAULT '',
		password_hash   TEXT NOT NULL DEFAULT '',
		workspace_admin INTEGER NOT NULL DEFAULT 0,
		user_admin      INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT NOT NULL,
		updated_at      TEXT NOT NULL,
		CHECK (name <> ''),
		CHECK (workspace_admin IN (0, 1)),
		CHECK (user_admin IN (0, 1))
	) STRICT, WITHOUT ROWID;

CREATE TRIGGER users_board_views_ad AFTER DELETE ON users BEGIN
		DELETE FROM board_views WHERE owner = old.name;
	END;

CREATE TABLE workspace_members (
		workspace TEXT NOT NULL REFERENCES workspaces (key) ON DELETE CASCADE,
		user      TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE,
		access    TEXT NOT NULL,
		PRIMARY KEY (workspace, user),
		CHECK (access IN ('regular', 'admin'))
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_workspace_members_user ON workspace_members (user);

CREATE TABLE ignored_workspaces (
		user      TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE,
		workspace TEXT NOT NULL REFERENCES workspaces (key) ON DELETE CASCADE,
		PRIMARY KEY (user, workspace)
	) STRICT, WITHOUT ROWID;

CREATE TABLE "issues" (
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
	) STRICT;

CREATE INDEX idx_issues_workspace ON issues (workspace);

CREATE INDEX idx_issues_issue_order ON issues (issue_order, priority, updated_at, id);

CREATE INDEX idx_issues_status ON issues (status);

CREATE INDEX idx_issues_order ON issues (priority, created_at, id);

CREATE TRIGGER issues_fts_ai AFTER INSERT ON issues BEGIN
		INSERT INTO issues_fts (rowid, title, description)
		VALUES (new.rowid, new.title, new.description);
	END;

CREATE TRIGGER issues_fts_ad AFTER DELETE ON issues BEGIN
		INSERT INTO issues_fts (issues_fts, rowid, title, description)
		VALUES ('delete', old.rowid, old.title, old.description);
	END;

CREATE TRIGGER issues_fts_au AFTER UPDATE ON issues BEGIN
		INSERT INTO issues_fts (issues_fts, rowid, title, description)
		VALUES ('delete', old.rowid, old.title, old.description);
		INSERT INTO issues_fts (rowid, title, description)
		VALUES (new.rowid, new.title, new.description);
	END;

CREATE TABLE issue_assignees (
		issue    TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		assignee TEXT NOT NULL,
		position INTEGER NOT NULL,
		PRIMARY KEY (issue, assignee),
		UNIQUE (issue, position),
		CHECK (assignee <> ''),
		CHECK (position >= 0)
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_issue_assignees_assignee ON issue_assignees (assignee, issue);

CREATE TABLE issue_labels (
		issue TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		PRIMARY KEY (issue, label)
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_issue_labels_label ON issue_labels (label);

CREATE TABLE relations (
		subject TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		type    TEXT NOT NULL,
		other   TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		PRIMARY KEY (subject, type, other),
		CHECK (type IN ('blocked-by', 'has-parent', 'discovered-from', 'related')),
		CHECK (subject <> other)
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_relations_other ON relations (type, other);

CREATE UNIQUE INDEX idx_relations_one_parent
		ON relations (subject) WHERE type = 'has-parent';

CREATE TABLE attachments (
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
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_attachments_order ON attachments (issue, created_at, name);

CREATE INDEX idx_attachments_sha256 ON attachments (sha256);

CREATE TABLE issue_activity (
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
	) STRICT;

CREATE INDEX idx_issue_activity_order
		ON issue_activity (issue, created_at DESC, id DESC);

CREATE TABLE board_view_epics (
        view TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
        epic TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
        PRIMARY KEY (view, epic)
    ) STRICT, WITHOUT ROWID;

CREATE TABLE board_view_columns (
		view TEXT NOT NULL REFERENCES board_views(id) ON DELETE CASCADE,
		status TEXT NOT NULL CHECK (status IN ('backlog', 'open', 'in_progress', 'closed')),
		position INTEGER NOT NULL CHECK (position >= 0),
		PRIMARY KEY (view, status),
		UNIQUE (view, position)
	) STRICT, WITHOUT ROWID;

CREATE INDEX idx_issues_board_candidates
		ON issues (type, status, workspace, priority, closed_at, issue_order, updated_at, id);
