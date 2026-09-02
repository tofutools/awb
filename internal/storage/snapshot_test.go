package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// A restore is a faithful copy of what another database already holds, so it
// is the one write path that does not apply domain's prose gate. A source
// predating the gate is still dumpable, and what comes back out is byte for
// byte what went in. The gate is on the operations: a restored database is as
// trustworthy as its source and no more.
func TestRestoreSnapshotIsFaithfulToProseTheGateWouldRefuse(t *testing.T) {
	const legacy = "<script>alert(1)</script> and [a](javascript:alert(1))"
	require.Error(t, domain.ValidateMarkdown("description", legacy),
		"the gate refuses this, which is what makes it worth restoring here")

	db := newDB(t)
	require.NoError(t, db.RestoreSnapshot(t.Context(), storage.Snapshot{
		Workspaces: []domain.Workspace{{Key: "awb", Name: "Agent Work Board", Description: legacy}},
		Issues: []domain.Issue{{
			ID: "awb-5c1d84", Workspace: "awb", Title: "Legacy", Description: legacy,
			Type: domain.DefaultType, Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
		}},
		Activity: []domain.Activity{{
			ID: 1, Issue: "awb-5c1d84", Kind: domain.ActivityKindComment, Actor: "mikael", Body: legacy,
			Changes: []domain.ActivityChange{},
		}},
	}))

	require.NoError(t, db.Read(t.Context(), func(tx *storage.Tx) error {
		workspace, err := tx.GetWorkspace("awb")
		require.NoError(t, err)
		assert.Equal(t, legacy, workspace.Description)

		issue, err := tx.GetIssue("awb-5c1d84")
		require.NoError(t, err)
		assert.Equal(t, legacy, issue.Description)

		entries, _, err := tx.ListActivity("awb-5c1d84", "", nil, nil)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, legacy, entries[0].Body)
		return nil
	}))
}
