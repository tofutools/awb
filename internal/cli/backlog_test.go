package cli_test

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestBacklogCLI(t *testing.T) {
	h := newHarness(t)
	t.Setenv("AWB_WORKSPACE", "awb")
	epic := h.create("Future", "--type", "epic", "--backlog")
	child := h.create("Child", "--has-parent", epic)
	assert.Contains(t, h.mustRun("list", "--status", "backlog", "--compact"), epic)
	assert.NotContains(t, h.mustRun("ready", "--compact"), child)
	h.mustRun("move", epic, "--status", "open")
	assert.Contains(t, h.mustRun("ready", "--compact"), child)
	h.mustRun("move", epic, "--status", "backlog")
	assert.NotContains(t, h.mustRun("ready", "--compact"), child)
	_, _, code := h.run("create", "Ambiguous", "--backlog", "--assignee", "alice")
	assert.NotEqual(t, 0, code)
}
