package storage_test

import (
	"strings"
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

// Metadata is the one thing a restore does normalize, because the canonical
// form is what the column holds rather than a rule about what a caller may
// ask for. An object restored spelled another way is stored in the form every
// other write uses, so a later update that re-sends it still changes nothing.
//
// The size bound is a rule and stays off here, exactly as the prose gate does:
// an object too large to write today is still a faithful copy of one some
// other database holds.
func TestRestoreSnapshotStoresMetadataCanonically(t *testing.T) {
	oversize := domain.Metadata{"big": []byte(
		`"` + strings.Repeat("x", domain.MaxMetadataBytes) + `"`)}
	_, err := domain.ValidateMetadata(oversize)
	require.Error(t, err, "the bound refuses this, which is what makes it worth restoring here")

	db := newDB(t)
	require.NoError(t, db.RestoreSnapshot(t.Context(), storage.Snapshot{
		Workspaces: []domain.Workspace{{Key: "awb", Name: "Agent Work Board"}},
		Issues: []domain.Issue{{
			ID: "awb-5c1d84", Workspace: "awb", Title: "Spelled otherwise",
			Type: domain.DefaultType, Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
			Metadata: domain.Metadata{"nested": []byte(`{ "b" : 2, "a" : 1 }`)},
		}, {
			ID: "awb-5c1d85", Workspace: "awb", Title: "Too large to write",
			Type: domain.DefaultType, Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
			Metadata: oversize,
		}},
	}))

	require.NoError(t, db.Read(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue("awb-5c1d84")
		require.NoError(t, err)
		encoded, err := domain.EncodeMetadata(issue.Metadata)
		require.NoError(t, err)
		assert.Equal(t, `{"nested":{"a":1,"b":2}}`, encoded)

		large, err := tx.GetIssue("awb-5c1d85")
		require.NoError(t, err)
		assert.Len(t, large.Metadata, 1, "a faithful copy, bound or no bound")
		return nil
	}))
}
