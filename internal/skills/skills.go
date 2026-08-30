// Package skills carries awb's bundled agent skill and installs it into the
// user-scope skill directories understood by supported agent harnesses.
package skills

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const skillName = "awb"

// definition is the canonical copy shipped in the awb binary.
//
//go:embed awb/SKILL.md
var definition []byte

// AgentGuide is the body of the bundled skill. The older agent-guide command
// prints this same text, so the two ways of teaching an agent cannot drift.
var AgentGuide = func() string {
	parts := bytes.SplitN(definition, []byte("---\n"), 3)
	if len(parts) != 3 || len(parts[0]) != 0 {
		panic("bundled awb skill has malformed frontmatter")
	}
	return string(parts[2])
}()

// Installed describes one materialised copy of the bundled skill.
type Installed struct {
	Harness string
	Path    string
}

// Install writes the bundled skill for the selected harnesses. An empty
// selection and "all" both mean every supported harness. Repeated selections
// and roots shared through environment configuration are written only once.
func Install(selected []string) ([]Installed, error) {
	harnesses := selectedHarnesses(selected)
	home, err := userHome()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var installed []Installed
	for _, harness := range harnesses {
		roots, err := harnessRoots(harness, home)
		if err != nil {
			return installed, err
		}
		for _, root := range roots {
			dst := filepath.Join(root, skillName)
			if seen[dst] {
				continue
			}
			seen[dst] = true
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return installed, fmt.Errorf("create skill directory %s: %w", dst, err)
			}
			if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), definition, 0o644); err != nil { //nolint:gosec // user documentation
				return installed, fmt.Errorf("write skill in %s: %w", dst, err)
			}
			installed = append(installed, Installed{Harness: harness, Path: dst})
		}
	}
	return installed, nil
}

func selectedHarnesses(selected []string) []string {
	all := []string{"claude", "codex", "opencode", "copilot"}
	if len(selected) == 0 {
		return all
	}
	for _, harness := range selected {
		if harness == "all" {
			return all
		}
	}
	seen := make(map[string]bool, len(selected))
	result := make([]string, 0, len(selected))
	for _, harness := range selected {
		if !seen[harness] {
			seen[harness] = true
			result = append(result, harness)
		}
	}
	return result
}

func harnessRoots(harness, home string) ([]string, error) {
	switch harness {
	case "claude":
		return []string{filepath.Join(home, ".claude", "skills")}, nil
	case "codex":
		codexHome, err := envHome("CODEX_HOME", filepath.Join(home, ".codex"))
		if err != nil {
			return nil, err
		}
		return dedupe([]string{
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(codexHome, "skills"),
		}), nil
	case "opencode":
		configHome, err := envHome("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		if err != nil {
			return nil, err
		}
		return []string{filepath.Join(configHome, "opencode", "skills")}, nil
	case "copilot":
		copilotHome, err := envHome("COPILOT_HOME", filepath.Join(home, ".copilot"))
		if err != nil {
			return nil, err
		}
		return []string{filepath.Join(copilotHome, "skills")}, nil
	default:
		return nil, fmt.Errorf("unknown harness %q", harness)
	}
}

func userHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return absolute(home, "home directory")
}

func envHome(name, fallback string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		value = fallback
	}
	return absolute(value, name)
}

func absolute(path, what string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", what, err)
	}
	return filepath.Clean(path), nil
}

func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}
