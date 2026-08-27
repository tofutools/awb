// Package config resolves everything awb needs to know before it can do
// anything: which database to use, who the caller is, what the working
// directory means, and whether to colour the output.
//
// The precedence is always the same — command line flags, then environment
// variables, then the local configuration file, then the user configuration
// file, then the built-in defaults — and the two files differ in what they are
// allowed to say, because one of them may have been committed by somebody
// else.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// LocalFileName is the file whose presence is the whole of the
// directory-context mechanism. Only this exact spelling is looked for;
// ".awb.yml" is not searched.
const LocalFileName = ".awb.yaml"

// ColorMode is when to colour the default output mode.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// ParseColorMode validates a colour setting.
func ParseColorMode(s string) (ColorMode, error) {
	switch ColorMode(s) {
	case ColorAuto, ColorAlways, ColorNever:
		return ColorMode(s), nil
	default:
		return "", fmt.Errorf("invalid color %q: must be auto, always or never", s)
	}
}

// userFile is the user configuration file. Every key is optional.
type userFile struct {
	DB       *string `yaml:"db"`
	User     *string `yaml:"user"`
	Password *string `yaml:"password"`
	Identity *string `yaml:"identity"`
	Project  *string `yaml:"project"`
	Color    *string `yaml:"color"`
}

// localFile is the local configuration file.
//
// It holds only project and label on purpose. The file is meant to be
// committed, so it may not have been written by the person running the
// command, and it therefore may not set db, user, password, identity or color:
// a directory can shape what you see, but cannot redirect where your issues
// are stored, claim to be you, or make you send a password somewhere. Those
// keys are ignored if present, exactly like unknown keys — and ignored means
// unread, so their values are not type-checked either.
type localFile struct {
	Project *string `yaml:"project"`
	Label   *string `yaml:"label"`
}

// Flags are the values the command line supplied, each nil when the flag was
// not given.
type Flags struct {
	DB        *string
	Color     *string
	NoColor   bool
	NoContext bool
}

// Config is everything resolved, ready to use.
type Config struct {
	// DB is a filesystem path (direct mode) or an http(s) URL (remote mode).
	DB string
	// RemoteURL is set when DB is a URL, already validated and normalised.
	RemoteURL *url.URL

	// User and Password are the basic-authentication credentials the CLI presents
	// in remote mode. They are ignored when DB is a path.
	User     string
	Password string

	// Identity is the default assignee: what --mine resolves to and what claim
	// uses without --as. It may be empty, in which case the commands that need
	// one fail asking for it to be set.
	Identity string

	// CreateProject is the default project for awb create, resolved through the
	// full precedence chain.
	CreateProject string

	// ContextProject and ContextLabel are the directory's own scope. They are
	// empty when --no-context was given or no local file was found.
	ContextProject string
	ContextLabel   string

	Color ColorMode

	// LocalFilePath is the local configuration file that was used, for error
	// messages. It is empty when none was found.
	LocalFilePath string
}

// Remote reports whether the CLI should talk to a server rather than open a
// file.
func (c *Config) Remote() bool { return c.RemoteURL != nil }

// Load resolves the configuration for one invocation.
//
// workingDir is the directory the command was run in; it is resolved through
// symlinks before the upward search begins.
func Load(flags Flags, workingDir string) (*Config, error) {
	userCfg, userPath, err := loadUserFile()
	if err != nil {
		return nil, err
	}

	localCfg, localPath, err := loadLocalFile(workingDir)
	if err != nil {
		return nil, err
	}

	cfg := &Config{LocalFilePath: localPath}

	if err := resolveDB(cfg, flags, userCfg, userPath); err != nil {
		return nil, err
	}
	if err := resolveCredentials(cfg, userCfg, userPath); err != nil {
		return nil, err
	}
	if err := resolveIdentity(cfg, userCfg, userPath); err != nil {
		return nil, err
	}
	if err := resolveContext(cfg, flags, localCfg, localPath); err != nil {
		return nil, err
	}
	if err := resolveCreateProject(cfg, userCfg, userPath); err != nil {
		return nil, err
	}
	if err := resolveColor(cfg, flags, userCfg, userPath); err != nil {
		return nil, err
	}
	return cfg, nil
}

// configError reports a problem with a configuration file. An unreadable,
// malformed or wrongly typed file, or a recognised key whose value violates
// that setting's vocabulary, fails the command with exit code 1 and a message
// naming the file — because silently falling back to defaults would hide the
// reason a command wrote to the wrong database or picked the wrong project.
func configError(path string, err error) error {
	return awberr.Runtimef("%s: %s", path, err.Error())
}

// usageError reports the same bad value arriving from a flag or an environment
// variable, which is a usage error rather than a configuration one.
func usageError(source string, err error) error {
	return awberr.Usagef("%s: %s", source, err.Error())
}

func loadUserFile() (*userFile, string, error) {
	path := filepath.Join(configHome(), "awb", "config.yaml")
	var cfg userFile
	found, err := readYAML(path, &cfg)
	if err != nil {
		return nil, path, err
	}
	if !found {
		return &userFile{}, path, nil
	}
	return &cfg, path, nil
}

// loadLocalFile performs the upward search: start at the working directory
// with symlinks resolved and walk up towards the filesystem root looking for
// .awb.yaml. The first one found is the local configuration file, and the
// search stops there — files further up are neither read nor merged.
func loadLocalFile(workingDir string) (*localFile, string, error) {
	dir, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		// A working directory that cannot be resolved simply yields no context; it
		// is not worth failing a command that needs none.
		dir = workingDir
	}

	for {
		path := filepath.Join(dir, LocalFileName)
		var cfg localFile
		found, err := readYAML(path, &cfg)
		if err != nil {
			return nil, path, err
		}
		if found {
			return &cfg, path, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return &localFile{}, "", nil
		}
		dir = parent
	}
}

// readYAML reads a configuration file into out. It reports whether the file
// existed. Unknown keys are ignored, so future versions can add settings
// without breaking older binaries.
func readYAML(path string, out any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, configError(path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return false, configError(path, err)
	}
	return true, nil
}

func resolveDB(cfg *Config, flags Flags, userCfg *userFile, userPath string) error {
	// The local configuration file cannot set db, so it takes no part.
	switch {
	case flags.DB != nil:
		if err := setDB(cfg, *flags.DB); err != nil {
			return usageError("--db", err)
		}
	case os.Getenv("AWB_DB") != "":
		if err := setDB(cfg, os.Getenv("AWB_DB")); err != nil {
			return usageError("AWB_DB", err)
		}
	case userCfg.DB != nil:
		if err := setDB(cfg, *userCfg.DB); err != nil {
			return configError(userPath, err)
		}
	default:
		cfg.DB = filepath.Join(dataHome(), "awb", "awb.db")
	}
	return nil
}

// setDB records a database location, which is either a filesystem path or an
// http(s) URL.
func setDB(cfg *Config, value string) error {
	if value == "" {
		return errors.New("database location must not be empty")
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		cfg.DB = value
		cfg.RemoteURL = nil
		return nil
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid database URL %q: %w", value, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid database URL %q: it names no host", value)
	}
	// A URL carrying a query or a fragment is refused, having no meaning here.
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid database URL %q: it must carry no query or fragment", value)
	}
	// Userinfo is refused rather than being a second place to keep a password,
	// and the one most likely to leak into a shell history or a process listing.
	if parsed.User != nil {
		return fmt.Errorf(
			"invalid database URL %q: put credentials in \"user\" and \"password\", or AWB_USER and AWB_PASSWORD",
			value)
	}

	// A remote URL may carry a path, which is the base the API paths hang under.
	// A trailing slash is optional and means the same either way.
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")

	cfg.DB = value
	cfg.RemoteURL = parsed
	return nil
}

func resolveCredentials(cfg *Config, userCfg *userFile, userPath string) error {
	// Either may stand alone: a server may want a username with an empty
	// password, and AWB_PASSWORD exists so the secret need not be written into
	// the file at all.
	switch {
	case os.Getenv("AWB_USER") != "":
		value := os.Getenv("AWB_USER")
		if _, err := domain.ValidateAssignee(value); err != nil {
			return usageError("AWB_USER", err)
		}
		cfg.User = value
	case userCfg.User != nil:
		if _, err := domain.ValidateAssignee(*userCfg.User); err != nil {
			return configError(userPath, err)
		}
		cfg.User = *userCfg.User
	}

	if value, set := os.LookupEnv("AWB_PASSWORD"); set {
		cfg.Password = value
	} else if userCfg.Password != nil {
		cfg.Password = *userCfg.Password
	}
	return nil
}

// resolveIdentity resolves who the caller is. When identity is unset it
// defaults to user if that is set, and otherwise to the OS username,
// lower-cased and stripped of any character outside the assignee set — a value
// the user never typed, which is why it is folded rather than refused.
func resolveIdentity(cfg *Config, userCfg *userFile, userPath string) error {
	switch {
	case os.Getenv("AWB_IDENTITY") != "":
		value := os.Getenv("AWB_IDENTITY")
		if _, err := domain.ValidateAssignee(value); err != nil {
			return usageError("AWB_IDENTITY", err)
		}
		cfg.Identity = value
		return nil
	case userCfg.Identity != nil:
		if _, err := domain.ValidateAssignee(*userCfg.Identity); err != nil {
			return configError(userPath, err)
		}
		cfg.Identity = *userCfg.Identity
		return nil
	case cfg.User != "":
		cfg.Identity = cfg.User
		return nil
	default:
		cfg.Identity = FoldOSUsername()
		return nil
	}
}

// FoldOSUsername is the last fallback for an identity: the OS username reduced
// to the assignee vocabulary, so a name like "Mikael" or a Windows
// "DOMAIN\user" still yields a usable one. It returns "" when nothing is left.
func FoldOSUsername() string {
	current, err := user.Current()
	if err != nil {
		return ""
	}
	return domain.FoldToAssignee(current.Username)
}

// resolveContext reads the directory's own scope. --no-context ignores the
// project and label of the local configuration file for this invocation,
// restoring the view of the whole database — but it does not stop the file
// from being read, so a malformed one still fails the command.
func resolveContext(cfg *Config, flags Flags, localCfg *localFile, localPath string) error {
	if flags.NoContext {
		return nil
	}
	if localCfg.Project != nil {
		if _, err := domain.ValidateProjectKey(*localCfg.Project); err != nil {
			return configError(localPath, err)
		}
		cfg.ContextProject = *localCfg.Project
	}
	if localCfg.Label != nil {
		if _, err := domain.ValidateLabel(*localCfg.Label); err != nil {
			return configError(localPath, err)
		}
		cfg.ContextLabel = *localCfg.Label
	}
	return nil
}

// resolveCreateProject applies the precedence chain for awb create's default
// project: --project, else AWB_PROJECT, else project in the local
// configuration file, else project in the user configuration file.
//
// --project is a per-command flag rather than a global one, so it is applied
// by the command itself; what is resolved here is everything below it.
//
// Note the documented consequence: an exported AWB_PROJECT outranks the
// directory's own project, so awb create run in a directory scoped to another
// project puts the issue where the variable says. The variable is a deliberate
// override and wins as one.
func resolveCreateProject(cfg *Config, userCfg *userFile, userPath string) error {
	if value := os.Getenv("AWB_PROJECT"); value != "" {
		if _, err := domain.ValidateProjectKey(value); err != nil {
			return usageError("AWB_PROJECT", err)
		}
		cfg.CreateProject = value
		return nil
	}
	// --no-context removes the local file from this chain but not the rest, which
	// is what ContextProject already being empty expresses.
	if cfg.ContextProject != "" {
		cfg.CreateProject = cfg.ContextProject
		return nil
	}
	if userCfg.Project != nil {
		if _, err := domain.ValidateProjectKey(*userCfg.Project); err != nil {
			return configError(userPath, err)
		}
		cfg.CreateProject = *userCfg.Project
	}
	return nil
}

// resolveColor applies the colour chain, in which NO_COLOR sits between the
// command line and everything else: --color and --no-color override it, an
// explicit flag being what the person running the command means, and it
// overrides AWB_COLOR, the configuration file and the default.
func resolveColor(cfg *Config, flags Flags, userCfg *userFile, userPath string) error {
	cfg.Color = ColorAuto

	switch {
	case flags.NoColor:
		cfg.Color = ColorNever
		return nil
	case flags.Color != nil:
		mode, err := ParseColorMode(*flags.Color)
		if err != nil {
			return usageError("--color", err)
		}
		cfg.Color = mode
		return nil
	}

	// A NO_COLOR variable that is set and not empty means never, as the
	// convention that defines it says; an empty one means nothing at all.
	if os.Getenv("NO_COLOR") != "" {
		cfg.Color = ColorNever
		return nil
	}

	if value := os.Getenv("AWB_COLOR"); value != "" {
		mode, err := ParseColorMode(value)
		if err != nil {
			return usageError("AWB_COLOR", err)
		}
		cfg.Color = mode
		return nil
	}
	if userCfg.Color != nil {
		mode, err := ParseColorMode(*userCfg.Color)
		if err != nil {
			return configError(userPath, err)
		}
		cfg.Color = mode
	}
	return nil
}

// configHome is $XDG_CONFIG_HOME, falling back to ~/.config.
func configHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config"
	}
	return filepath.Join(home, ".config")
}

// dataHome is $XDG_DATA_HOME, falling back to ~/.local/share.
func dataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share")
	}
	return filepath.Join(home, ".local", "share")
}
