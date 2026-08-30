package storage_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/tofutools/awb/internal/storage"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "awb.db")
}

func TestInitCreatesAndStamps(t *testing.T) {
	path := tempPath(t)

	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()

	var appID int32
	require.NoError(t, raw.QueryRow("PRAGMA application_id").Scan(&appID))
	assert.Equal(t, storage.ApplicationID, appID)

	var version int
	require.NoError(t, raw.QueryRow("PRAGMA user_version").Scan(&version))
	assert.Positive(t, version)

	var mode string
	require.NoError(t, raw.QueryRow("PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode)
}

func TestInitCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "awb.db")

	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = os.Stat(path)
	assert.NoError(t, err)
}

func TestInitIsIdempotent(t *testing.T) {
	path := tempPath(t)
	for range 3 {
		db, err := storage.Init(t.Context(), path)
		require.NoError(t, err)
		require.NoError(t, db.Close())
	}
}

// Only init creates a database, so that a typo cannot silently produce a
// second, empty tracker.
func TestOpenRefusesMissingDatabase(t *testing.T) {
	path := tempPath(t)

	_, err := storage.Open(t.Context(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path, "the message names the path")
	assert.Contains(t, err.Error(), "awb init")

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "a failed open must not create the file")
}

// A zero-length file counts as missing, so what touch leaves behind is not
// something another command quietly fills in.
func TestOpenRefusesZeroLengthFile(t *testing.T) {
	path := tempPath(t)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	_, err := storage.Open(t.Context(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

func TestInitAcceptsZeroLengthFile(t *testing.T) {
	path := tempPath(t)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err, "only init treats an empty file as one to create the schema in")
	require.NoError(t, db.Close())
}

// An interrupted init leaves a readable SQLite database with both pragmas still
// zero and no table of its own; init adopts it and stamps it.
func TestInitAdoptsAnEmptySQLiteDatabase(t *testing.T) {
	path := tempPath(t)

	// MigrateStrict sets WAL before running the first batch, so an init
	// interrupted between the two leaves exactly this: a real SQLite header,
	// both pragmas still zero, and no table.
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("PRAGMA journal_mode = WAL")
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NotZero(t, info.Size(), "the file must be a real, non-empty SQLite header")

	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

// Anything else unstamped stays refused, for every command including init.
func TestUnstampedDatabaseIsRefused(t *testing.T) {
	path := tempPath(t)

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("CREATE TABLE somebody_elses (x TEXT)")
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	for name, open := range map[string]func() (*storage.DB, error){
		"open": func() (*storage.DB, error) { return storage.Open(t.Context(), path) },
		"init": func() (*storage.DB, error) { return storage.Init(t.Context(), path) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := open()
			require.Error(t, err)
			assert.Contains(t, err.Error(), path, "the message names the path")
			assert.Contains(t, err.Error(), "remove the file or point",
				"the refusal says how to get out of it")
		})
	}
}

func TestNonSQLiteFileIsRefused(t *testing.T) {
	path := tempPath(t)
	require.NoError(t, os.WriteFile(path, []byte("this is not a database at all"), 0o600))

	_, err := storage.Open(t.Context(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

func TestOpenExistingAwbDatabase(t *testing.T) {
	path := tempPath(t)
	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = storage.Open(t.Context(), path)
	require.NoError(t, err)
	assert.Equal(t, path, db.Path())
	require.NoError(t, db.Close())
}

// A binary refuses to open a database whose user_version is higher than the
// number of batches it carries, rather than operating on a schema it does not
// understand.
func TestSchemaFromTheFutureIsRefused(t *testing.T) {
	path := tempPath(t)
	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("PRAGMA user_version = 999")
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	_, err = storage.Open(t.Context(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer version of awb")
}

// A database left at an older schema version is brought forward when it is
// opened, which is what an existing tracker gaining attachments, and then
// users and activity, looks like. The batches are replayed here by winding the version back
// to 1 and dropping everything they created, so what is exercised is the
// migrations rather than a fresh schema.
func TestOpeningMigratesForward(t *testing.T) {
	path := tempPath(t)
	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	var latest int
	require.NoError(t, raw.QueryRow("PRAGMA user_version").Scan(&latest))
	require.Greater(t, latest, 1, "there is more than one batch to migrate through")

	for _, table := range []string{"attachments", "project_members", "users", "issue_activity"} {
		_, err = raw.Exec("DROP TABLE " + table)
		require.NoError(t, err, table)
	}
	_, err = raw.Exec("PRAGMA user_version = 1")
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err = storage.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	raw, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()

	var version int
	require.NoError(t, raw.QueryRow("PRAGMA user_version").Scan(&version))
	assert.Equal(t, latest, version)

	for _, table := range []string{"attachments", "project_members", "users", "issue_activity"} {
		var name string
		require.NoError(t, raw.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table).Scan(&name), table)
		assert.Equal(t, table, name)
	}
}

func TestOpenCurrentIsReadOnlyAndDoesNotMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awb.db")
	db, err := storage.Init(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	readOnly, err := storage.OpenCurrent(t.Context(), path)
	require.NoError(t, err)
	_, err = readOnly.SQL().ExecContext(t.Context(), "CREATE TABLE forbidden (n INTEGER)")
	require.Error(t, err)
	require.NoError(t, readOnly.Close())

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("PRAGMA user_version = 1")
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	_, err = storage.OpenCurrent(t.Context(), path)
	require.Error(t, err)
	raw, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close() //nolint:errcheck
	var version int
	require.NoError(t, raw.QueryRow("PRAGMA user_version").Scan(&version))
	assert.Equal(t, 1, version, "completion opening must not migrate")
}
