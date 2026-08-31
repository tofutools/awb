package storage

// ApplicationID is the stamp that identifies an awb database: SQLite's
// application_id set to 0x41574200, the ASCII bytes "AWB" and a NUL.
//
// It is written by the first migration rather than as a separate step of init,
// so whatever creates the schema also carries it. Every command refuses a file
// that exists, is not empty and does not carry the stamp, so a typo in --db or
// AWB_DB cannot point at somebody else's database and have awb's migrations
// applied to it.
const ApplicationID int32 = 0x41574200

// migrations is the ordered list of statement batches. migrations[0] takes a
// fresh database to version 1, migrations[1] from 1 to 2, and so on. The
// version reached is recorded in SQLite's own user_version, so the schema
// carries no bookkeeping table of its own.
//
// A released batch is never edited, only followed by another.
var migrations = [][]string{
	schemaV1,
	schemaV2,
	schemaV3,
	schemaV4,
	schemaV5,
	schemaV6,
}

// schemaV6 records that a database has had a user, which is what stops
// deleting the last one from turning a running server's authentication off.
//
// The fact has to outlive the rows, and it has to live here rather than in the
// server's memory. Users are added and deleted by a command line holding the
// file, which a running server learns about only by looking; one that looked
// before the first user was added and again after the last was deleted would
// see the same empty table twice and go back to serving everybody. That is the
// accident this table exists to prevent, and only something the deletion
// cannot remove can prevent it.
//
// It is one row that exists or does not, written by the insert that creates a
// user and never deleted. A database that already holds users is marked as the
// migration runs, because its authentication is already on.
var schemaV6 = []string{
	`CREATE TABLE user_history (
		one INTEGER PRIMARY KEY CHECK (one = 1)
	) STRICT`,

	`INSERT INTO user_history (one) SELECT 1 WHERE EXISTS (SELECT 1 FROM users)`,
}

// schemaV4 adds the append-only issue activity stream. Changes are JSON text
// because SQLite has no JSON column type; json_valid keeps the stored value a
// valid array whatever code writes it.
var schemaV4 = []string{
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
		CHECK ((kind = 'comment' AND body <> '' AND action = '') OR
		       (kind = 'change' AND body = '' AND action <> '')),
		CHECK (json_valid(changes) AND json_type(changes) = 'array')
	) STRICT`,

	`CREATE INDEX idx_issue_activity_order
		ON issue_activity (issue, created_at DESC, id DESC)`,
}

// schemaV5 moves the current close reason into the activity stream and removes
// its issue column. Existing databases cannot name the historical actor, so a
// migrated reason deliberately carries the empty/system actor and the issue's
// last-update timestamp rather than inventing either value.
//
// SQLite cannot drop close_reason directly because schemaV1's CHECK constraint
// names it. The table and its foreign-key dependants are therefore rebuilt in
// one migration transaction. Rowids are preserved and the external-content
// full-text index is rebuilt before the transaction commits.
var schemaV5 = []string{
	`CREATE TEMP TABLE migration_v5_close_reasons AS
		SELECT id AS issue, close_reason AS body, updated_at AS created_at
		  FROM issues WHERE close_reason <> ''`,
	`CREATE TEMP TABLE migration_v5_labels AS SELECT issue, label FROM issue_labels`,
	`CREATE TEMP TABLE migration_v5_relations AS SELECT subject, type, other FROM relations`,
	`CREATE TEMP TABLE migration_v5_attachments AS
		SELECT issue, name, content_type, size, sha256, created_at FROM attachments`,
	`CREATE TEMP TABLE migration_v5_activity AS
		SELECT id, issue, kind, actor, body, action, changes, created_at FROM issue_activity`,

	`DROP TABLE issue_labels`,
	`DROP TABLE relations`,
	`DROP TABLE attachments`,
	`DROP TABLE issue_activity`,
	`DROP TRIGGER issues_fts_ai`,
	`DROP TRIGGER issues_fts_ad`,
	`DROP TRIGGER issues_fts_au`,

	`CREATE TABLE issues_new (
		id          TEXT PRIMARY KEY,
		project     TEXT NOT NULL REFERENCES projects(key) ON DELETE RESTRICT,
		title       TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		type        TEXT NOT NULL,
		status      TEXT NOT NULL,
		priority    INTEGER NOT NULL,
		assignee    TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL,
		CHECK (type IN ('epic', 'feature', 'bug', 'task', 'chore')),
		CHECK (status IN ('open', 'in_progress', 'closed')),
		CHECK (priority BETWEEN 0 AND 4),
		CHECK (
			(status = 'open'        AND assignee =  '') OR
			(status = 'in_progress' AND assignee <> '') OR
			(status = 'closed')
		)
	) STRICT`,
	`INSERT INTO issues_new (rowid, id, project, title, description, type, status,
		priority, assignee, created_at, updated_at)
		SELECT rowid, id, project, title, description, type, status, priority,
		       assignee, created_at, updated_at FROM issues`,
	`DROP TABLE issues`,
	`ALTER TABLE issues_new RENAME TO issues`,

	`CREATE INDEX idx_issues_project ON issues (project)`,
	`CREATE INDEX idx_issues_status ON issues (status)`,
	`CREATE INDEX idx_issues_assignee ON issues (assignee)`,
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

	`CREATE TABLE issue_labels (
		issue TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		PRIMARY KEY (issue, label)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO issue_labels SELECT issue, label FROM migration_v5_labels`,
	`CREATE INDEX idx_issue_labels_label ON issue_labels (label)`,

	`CREATE TABLE relations (
		subject TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		type    TEXT NOT NULL,
		other   TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		PRIMARY KEY (subject, type, other),
		CHECK (type IN ('blocked-by', 'has-parent', 'discovered-from', 'related')),
		CHECK (subject <> other)
	) STRICT, WITHOUT ROWID`,
	`INSERT INTO relations SELECT subject, type, other FROM migration_v5_relations`,
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
		SELECT issue, name, content_type, size, sha256, created_at FROM migration_v5_attachments`,
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
		SELECT id, issue, kind, actor, body, action, changes, created_at FROM migration_v5_activity`,
	`INSERT INTO issue_activity (issue, kind, actor, body, action, changes, created_at)
		SELECT issue, 'comment', '', body, 'closed', '[]', created_at
		  FROM migration_v5_close_reasons`,
	`CREATE INDEX idx_issue_activity_order
		ON issue_activity (issue, created_at DESC, id DESC)`,

	`DROP TABLE migration_v5_close_reasons`,
	`DROP TABLE migration_v5_labels`,
	`DROP TABLE migration_v5_relations`,
	`DROP TABLE migration_v5_attachments`,
	`DROP TABLE migration_v5_activity`,
}

var schemaV1 = []string{
	// The application stamp, written by the schema's own first batch.
	`PRAGMA application_id = 1096237568`,

	`CREATE TABLE projects (
		key         TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	) STRICT`,

	// The two CHECK constraints below are the model's invariants, kept in the
	// storage layer so the recorded state cannot disagree with itself whatever a
	// caller does: status and assignee never drift apart, and a non-closed issue
	// never carries a close reason.
	//
	// A closed issue may have an assignee or not, its assignee being a record of
	// who did the work rather than a claim on it.
	`CREATE TABLE issues (
		id           TEXT PRIMARY KEY,
		project      TEXT NOT NULL REFERENCES projects(key) ON DELETE RESTRICT,
		title        TEXT NOT NULL,
		description  TEXT NOT NULL DEFAULT '',
		type         TEXT NOT NULL,
		status       TEXT NOT NULL,
		priority     INTEGER NOT NULL,
		assignee     TEXT NOT NULL DEFAULT '',
		close_reason TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL,
		CHECK (type IN ('epic', 'feature', 'bug', 'task', 'chore')),
		CHECK (status IN ('open', 'in_progress', 'closed')),
		CHECK (priority BETWEEN 0 AND 4),
		CHECK (
			(status = 'open'        AND assignee =  '') OR
			(status = 'in_progress' AND assignee <> '') OR
			(status = 'closed')
		),
		CHECK (status = 'closed' OR close_reason = '')
	) STRICT`,

	`CREATE INDEX idx_issues_project ON issues (project)`,
	`CREATE INDEX idx_issues_status ON issues (status)`,
	`CREATE INDEX idx_issues_assignee ON issues (assignee)`,
	`CREATE INDEX idx_issues_order ON issues (priority, created_at, id)`,

	`CREATE TABLE issue_labels (
		issue TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		PRIMARY KEY (issue, label)
	) STRICT, WITHOUT ROWID`,

	`CREATE INDEX idx_issue_labels_label ON issue_labels (label)`,

	// A relation is stored once and shown on both issues. A symmetric related
	// pair is stored canonically with the smaller ID as subject, which is what
	// makes adding it from either end idempotent.
	`CREATE TABLE relations (
		subject TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		type    TEXT NOT NULL,
		other   TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		PRIMARY KEY (subject, type, other),
		CHECK (type IN ('blocked-by', 'has-parent', 'discovered-from', 'related')),
		CHECK (subject <> other)
	) STRICT, WITHOUT ROWID`,

	`CREATE INDEX idx_relations_other ON relations (type, other)`,

	// An issue has at most one parent.
	`CREATE UNIQUE INDEX idx_relations_one_parent
		ON relations (subject) WHERE type = 'has-parent'`,

	// Full text search over titles and descriptions, with the unicode61 tokenizer
	// at its default settings: case- and diacritic-insensitive, splitting on
	// non-alphanumeric characters, no stemming and no prefix matching.
	`CREATE VIRTUAL TABLE issues_fts USING fts5 (
		title,
		description,
		content = 'issues',
		content_rowid = 'rowid',
		tokenize = 'unicode61'
	)`,

	// Kept in sync by triggers.
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
}

// schemaV2 adds attachments.
//
// Only the metadata is here. The content is a file in the attachments
// directory named by its own SHA-256, so the database stays small enough to
// copy and the blobs can sit on a filesystem of their own. Two attachments
// holding the same bytes therefore share one file, which is why deleting a row
// removes the file only once no row names that digest any more.
var schemaV2 = []string{
	// An attachment is identified by its issue and its name, exactly as a label
	// is identified by its issue and its value, so that is the key. It carries
	// no identifier of its own — a synthetic one would be a second name for
	// something that already has one — and the key is what makes a name unique
	// within an issue.
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

	// The listing order — oldest first, then name — is the index's order too.
	`CREATE INDEX idx_attachments_order ON attachments (issue, created_at, name)`,

	// What answers "does any other row still name this digest?" when one is
	// deleted.
	`CREATE INDEX idx_attachments_sha256 ON attachments (sha256)`,
}

// schemaV3 adds users and their access to projects, which is what awb serve
// authenticates and authorizes against.
//
// It is deliberately data rather than a file beside the database: a server's
// users are part of the tracker's state, they are managed through the same two
// surfaces as everything else, and a membership is a row that a foreign key
// can keep pointing at a project that still exists.
//
// A database that holds no user row at all is the version 1 database this
// migration leaves behind, and it stays a server without authentication —
// which is why nothing here is seeded.
var schemaV3 = []string{
	// A username is an assignee: it is what the issues that user works on record
	// as their assignee, so the two vocabularies are one and the column holds
	// the same values the issues table's assignee does.
	//
	// The password hash never leaves this table. It is bcrypt's own output,
	// which carries its cost inside it, so a hash written under an older
	// default keeps verifying after that default has risen.
	`CREATE TABLE users (
		name          TEXT PRIMARY KEY,
		password_hash TEXT NOT NULL,
		project_admin INTEGER NOT NULL DEFAULT 0,
		user_admin    INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		CHECK (name <> ''),
		CHECK (password_hash <> ''),
		CHECK (project_admin IN (0, 1)),
		CHECK (user_admin IN (0, 1))
	) STRICT, WITHOUT ROWID`,

	// A membership is keyed on its project and its user, exactly as a label is
	// keyed on its issue and its value: it carries no identifier of its own,
	// because the pair is what identifies one, and a user therefore holds at
	// most one access level in a project.
	//
	// Both ends cascade. Deleting a project takes its memberships with it, and
	// so does deleting a user, because a membership without one of its two ends
	// is not a permission anybody holds.
	`CREATE TABLE project_members (
		project TEXT NOT NULL REFERENCES projects (key) ON DELETE CASCADE,
		user    TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE,
		access  TEXT NOT NULL,
		PRIMARY KEY (project, user),
		CHECK (access IN ('regular', 'admin'))
	) STRICT, WITHOUT ROWID`,

	// What answers "which projects may this user see?", which is the condition
	// every listing, search and facet carries on a server that authorizes.
	`CREATE INDEX idx_project_members_user ON project_members (user)`,
}
