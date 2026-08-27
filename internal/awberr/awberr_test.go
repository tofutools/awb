package awberr_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tofutools/awb/internal/awberr"
)

// The whole mapping in one table, so neither surface can drift from it.
func TestKindMapping(t *testing.T) {
	cases := []struct {
		kind   awberr.Kind
		exit   int
		status int
	}{
		{awberr.Runtime, 1, http.StatusInternalServerError},
		{awberr.Usage, 2, http.StatusBadRequest},
		{awberr.NotFound, 3, http.StatusNotFound},
		{awberr.Conflict, 4, http.StatusConflict},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.exit, tc.kind.ExitCode())
		assert.Equal(t, tc.status, tc.kind.HTTPStatus())
		assert.Equal(t, tc.kind, awberr.KindFromHTTPStatus(tc.status))
	}
}

// The six statuses with no exit code behind them all fold into 1, which is
// what the CLI reports in remote mode.
func TestKindFromHTTPStatusFoldsTheRest(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed,
		http.StatusPreconditionFailed, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusBadGateway, http.StatusOK,
	} {
		assert.Equal(t, awberr.Runtime, awberr.KindFromHTTPStatus(status), status)
		assert.Equal(t, 1, awberr.KindFromHTTPStatus(status).ExitCode(), status)
	}
}

func TestZeroKindIsRuntime(t *testing.T) {
	var k awberr.Kind
	assert.Equal(t, awberr.Runtime, k)
}

func TestConstructors(t *testing.T) {
	assert.Equal(t, awberr.Usage, awberr.KindOf(awberr.Usagef("bad %s", "flag")))
	assert.Equal(t, awberr.NotFound, awberr.KindOf(awberr.NotFoundf("no %s", "issue")))
	assert.Equal(t, awberr.Conflict, awberr.KindOf(awberr.Conflictf("cycle")))
	assert.Equal(t, awberr.Runtime, awberr.KindOf(awberr.Runtimef("boom")))
	assert.Equal(t, "bad flag", awberr.Usagef("bad %s", "flag").Error())
}

// An unclassified failure is a runtime one, never a usage one: blaming the
// caller for a bug would be the worse mistake.
func TestKindOfUnclassified(t *testing.T) {
	assert.Equal(t, awberr.Runtime, awberr.KindOf(errors.New("plain")))
	assert.Equal(t, 1, awberr.ExitCode(errors.New("plain")))
}

func TestExitCodeOfNil(t *testing.T) {
	assert.Equal(t, 0, awberr.ExitCode(nil))
}

func TestWrapKeepsTheChain(t *testing.T) {
	sentinel := errors.New("underlying")
	err := awberr.Wrap(awberr.Conflict, sentinel, "adding %s", "edge")

	assert.Equal(t, awberr.Conflict, awberr.KindOf(err))
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, "adding edge: underlying", err.Error())

	assert.NoError(t, awberr.Wrap(awberr.Usage, nil, "ignored"))
}

// A classified error found anywhere in the chain decides the kind.
func TestKindOfNestedError(t *testing.T) {
	err := fmt.Errorf("outer: %w", awberr.NotFoundf("inner"))
	assert.Equal(t, awberr.NotFound, awberr.KindOf(err))
}
