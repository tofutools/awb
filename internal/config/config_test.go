package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/config"
)

// isolate points the XDG directories at a scratch tree and clears every awb
// variable, so a test never reads the developer's own configuration.
func isolate(t *testing.T) (configDir, dataDir string) {
	t.Helper()
	root := t.TempDir()
	configDir = filepath.Join(root, "config")
	dataDir = filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "awb"), 0o700))

	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	for _, name := range []string{
		"AWB_DB", "AWB_ATTACHMENTS", "AWB_USER", "AWB_PASSWORD", "AWB_IDENTITY", "AWB_PROJECT",
		"AWB_COLOR", "AWB_CONFIG_FILE", "NO_COLOR",
	} {
		t.Setenv(name, "")
	}
	return configDir, dataDir
}

func writeUserConfig(t *testing.T, configDir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "awb", "config.yaml"), []byte(content), 0o600))
}

// workdir builds a directory tree and drops a local configuration file at the
// given relative path, returning the deepest directory.
func workdir(t *testing.T, localPath, content string) string {
	t.Helper()
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deep, 0o700))
	if localPath != "" {
		full := filepath.Join(root, localPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return deep
}

func TestDefaults(t *testing.T) {
	_, dataDir := isolate(t)

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "awb", "awb.db"), cfg.DB)
	assert.Equal(t, filepath.Join(dataDir, "awb", "attachments"), cfg.Attachments,
		"beside the database unless something says otherwise")
	assert.False(t, cfg.Remote())
	assert.Equal(t, config.ColorAuto, cfg.Color)
	assert.Empty(t, cfg.ContextProject)
	assert.Empty(t, cfg.ContextLabel)
}

// Command line flags, then environment variables, then the local file, then
// the user file, then the defaults.
func TestDBPrecedence(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "db: /from/user/config\n")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/user/config", cfg.DB)

	t.Setenv("AWB_DB", "/from/env")
	cfg, err = config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/env", cfg.DB)

	flag := "/from/flag"
	cfg, err = config.Load(config.Flags{DB: &flag}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/flag", cfg.DB)
}

// A leading home-directory alias in a path-valued environment variable is
// expanded here because shells do not necessarily expand one in assignments.
func TestEnvironmentPathsExpandHomeDirectoryAlias(t *testing.T) {
	configDir, _ := isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	relocated := filepath.Join(home, "config", "awb.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(relocated), 0o700))
	require.NoError(t, os.WriteFile(relocated, []byte("project: chosen\n"), 0o600))
	t.Setenv("AWB_CONFIG_FILE", "~/config/awb.yaml")
	t.Setenv("AWB_DB", "~/data/awb.db")
	t.Setenv("AWB_ATTACHMENTS", "~/data/blobs")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "chosen", cfg.DefaultProject)
	assert.Equal(t, filepath.Join(home, "data", "awb.db"), cfg.DB)
	assert.Equal(t, filepath.Join(home, "data", "blobs"), cfg.Attachments)

	// The default file must not take part when AWB_CONFIG_FILE is set.
	assert.NoFileExists(t, filepath.Join(configDir, "awb", "config.yaml"))
}

// The local file cannot redirect where your issues are stored, so db there is
// unread — not merely overridden.
func TestLocalFileCannotSetProtectedKeys(t *testing.T) {
	configDir, dataDir := isolate(t)
	writeUserConfig(t, configDir, "identity: mikael\n")

	dir := workdir(t, ".awb.yaml", `
db: /somewhere/else
user: someone
password: hunter2
identity: impostor
color: always
project: awb
`)

	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "awb", "awb.db"), cfg.DB)
	assert.Empty(t, cfg.User)
	assert.Empty(t, cfg.Password)
	assert.Equal(t, "mikael", cfg.Identity)
	assert.Equal(t, config.ColorAuto, cfg.Color)
	assert.Equal(t, "awb", cfg.ContextProject, "project and label are the two keys it may set")
}

// Ignored means unread: their values are not type-checked either, so only
// project and label can make this file fail a command.
func TestLocalFileProtectedKeysAreNotEvenTypeChecked(t *testing.T) {
	isolate(t)
	dir := workdir(t, ".awb.yaml", "db: [not, a, string]\ncolor: 42\nproject: awb\n")

	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Equal(t, "awb", cfg.ContextProject)
}

func TestUpwardSearchStopsAtTheFirstFile(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".awb.yaml"),
		[]byte("project: outer\nlabel: outerlabel\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", ".awb.yaml"),
		[]byte("project: inner\n"), 0o600))

	cfg, err := config.Load(config.Flags{}, deep)
	require.NoError(t, err)
	assert.Equal(t, "inner", cfg.ContextProject)
	assert.Empty(t, cfg.ContextLabel, "files further up are neither read nor merged")
}

func TestNoContextIgnoresTheLocalFile(t *testing.T) {
	isolate(t)
	dir := workdir(t, ".awb.yaml", "project: awb\nlabel: frontend\n")

	cfg, err := config.Load(config.Flags{NoContext: true}, dir)
	require.NoError(t, err)
	assert.Empty(t, cfg.ContextProject)
	assert.Empty(t, cfg.ContextLabel)
}

// --no-context does not stop the file from being read, so a malformed one
// still fails the command.
func TestNoContextStillReadsTheFile(t *testing.T) {
	isolate(t)
	dir := workdir(t, ".awb.yaml", "project: [malformed\n")

	_, err := config.Load(config.Flags{NoContext: true}, dir)
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))
}

// A bad value in a configuration file is a configuration error (exit 1) naming
// the file; the same bad value from a flag or a variable is a usage error
// (exit 2).
func TestBadValueClassification(t *testing.T) {
	configDir, _ := isolate(t)

	writeUserConfig(t, configDir, "color: purple\n")
	_, err := config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))
	assert.Contains(t, err.Error(), "config.yaml", "the message names the file")

	writeUserConfig(t, configDir, "")
	t.Setenv("AWB_COLOR", "purple")
	_, err = config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))

	t.Setenv("AWB_COLOR", "")
	bad := "purple"
	_, err = config.Load(config.Flags{Color: &bad}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))
}

func TestLocalFileBadValueIsAConfigError(t *testing.T) {
	isolate(t)
	for _, content := range []string{"project: NotAKey\n", "label: Not A Label\n"} {
		dir := workdir(t, ".awb.yaml", content)
		_, err := config.Load(config.Flags{}, dir)
		require.Error(t, err, content)
		assert.Equal(t, 1, awberr.ExitCode(err), content)
		assert.Contains(t, err.Error(), config.LocalFileName)
	}
}

// Unknown keys are ignored, so future versions can add settings without
// breaking older binaries.
func TestUnknownKeysAreIgnored(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "identity: mikael\nsomething_new: yes\n")

	dir := workdir(t, ".awb.yaml", "project: awb\nfuture_key: 1\n")
	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Equal(t, "mikael", cfg.Identity)
	assert.Equal(t, "awb", cfg.ContextProject)
}

func TestIdentityFallsBackToUser(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "user: mikael\n")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "mikael", cfg.Identity)
	assert.Equal(t, "mikael", cfg.User)
}

func TestIdentityFallsBackToTheOSUsername(t *testing.T) {
	isolate(t)
	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.FoldOSUsername(), cfg.Identity)
}

// An identity supplied by the user is rejected rather than folded.
func TestIdentityFromConfigIsRejectedNotFolded(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "identity: Mikael\n")

	_, err := config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))

	writeUserConfig(t, configDir, "")
	t.Setenv("AWB_IDENTITY", "Mikael")
	_, err = config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))
}

func TestRemoteURL(t *testing.T) {
	isolate(t)

	for _, raw := range []string{
		"http://127.0.0.1:7777",
		"https://host/awb/",
		"https://host/awb",
	} {
		t.Setenv("AWB_DB", raw)
		cfg, err := config.Load(config.Flags{}, t.TempDir())
		require.NoError(t, err, raw)
		assert.True(t, cfg.Remote(), raw)
		assert.NotContains(t, cfg.RemoteURL.Path, "//")
		assert.False(t, len(cfg.RemoteURL.Path) > 0 &&
			cfg.RemoteURL.Path[len(cfg.RemoteURL.Path)-1] == '/',
			"a trailing slash is optional and means the same either way")
	}
}

func TestRemoteURLRefusals(t *testing.T) {
	isolate(t)

	for _, raw := range []string{
		"https://host/?a=b",
		"https://host/#frag",
		"https://user:pass@host/",
		"https://",
	} {
		t.Setenv("AWB_DB", raw)
		_, err := config.Load(config.Flags{}, t.TempDir())
		require.Error(t, err, raw)
		assert.Equal(t, 2, awberr.ExitCode(err), raw)
	}
}

// A path is a path: a value that is not an http(s) URL is never parsed as one.
func TestPathsAreNotURLs(t *testing.T) {
	isolate(t)
	t.Setenv("AWB_DB", "/tmp/a file?with#odd chars.db")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.False(t, cfg.Remote())
	assert.Equal(t, "/tmp/a file?with#odd chars.db", cfg.DB)
}

// The project resolved from any configuration source is the default used by
// both creation and issue listings. ContextProject separately records whether
// that default came from the directory, so --no-context can remove it.
func TestDefaultProjectVersusContextProject(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "project: fromuser\n")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "fromuser", cfg.DefaultProject)
	assert.Empty(t, cfg.ContextProject, "it did not come from directory context")

	// The local file's project is both the creation default and the filter.
	dir := workdir(t, ".awb.yaml", "project: fromdir\n")
	cfg, err = config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Equal(t, "fromdir", cfg.DefaultProject)
	assert.Equal(t, "fromdir", cfg.ContextProject)
}

// An exported AWB_PROJECT outranks the directory's own project, while the
// directory label remains independently in effect.
func TestEnvProjectOutranksTheDirectory(t *testing.T) {
	isolate(t)
	t.Setenv("AWB_PROJECT", "fromenv")
	dir := workdir(t, ".awb.yaml", "project: fromdir\nlabel: frontend\n")

	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Equal(t, "fromenv", cfg.DefaultProject, "the variable is a deliberate override")
	assert.Equal(t, "fromdir", cfg.ContextProject, "the directory source remains recorded")
	assert.Equal(t, "frontend", cfg.ContextLabel)
}

// --no-context removes the local file from the creation chain but not the
// rest.
func TestNoContextLeavesTheUserFileCreationDefault(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "project: fromuser\n")
	dir := workdir(t, ".awb.yaml", "project: fromdir\n")

	cfg, err := config.Load(config.Flags{NoContext: true}, dir)
	require.NoError(t, err)
	assert.Equal(t, "fromuser", cfg.DefaultProject)
	assert.Empty(t, cfg.ContextProject)
}

// NO_COLOR sits between the command line and everything else.
func TestColorChain(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "color: always\n")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorAlways, cfg.Color)

	t.Setenv("AWB_COLOR", "auto")
	cfg, err = config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorAuto, cfg.Color)

	// A NO_COLOR that is set and not empty overrides AWB_COLOR and the file.
	t.Setenv("NO_COLOR", "1")
	cfg, err = config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorNever, cfg.Color)

	// An explicit flag overrides NO_COLOR.
	always := "always"
	cfg, err = config.Load(config.Flags{Color: &always}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorAlways, cfg.Color)

	cfg, err = config.Load(config.Flags{NoColor: true}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorNever, cfg.Color)

	// An empty NO_COLOR means nothing at all.
	t.Setenv("NO_COLOR", "")
	t.Setenv("AWB_COLOR", "always")
	cfg, err = config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorAlways, cfg.Color)
}

func TestAWBPasswordMayStandAlone(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "user: mikael\n")
	t.Setenv("AWB_PASSWORD", "hunter2")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "mikael", cfg.User)
	assert.Equal(t, "hunter2", cfg.Password)
}

// A file with neither key is legal and simply gives an empty context.
func TestEmptyLocalFileIsLegal(t *testing.T) {
	isolate(t)
	dir := workdir(t, ".awb.yaml", "")

	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Empty(t, cfg.ContextProject)
	assert.Empty(t, cfg.ContextLabel)
	assert.NotEmpty(t, cfg.LocalFilePath, "the file was still found")
}

// The attachments directory follows the same chain as the database: the flag,
// then the variable, then the user file, then a directory beside the database.
func TestAttachmentsPrecedence(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "db: /files/awb.db\nattachments: /from/file\n")

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/file", cfg.Attachments)

	t.Setenv("AWB_ATTACHMENTS", "/from/env")
	cfg, err = config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/env", cfg.Attachments)

	flag := "/from/flag"
	cfg, err = config.Load(config.Flags{Attachments: &flag}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "/from/flag", cfg.Attachments)
}

// With no setting at all it sits beside whatever database is in force, which
// is what makes a copied pair of them travel together.
func TestAttachmentsDefaultsBesideTheDatabase(t *testing.T) {
	isolate(t)

	db := "/files/tracker/awb.db"
	cfg, err := config.Load(config.Flags{DB: &db}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/files/tracker", "attachments"), cfg.Attachments)
}

// Remote mode has no attachments directory: the server holds the files.
func TestAttachmentsAreEmptyInRemoteMode(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "attachments: /from/file\n")

	db := "https://awb.example.com/"
	cfg, err := config.Load(config.Flags{DB: &db}, t.TempDir())
	require.NoError(t, err)
	assert.True(t, cfg.Remote())
	assert.Empty(t, cfg.Attachments)
}

// An empty setting is a mistake to report rather than a fall back to the
// default, exactly as an empty database location is.
func TestEmptyAttachmentsIsRefused(t *testing.T) {
	configDir, _ := isolate(t)

	empty := ""
	_, err := config.Load(config.Flags{Attachments: &empty}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))

	writeUserConfig(t, configDir, "attachments: \"\"\n")
	_, err = config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))
}

// The local file may not redirect where files are stored, exactly as it may
// not redirect where issues are.
func TestLocalFileCannotSetAttachments(t *testing.T) {
	_, dataDir := isolate(t)
	dir := workdir(t, ".awb.yaml", "project: awb\nattachments: /somewhere/else\n")

	cfg, err := config.Load(config.Flags{}, dir)
	require.NoError(t, err)
	assert.Equal(t, "awb", cfg.ContextProject)
	assert.Equal(t, filepath.Join(dataDir, "awb", "attachments"), cfg.Attachments)
}

// AWB_CONFIG_FILE moves the user configuration file, and the one at the
// default path is then not read at all.
func TestConfigFileEnvOverridesThePath(t *testing.T) {
	configDir, _ := isolate(t)
	writeUserConfig(t, configDir, "project: ignored\ncolor: always\n")

	elsewhere := filepath.Join(t.TempDir(), "elsewhere.yaml")
	require.NoError(t, os.WriteFile(elsewhere, []byte("project: chosen\n"), 0o600))
	t.Setenv("AWB_CONFIG_FILE", elsewhere)

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "chosen", cfg.DefaultProject)
	assert.Equal(t, config.ColorAuto, cfg.Color, "the file at the default path is not read")
}

// A bad value in the named file is still a configuration error, and the
// message names that file rather than the default one.
func TestConfigFileEnvErrorNamesTheNamedFile(t *testing.T) {
	isolate(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.yaml")
	require.NoError(t, os.WriteFile(elsewhere, []byte("color: purple\n"), 0o600))
	t.Setenv("AWB_CONFIG_FILE", elsewhere)

	_, err := config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))
	assert.Contains(t, err.Error(), elsewhere)
}

// A file named by the variable must exist: pointing at one that does not is a
// usage error rather than a silent fall back to the defaults, so a typo in the
// path cannot go unnoticed.
func TestConfigFileEnvMissingFileIsAUsageError(t *testing.T) {
	isolate(t)
	missing := filepath.Join(t.TempDir(), "nothing-here.yaml")
	t.Setenv("AWB_CONFIG_FILE", missing)

	_, err := config.Load(config.Flags{}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))
	assert.Contains(t, err.Error(), "AWB_CONFIG_FILE")
	assert.Contains(t, err.Error(), missing)
}

// The default path carries no such obligation: nobody named it, so its absence
// simply means no user configuration.
func TestMissingDefaultUserFileIsLegal(t *testing.T) {
	configDir, _ := isolate(t)
	require.NoError(t, os.RemoveAll(filepath.Join(configDir, "awb")))

	cfg, err := config.Load(config.Flags{}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, config.ColorAuto, cfg.Color)
}
