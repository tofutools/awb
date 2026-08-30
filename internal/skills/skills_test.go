package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentGuideComesFromBundledSkill(t *testing.T) {
	parts := bytes.SplitN(definition, []byte("---\n"), 3)
	require.Len(t, parts, 3)
	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	require.NoError(t, yaml.Unmarshal(parts[1], &frontmatter))
	assert.Equal(t, "awb", frontmatter.Name)
	assert.NotEmpty(t, frontmatter.Description)
	assert.LessOrEqual(t, utf8.RuneCountInString(frontmatter.Description), 1024)
	assert.Contains(t, AgentGuide, "awb ready --compact")
	assert.NotContains(t, AgentGuide, "name: awb")
}

func TestInstallAllHarnessesInTheirUserRoots(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex-home")
	configHome := filepath.Join(home, "config-home")
	copilotHome := filepath.Join(home, "copilot-home")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("COPILOT_HOME", copilotHome)

	installed, err := Install(nil)
	require.NoError(t, err)
	require.Len(t, installed, 5)

	want := []string{
		filepath.Join(home, ".claude", "skills", "awb"),
		filepath.Join(home, ".agents", "skills", "awb"),
		filepath.Join(codexHome, "skills", "awb"),
		filepath.Join(configHome, "opencode", "skills", "awb"),
		filepath.Join(copilotHome, "skills", "awb"),
	}
	for i, path := range want {
		assert.Equal(t, path, installed[i].Path)
		got, readErr := os.ReadFile(filepath.Join(path, "SKILL.md"))
		require.NoError(t, readErr)
		assert.Equal(t, definition, got)
	}
}

func TestInstallFiltersDeduplicatesAndRefreshes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".agents"))

	installed, err := Install([]string{"codex", "codex"})
	require.NoError(t, err)
	require.Len(t, installed, 1, "the two Codex roots coincide in this configuration")
	path := filepath.Join(installed[0].Path, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o600))

	installed, err = Install([]string{"codex"})
	require.NoError(t, err)
	require.Len(t, installed, 1)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, definition, got)
}

func TestInstallAllOverridesOtherSelections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, "copilot"))

	installed, err := Install([]string{"claude", "all", "claude"})
	require.NoError(t, err)
	assert.Len(t, installed, 5)
}
