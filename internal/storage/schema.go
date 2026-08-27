package storage

// ApplicationID is the stamp that identifies an awb database: SQLite's
// application_id set to 0x41574200, the ASCII bytes "AWB" and a NUL (SPEC §3).
//
// It is written by the first migration rather than as a separate step of init,
// so whatever creates the schema also carries it. Every command refuses a file
// that exists, is not empty and does not carry the stamp, so a typo in --db or
// AWB_DB cannot point at somebody else's database and have awb's migrations
// applied to it.
const ApplicationID int32 = 0x41574200

// migrations is the ordered list of statement batches SPEC §3 describes.
// migrations[0] takes a fresh database to version 1, migrations[1] from 1 to 2,
// and so on. The version reached is recorded in SQLite's own user_version, so
// the schema carries no bookkeeping table of its own.
//
// A released batch is never edited, only followed by another.
var migrations = [][]string{
	schemaV1,
}

var schemaV1 = []string{
	// The stamp of SPEC §3, written by the schema's own first batch.
	`PRAGMA application_id = 1096237568`,

	`CREATE TABLE projects (
		key         TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	) STRICT`,

	// The two CHECK constraints below are SPEC §2.2's invariants, kept in the
	// storage layer so the recorded state cannot disagree with itself whatever
	// a caller does: status and assignee never drift apart, and a non-closed
	// issue never carries a close reason.
	//
	// A closed issue may have an assignee or not, its assignee being a record
	// of who did the work rather than a claim on it.
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
	// pair is stored canonically with the smaller ID as subject (SPEC §2.3),
	// which is what makes adding it from either end idempotent.
	`CREATE TABLE relations (
		subject TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		type    TEXT NOT NULL,
		other   TEXT NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
		PRIMARY KEY (subject, type, other),
		CHECK (type IN ('blocked-by', 'has-parent', 'discovered-from', 'related')),
		CHECK (subject <> other)
	) STRICT, WITHOUT ROWID`,

	`CREATE INDEX idx_relations_other ON relations (type, other)`,

	// SPEC §2.3: an issue has at most one parent.
	`CREATE UNIQUE INDEX idx_relations_one_parent
		ON relations (subject) WHERE type = 'has-parent'`,

	// Full text search over titles and descriptions (SPEC §3), with the
	// unicode61 tokenizer at its default settings: case- and
	// diacritic-insensitive, splitting on non-alphanumeric characters, no
	// stemming and no prefix matching (SPEC §4.3).
	`CREATE VIRTUAL TABLE issues_fts USING fts5 (
		title,
		description,
		content = 'issues',
		content_rowid = 'rowid',
		tokenize = 'unicode61'
	)`,

	// Kept in sync by triggers (SPEC §3).
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
