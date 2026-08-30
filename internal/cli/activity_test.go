package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

func TestCommentAndActivityCommands(t *testing.T) {
	h := newHarness(t)
	id := h.create("Timeline", "--project", "awb")

	assert.Empty(t, h.mustRun("comment", "add", id, "--body", "hello **world**"))
	assert.Empty(t, h.mustRunStdin("from stdin\n", "comment", "add", id, "--body-file", "-"))

	var comments []domain.Activity
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("comment", "list", id, "--json")), &comments))
	require.Len(t, comments, 2)
	assert.Equal(t, "from stdin\n", comments[0].Body)

	reason := "verified"
	assert.Empty(t, h.mustRun("close", id, "--reason", reason))

	lines := strings.Split(strings.TrimSpace(h.mustRun("activity", id, "--compact")), "\n")
	assert.Len(t, lines, 4, "creation, two comments and the reasoned close")
	assert.Contains(t, lines[0], `comment @mikael closed "verified" [{"field":"status"`)
	assert.Contains(t, lines[1], `comment @mikael "from stdin\n"`)
}

func TestCommentRequiresExactlyOneBodySource(t *testing.T) {
	h := newHarness(t)
	id := h.create("Timeline", "--project", "awb")

	_, _, code := h.run("comment", "add", id)
	assert.Equal(t, 2, code)
	_, _, code = h.run("comment", "add", id, "--body", "x", "--body-file", "-")
	assert.Equal(t, 2, code)
}
