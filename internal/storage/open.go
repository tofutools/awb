package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	commonsqlite "github.com/mikaelstaldal/go-server-common/sqlite"
	"github.com/tofutools/awb/internal/awberr"
)

// busyTimeoutMS is how long a statement waits for the write lock before giving
// up. A transaction that cannot take the lock in that time fails rather than
// being retried in a loop.
const busyTimeoutMS = 5000

// DB is an open awb database.
type DB struct {
	db   *sql.DB
	path string
}

// SQL exposes the underlying handle. It is here because the query helpers in
// this package are methods on DB and the local backend drives transactions
// through them; nothing outside the storage layer should reach for it.
func (d *DB) SQL() *sql.DB { return d.db }

// Path is the file this database was opened from.
func (d *DB) Path() string { return d.path }

// Close releases the database.
func (d *DB) Close() error { return d.db.Close() }

// Open opens an existing awb database and brings its schema up to date.
//
// It refuses a file that is missing or zero-length, so a typo in --db or
// AWB_DB cannot silently produce a second, empty tracker: only init creates a
// database. It also refuses a file that exists, is not empty and does not
// carry awb's application_id, so the same typo cannot point at somebody else's
// database and have awb's migrations applied to it.
func Open(ctx context.Context, path string) (*DB, error) {
	empty, err := isEmptyFile(path)
	if err != nil {
		return nil, err
	}
	if empty {
		return nil, awberr.Runtimef(
			"no awb database at %s: run \"awb init\" to create it, or point --db or AWB_DB elsewhere", path)
	}
	return openExisting(ctx, path, false)
}

// OpenCurrent opens an existing database read-only and only when its schema is
// already current. It is for advisory reads such as shell completion, which
// must not apply migrations merely because somebody pressed Tab.
func OpenCurrent(ctx context.Context, path string) (*DB, error) {
	empty, err := isEmptyFile(path)
	if err != nil {
		return nil, err
	}
	if empty {
		return nil, awberr.Runtimef("no awb database at %s", path)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "open %s", path)
	}
	if err := checkStamp(ctx, db, path, false); err != nil {
		_ = db.Close()
		return nil, err
	}
	version, err := readInt32Pragma(ctx, db, "user_version")
	if err != nil {
		_ = db.Close()
		return nil, awberr.Wrap(awberr.Runtime, err, "read %s", path)
	}
	if version != int32(len(migrations)) {
		_ = db.Close()
		return nil, awberr.Runtimef("%s schema is not current", path)
	}
	return &DB{db: db, path: path}, nil
}

// Init creates the database if it is absent, together with any missing parent
// directory, and brings its schema up to date. It is the only command that
// creates one, and it is idempotent.
func Init(ctx context.Context, path string) (*DB, error) {
	if err := commonsqlite.CreateDataDir(path); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "create directory for %s", path)
	}
	return openExisting(ctx, path, true)
}

// openExisting opens path, checks the stamp and migrates. adopt is set by
// init, which may take over an empty database as well as create one.
func openExisting(ctx context.Context, path string, adopt bool) (*DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "open %s", path)
	}

	if err := checkStamp(ctx, db, path, adopt); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := commonsqlite.MigrateStrict(ctx, db, migrations); err != nil {
		_ = db.Close()
		if errors.Is(err, commonsqlite.ErrSchemaTooNew) {
			return nil, awberr.Runtimef(
				"%s was written by a newer version of awb: %s", path, err.Error())
		}
		return nil, awberr.Wrap(awberr.Runtime, err, "migrate %s", path)
	}

	return &DB{db: db, path: path}, nil
}

// dsn builds the connection string. Foreign keys are on, non-read-only
// database/sql transactions begin immediately, and a busy timeout is set, so
// several local processes can use the same file safely. WAL journalling is a
// property of the file and is set by the first migration.
func dsn(path string) string {
	return fmt.Sprintf("%s?_pragma=foreign_keys=on&_pragma=busy_timeout=%d&_txlock=immediate",
		path, busyTimeoutMS)
}

func readOnlyDSN(path string) string {
	return fmt.Sprintf("file:%s?mode=ro&_pragma=foreign_keys=on&_pragma=busy_timeout=%d&_txlock=immediate",
		path, busyTimeoutMS)
}

// checkStamp applies the file-identity rule.
func checkStamp(ctx context.Context, db *sql.DB, path string, adopt bool) error {
	appID, err := readInt32Pragma(ctx, db, "application_id")
	if err != nil {
		return awberr.Runtimef(
			"%s is not a readable SQLite database: remove the file or point --db or AWB_DB elsewhere", path)
	}
	if appID == ApplicationID {
		return nil
	}

	// An interrupted init does not leave a zero-length file, SQLite having
	// written the header before the first migration could commit. A readable
	// SQLite database with application_id and user_version both still 0, and no
	// table of its own, is an empty database rather than somebody else's, and
	// init adopts it and stamps it. That covers what a crashed init and a bare
	// sqlite3 alike leave behind.
	if adopt && appID == 0 {
		userVersion, err := readInt32Pragma(ctx, db, "user_version")
		if err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read %s", path)
		}
		if userVersion == 0 {
			tables, err := countTables(ctx, db)
			if err != nil {
				return awberr.Wrap(awberr.Runtime, err, "read %s", path)
			}
			if tables == 0 {
				return nil
			}
		}
	}

	return awberr.Runtimef(
		"%s is not an awb database: remove the file or point --db or AWB_DB elsewhere", path)
}

func readInt32Pragma(ctx context.Context, db *sql.DB, name string) (int32, error) {
	var value int32
	// The name is a constant from this package, never caller input.
	if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

func countTables(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return n, err
}

// isEmptyFile reports whether path is missing or zero-length. A zero-length
// file counts as missing, so what touch leaves behind is not something another
// command quietly fills in.
func isEmptyFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "stat %s", path)
	}
	if info.IsDir() {
		return false, awberr.Runtimef("%s is a directory, not a database file", path)
	}
	return info.Size() == 0, nil
}
