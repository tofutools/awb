package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tofutools/awb/internal/domain"
)

// The canonical compact line, for the issue the JSON shape documents.
func TestCompactLineSpecExample(t *testing.T) {
	issue := &domain.Issue{
		ID:       "awb-5c1d84",
		Title:    "Tokeniser drops the trailing newline",
		Type:     domain.TypeBug,
		Status:   domain.StatusInProgress,
		Priority: 1,
		Labels:   []string{"tokeniser"},
		Assignee: "claude-1",
		Blocked:  true,
		Blockers: []string{"awb-9b2f60"},
	}

	assert.Equal(t,
		`awb-5c1d84 P1 in_progress bug "Tokeniser drops the trailing newline" @claude-1 #tokeniser !blocked`,
		domain.CompactLine(issue, false))
}

func TestCompactLineOptionalFieldsAndOrder(t *testing.T) {
	base := domain.Issue{
		ID: "awb-a1", Title: "T", Type: domain.TypeTask, Status: domain.StatusOpen, Priority: 2,
	}

	t.Run("bare issue is five fields", func(t *testing.T) {
		assert.Equal(t, `awb-a1 P2 open task "T"`, domain.CompactLine(&base, false))
	})

	t.Run("labels come out in the order given, which callers sort", func(t *testing.T) {
		i := base
		i.Labels = []string{"a", "b", "c"}
		assert.Equal(t, `awb-a1 P2 open task "T" #a #b #c`, domain.CompactLine(&i, false))
	})

	t.Run("fixed order: assignee, labels, blocked, blockers", func(t *testing.T) {
		i := base
		i.Assignee = "claude-1"
		i.Labels = []string{"x", "y"}
		i.Blocked = true
		i.Blockers = []string{"awb-b1", "awb-b2"}

		assert.Equal(t,
			`awb-a1 P2 open task "T" @claude-1 #x #y !blocked`,
			domain.CompactLine(&i, false))
		assert.Equal(t,
			`awb-a1 P2 open task "T" @claude-1 #x #y !blocked blocked-by:awb-b1 blocked-by:awb-b2`,
			domain.CompactLine(&i, true))
	})
}

// The title is the only field that may contain literal spaces after decoding,
// and it is encoded as a JSON string so a line stays parseable by splitting on
// whitespace outside it.
func TestCompactLineTitleIsAJSONString(t *testing.T) {
	cases := map[string]string{
		`plain`:            `"plain"`,
		`with "quotes"`:    `"with \"quotes\""`,
		`back\slash`:       `"back\\slash"`,
		`tab	inside`:       `"tab\tinside"`,
		`<html> & things`:  `"<html> & things"`,
		`unicode: naïve 日`: `"unicode: naïve 日"`,
	}
	for title, want := range cases {
		i := domain.Issue{ID: "awb-a1", Title: title, Type: domain.TypeTask, Status: domain.StatusOpen}
		line := domain.CompactLine(&i, false)
		assert.True(t, strings.HasSuffix(line, want), "title %q gave %q, want suffix %q", title, line, want)
	}
}

func TestCompactProjectLine(t *testing.T) {
	assert.Equal(t, `awb 3 "Agent Work Board"`,
		domain.CompactProjectLine(&domain.Project{Key: "awb", Name: "Agent Work Board", ActiveIssues: 3}))
	assert.Equal(t, `web 0 "web"`,
		domain.CompactProjectLine(&domain.Project{Key: "web", Name: "web"}))
}

func TestCompactTreePrefix(t *testing.T) {
	assert.Equal(t, "", domain.CompactTreePrefix(0), "the root is unindented")
	assert.Equal(t, "  ", domain.CompactTreePrefix(1))
	assert.Equal(t, "      ", domain.CompactTreePrefix(3))
}
