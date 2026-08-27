package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// Everything the text gate rejects is a usage error: exit 2, HTTP 400.
func assertUsage(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	require.Error(t, err, msgAndArgs...)
	assert.Equal(t, awberr.Usage, awberr.KindOf(err), msgAndArgs...)
}

func TestValidateTitle(t *testing.T) {
	t.Run("trims", func(t *testing.T) {
		got, err := domain.ValidateTitle("  Parser crashes  ")
		require.NoError(t, err)
		assert.Equal(t, "Parser crashes", got)
	})

	t.Run("keeps inner content byte for byte", func(t *testing.T) {
		// No normalisation, and a byte-order mark is ordinary content rather than a
		// prefix to remove.
		const withBOM = "a  b c \ufeff"
		got, err := domain.ValidateTitle(withBOM)
		require.NoError(t, err)
		assert.Equal(t, withBOM, got)
	})

	t.Run("rejects", func(t *testing.T) {
		for _, s := range []string{
			"", "   ", "\t\n ",
			"line\nbreak", "carriage\rreturn",
			"nul\x00byte", "bell\x07", "del\u007f", "c1\u0080",
			strings.Repeat("x", domain.MaxTitleLen+1),
			"bad\xff\xfeutf8",
		} {
			_, err := domain.ValidateTitle(s)
			assertUsage(t, err, "%q", s)
		}
	})

	t.Run("length is counted in code points after trimming", func(t *testing.T) {
		_, err := domain.ValidateTitle("  " + strings.Repeat("é", domain.MaxTitleLen) + "  ")
		require.NoError(t, err, "500 two-byte runes is 500 characters, not 1000")

		_, err = domain.ValidateTitle(strings.Repeat("é", domain.MaxTitleLen+1))
		assertUsage(t, err)
	})
}

func TestValidateCloseReason(t *testing.T) {
	got, err := domain.ValidateCloseReason("  done  ")
	require.NoError(t, err)
	assert.Equal(t, "done", got)

	got, err = domain.ValidateCloseReason("   ")
	require.NoError(t, err)
	assert.Equal(t, "", got, "empty after trimming clears it, unlike a title")

	_, err = domain.ValidateCloseReason("line\nbreak")
	assertUsage(t, err)
}

func TestValidateDescription(t *testing.T) {
	t.Run("is never trimmed", func(t *testing.T) {
		got, err := domain.ValidateDescription("  text\n\n")
		require.NoError(t, err)
		assert.Equal(t, "  text\n\n", got, "a trailing line feed is part of the description")
	})

	t.Run("allows tab, LF and CR", func(t *testing.T) {
		_, err := domain.ValidateDescription("a\tb\nc\r\nd")
		require.NoError(t, err)
	})

	t.Run("rejects other controls", func(t *testing.T) {
		for _, s := range []string{"nul\x00", "bell\x07", "del\u007f", "c1\u0080", "bad\xffutf8"} {
			_, err := domain.ValidateDescription(s)
			assertUsage(t, err, "%q", s)
		}
	})

	t.Run("is bounded in bytes, not characters", func(t *testing.T) {
		_, err := domain.ValidateDescription(strings.Repeat("x", domain.MaxDescriptionBytes))
		require.NoError(t, err)

		_, err = domain.ValidateDescription(strings.Repeat("x", domain.MaxDescriptionBytes+1))
		assertUsage(t, err)

		// Two-byte runes hit the byte cap at half the character count.
		_, err = domain.ValidateDescription(strings.Repeat("é", domain.MaxDescriptionBytes/2+1))
		assertUsage(t, err)
	})
}

func TestValidateProjectKey(t *testing.T) {
	for _, s := range []string{"awb", "a", "web-ui", "x1", "a-1-b"} {
		got, err := domain.ValidateProjectKey(s)
		require.NoError(t, err, s)
		assert.Equal(t, s, got)
	}
	for _, s := range []string{
		"", "1abc", "-abc", "Awb", "a_b", "a.b", "a/b", "a b", "å",
		strings.Repeat("a", domain.MaxProjectKeyLen+1),
	} {
		_, err := domain.ValidateProjectKey(s)
		assertUsage(t, err, "%q", s)
	}
}

func TestValidateLabelAndAssignee(t *testing.T) {
	for _, s := range []string{"parser", "a", "front-end", "a_b", "a.b", "team/web", "x1", "1a"} {
		_, err := domain.ValidateLabel(s)
		require.NoError(t, err, s)
		_, err = domain.ValidateAssignee(s)
		require.NoError(t, err, s)
	}
	for _, s := range []string{
		"", "Parser", "a b", "a+b", "å", "claude 1",
		strings.Repeat("a", domain.MaxLabelLen+1),
	} {
		_, err := domain.ValidateLabel(s)
		assertUsage(t, err, "%q", s)
		_, err = domain.ValidateAssignee(s)
		assertUsage(t, err, "%q", s)
	}
}

// The design spells this one out: claim --as Mikael is a usage error, not a
// value to fold.
func TestValidateAssigneeRefusesRatherThanNormalises(t *testing.T) {
	_, err := domain.ValidateAssignee("Mikael")
	assertUsage(t, err)
}

func TestValidateProjectName(t *testing.T) {
	got, err := domain.ValidateProjectName("")
	require.NoError(t, err)
	assert.Equal(t, "", got, "empty means restore the key, so it is accepted here")

	_, err = domain.ValidateProjectName("line\nbreak")
	assertUsage(t, err)

	_, err = domain.ValidateProjectName(strings.Repeat("x", domain.MaxProjectNameLen+1))
	assertUsage(t, err)
}

func TestValidateSearchTerm(t *testing.T) {
	for _, s := range []string{"parser", "hello world", "3", "naïve", "日本語"} {
		_, err := domain.ValidateSearchTerm(s)
		require.NoError(t, err, s)
	}
	// A term the tokenizer reduces to nothing is a usage error rather than a
	// silently widened search.
	for _, s := range []string{"", "-", ",", "  ", "!!!", "--"} {
		_, err := domain.ValidateSearchTerm(s)
		assertUsage(t, err, "%q", s)
	}
}

func TestFoldToAssignee(t *testing.T) {
	cases := map[string]string{
		"Mikael":       "mikael",
		"DOMAIN\\user": "domainuser",
		"claude-1":     "claude-1",
		"A B":          "ab",
		"!!!":          "",
		"":             "",
		"Ståldal":      "stldal",
	}
	for in, want := range cases {
		assert.Equal(t, want, domain.FoldToAssignee(in), in)
	}

	folded := domain.FoldToAssignee(strings.Repeat("a", domain.MaxAssigneeLen+10))
	assert.Len(t, folded, domain.MaxAssigneeLen, "folding also respects the maximum")
	_, err := domain.ValidateAssignee(folded)
	assert.NoError(t, err, "whatever folding yields must itself be a valid assignee")
}

// A project name is not trimmed: only a title and a close reason are, and
// everything else is stored byte for byte as it arrived. Regression: it used to
// be trimmed, which silently altered a name the caller meant and turned a
// whitespace-only name into an empty one.
func TestValidateProjectNameIsNotTrimmed(t *testing.T) {
	for _, name := range []string{"  Display name  ", " leading", "trailing ", "   "} {
		got, err := domain.ValidateProjectName(name)
		require.NoError(t, err, "%q", name)
		assert.Equal(t, name, got, "%q must be stored as it arrived", name)
	}

	// Only the genuinely empty name is empty, and that is what restores the key.
	got, err := domain.ValidateProjectName("")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// The maximum is counted over the value as stored, which for a name is the
// untrimmed one.
func TestValidateProjectNameLengthCountsUntrimmed(t *testing.T) {
	_, err := domain.ValidateProjectName(strings.Repeat("x", domain.MaxProjectNameLen))
	require.NoError(t, err)

	_, err = domain.ValidateProjectName(" " + strings.Repeat("x", domain.MaxProjectNameLen))
	assertUsage(t, err, "the leading space counts")
}
