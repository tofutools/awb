// Package domain holds awb's rules: the fixed vocabulary, the text gate, the
// identifier scheme, link extraction, the relation graph and readiness. It
// reads and writes nothing, so the command line and the HTTP API can share it
// wholesale and cannot drift apart.
package domain

import (
	"slices"
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// WorkspaceState distinguishes current work from retained read-only history.
// Archiving is deliberately the only inactive state: a second locked state
// would carry the same write rules while making restoration ambiguous.
type WorkspaceState string

const (
	WorkspaceActive   WorkspaceState = "active"
	WorkspaceArchived WorkspaceState = "archived"
)

var WorkspaceStates = []WorkspaceState{WorkspaceActive, WorkspaceArchived}

func ParseWorkspaceState(s string) (WorkspaceState, error) {
	if slices.Contains(WorkspaceStates, WorkspaceState(s)) {
		return WorkspaceState(s), nil
	}
	return "", awberr.Usagef("invalid workspace state %q: must be one of %s", s, join(WorkspaceStates))
}

type WorkspaceStateFilter string

const (
	WorkspacesActive   WorkspaceStateFilter = "active"
	WorkspacesArchived WorkspaceStateFilter = "archived"
	WorkspacesAll      WorkspaceStateFilter = "all"
)

func ParseWorkspaceStateFilter(s string) (WorkspaceStateFilter, error) {
	filter := WorkspaceStateFilter(s)
	if filter == WorkspacesActive || filter == WorkspacesArchived || filter == WorkspacesAll {
		return filter, nil
	}
	return "", awberr.Usagef("invalid workspace state filter %q: must be active, archived or all", s)
}

// Type is an issue's kind. Only epic carries behaviour, and only on the board:
// an epic is a lane there rather than a card in a status column, and the epic an
// issue belongs to is its has-parent whose parent is an epic in the same
// workspace. The other four are interchangeable; the only thing that separates
// them is that task is the default.
type Type string

const (
	TypeEpic    Type = "epic"
	TypeFeature Type = "feature"
	TypeBug     Type = "bug"
	TypeTask    Type = "task"
	TypeChore   Type = "chore"

	// DefaultType is what an issue created without a type gets.
	DefaultType = TypeTask
)

// Types lists every issue type, in their canonical order.
var Types = []Type{TypeEpic, TypeFeature, TypeBug, TypeTask, TypeChore}

// ParseType validates s as an issue type.
func ParseType(s string) (Type, error) {
	if slices.Contains(Types, Type(s)) {
		return Type(s), nil
	}
	return "", awberr.Usagef("invalid type %q: must be one of %s", s, join(Types))
}

// Status is where an issue stands. It is changed only by explicit workflow transitions,
// never by update, which is what keeps it from drifting away from the
// assignee.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusClosed     Status = "closed"

	// DefaultStatus is what an issue created without an assignee gets.
	DefaultStatus = StatusOpen
)

// Statuses lists every status, in their canonical order.
var Statuses = []Status{StatusBacklog, StatusOpen, StatusInProgress, StatusClosed}

// ParseStatus validates s as a status. There is deliberately no magic "all"
// value: the vocabulary here is exactly the enum the OpenAPI document
// declares.
func ParseStatus(s string) (Status, error) {
	if slices.Contains(Statuses, Status(s)) {
		return Status(s), nil
	}
	return "", awberr.Usagef("invalid status %q: must be one of %s", s, join(Statuses))
}

// NotClosedStatuses are the statuses an issue that is still live can hold.
// It is the status set awb blocked fixes for itself, and the one every listing
// falls back to when closed issues are hidden.
var NotClosedStatuses = []Status{StatusBacklog, StatusOpen, StatusInProgress}

// Priority ranges from 0, the highest, to 4, the lowest.
const (
	MinPriority     = 0
	MaxPriority     = 4
	DefaultPriority = 2
)

// ParsePriority validates p as a priority.
func ParsePriority(p int) (int, error) {
	if p < MinPriority || p > MaxPriority {
		return 0, awberr.Usagef("invalid priority %d: must be %d (highest) to %d (lowest)",
			p, MinPriority, MaxPriority)
	}
	return p, nil
}

// RelationType names a directed link between two issues. Every one of them is
// named from the point of view of its subject and reads "subject — type —
// other", which is the single convention the whole tool uses.
type RelationType string

const (
	// RelBlockedBy: "A blocked-by B" — A cannot start until B is closed. The only
	// relation that drives readiness.
	RelBlockedBy RelationType = "blocked-by"
	// RelHasParent: "A has-parent B" — B is the parent of A. Decomposition only;
	// it does not drive readiness.
	RelHasParent RelationType = "has-parent"
	// RelDiscoveredFrom: "A discovered-from B" — A was found while working on B.
	// Provenance only.
	RelDiscoveredFrom RelationType = "discovered-from"
	// RelRelated: "A related B" — a loose, symmetric association with no
	// behaviour attached.
	RelRelated RelationType = "related"
)

// RelationTypes lists every relation type, in their canonical order.
var RelationTypes = []RelationType{RelBlockedBy, RelHasParent, RelDiscoveredFrom, RelRelated}

// ParseRelationType validates s as a relation type.
func ParseRelationType(s string) (RelationType, error) {
	if slices.Contains(RelationTypes, RelationType(s)) {
		return RelationType(s), nil
	}
	return "", awberr.Usagef("invalid relation type %q: must be one of %s", s, join(RelationTypes))
}

// Acyclic reports whether this relation's graph must remain free of cycles.
// The three directed types are each checked separately: work cannot depend on
// itself, decomposition cannot nest inside itself, and an issue cannot be its
// own origin. Only related is unconstrained, having no direction to run in a
// circle.
func (t RelationType) Acyclic() bool { return t != RelRelated }

// Symmetric reports whether this relation reads the same in both directions,
// which only related does. A symmetric relation is stored once, canonically,
// so adding it from either end is the same edge.
func (t RelationType) Symmetric() bool { return t == RelRelated }

// Direction says which end of a stored relation an issue is: out when it is
// the subject — the one named first — and in when the other issue is. A
// symmetric relation is always out.
type Direction string

const (
	DirectionOut Direction = "out"
	DirectionIn  Direction = "in"
)

func join[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}
