package domain

import (
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// SortKey names an ordering for a listing.
type SortKey string

const (
	SortOrder     SortKey = "order"
	SortPriority  SortKey = "priority"
	SortCreated   SortKey = "created"
	SortUpdated   SortKey = "updated"
	SortID        SortKey = "id"
	SortWorkspace SortKey = "workspace"
	SortStatus    SortKey = "status"
	SortAssignee  SortKey = "assignee"
	SortType      SortKey = "type"
	SortBlockers  SortKey = "blockers"
	SortRelevance SortKey = "relevance"
)

// SortKeys lists the keys every listing accepts. relevance is deliberately not
// among them: it is search's alone.
var SortKeys = []SortKey{
	SortOrder, SortPriority, SortCreated, SortUpdated, SortID, SortWorkspace, SortStatus,
	SortAssignee, SortType, SortBlockers,
}

// Sort is one parsed --sort value.
//
// Every sort ends with id ascending as a final tiebreak, so the order is total
// and two invocations against unchanged data agree. order places sparse manual
// ranks first and falls back to priority then updated_at; priority retains its
// created_at ascending tiebreak. The Desc prefix reverses the named key only;
// the remaining tiebreaks keep their documented direction.
type Sort struct {
	Key  SortKey
	Desc bool
}

// DefaultSort puts manually ordered issues first, then falls back to priority
// and recency for issues which have not been positioned by a person.
var DefaultSort = Sort{Key: SortOrder}

// DefaultSearchSort is search's, relevance being the one key whose bare form
// is descending, because best match first is what it means.
var DefaultSearchSort = Sort{Key: SortRelevance}

// ParseSort reads a --sort value, optionally prefixed with "-" for descending
// order. allowRelevance is set only for search; --sort relevance anywhere else
// is a usage error.
func ParseSort(s string, allowRelevance bool) (Sort, error) {
	desc := false
	if rest, found := strings.CutPrefix(s, "-"); found {
		desc = true
		s = rest
	}

	key := SortKey(s)
	switch {
	case key == SortRelevance && allowRelevance:
	case key == SortRelevance:
		return Sort{}, awberr.Usagef("invalid sort %q: relevance is only available on search", s)
	case containsKey(SortKeys, key):
	default:
		return Sort{}, awberr.Usagef("invalid sort %q: must be one of %s", s, SortHelp(allowRelevance))
	}

	// relevance means best match first, so its bare form is already descending
	// and "-relevance" is worst match first. The flag is stored as written and
	// the storage layer knows which way round the function runs.
	return Sort{Key: key, Desc: desc}, nil
}

// sortKeys is every key ParseSort accepts, relevance last when it accepts it.
func sortKeys(allowRelevance bool) []SortKey {
	if allowRelevance {
		return append(append([]SortKey{}, SortKeys...), SortRelevance)
	}
	return SortKeys
}

// SortAlternatives is every value ParseSort accepts, each key ascending and
// then descending. The command line offers and validates against this rather
// than a second copy of the vocabulary, so --sort and ParseSort cannot come to
// disagree about what a listing may be ordered by.
func SortAlternatives(allowRelevance bool) []string {
	keys := sortKeys(allowRelevance)
	values := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		values = append(values, string(key), "-"+string(key))
	}
	return values
}

// SortHelp names the accepted keys, for --sort's help text and for the usage
// error a rejected value produces. Both surfaces say the same sentence.
func SortHelp(allowRelevance bool) string {
	return join(sortKeys(allowRelevance)) + `, optionally prefixed with "-"`
}

func containsKey(keys []SortKey, key SortKey) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// WorkspaceSortKey names an ordering for workspace listings. Workspace ordering is
// separate from issue ordering because "active" is a derived workspace count.
type WorkspaceSortKey string

const (
	WorkspaceSortByKey   WorkspaceSortKey = "key"
	WorkspaceSortActive  WorkspaceSortKey = "active"
	WorkspaceSortUpdated WorkspaceSortKey = "updated"
)

// WorkspaceSort is one parsed workspace-list ordering.
type WorkspaceSort struct {
	Key  WorkspaceSortKey
	Desc bool
}

// DefaultWorkspaceSort is the stable key-ascending order used by the CLI and by
// API callers that omit sort.
var DefaultWorkspaceSort = WorkspaceSort{Key: WorkspaceSortByKey}

// WorkspaceSortKeys lists every key ParseWorkspaceSort accepts.
var WorkspaceSortKeys = []WorkspaceSortKey{WorkspaceSortByKey, WorkspaceSortActive, WorkspaceSortUpdated}

// WorkspaceSortAlternatives and WorkspaceSortHelp are SortAlternatives and
// SortHelp for workspace listings, and exist for the same reason: the command
// line offers and validates against the parser's own vocabulary rather than a
// second copy of it.
func WorkspaceSortAlternatives() []string {
	values := make([]string, 0, 2*len(WorkspaceSortKeys))
	for _, key := range WorkspaceSortKeys {
		values = append(values, string(key), "-"+string(key))
	}
	return values
}

func WorkspaceSortHelp() string {
	return join(WorkspaceSortKeys) + `, optionally prefixed with "-"`
}

// ParseWorkspaceSort reads a workspace ordering, optionally prefixed with "-".
func ParseWorkspaceSort(s string) (WorkspaceSort, error) {
	desc := false
	if rest, found := strings.CutPrefix(s, "-"); found {
		desc = true
		s = rest
	}
	key := WorkspaceSortKey(s)
	for _, accepted := range WorkspaceSortKeys {
		if key == accepted {
			return WorkspaceSort{Key: key, Desc: desc}, nil
		}
	}
	return WorkspaceSort{}, awberr.Usagef("invalid workspace sort %q: must be one of %s",
		s, WorkspaceSortHelp())
}

// Readiness selects on the derived blocked state, which is what separates awb
// ready and awb blocked from awb list.
type Readiness int

const (
	// ReadinessAny does not select on blocked state, which is awb list.
	ReadinessAny Readiness = iota
	// ReadinessReady selects issues that are not blocked. Combined with the
	// status and assignee sets awb ready fixes for itself, that is the whole
	// definition of ready.
	ReadinessReady
	// ReadinessBlocked selects issues that are blocked.
	ReadinessBlocked
)

// Filter selects issues for a listing. Repeated values of one filter are ORed;
// different filters are ANDed.
type Filter struct {
	// Readiness selects on the derived blocked state; the zero value does not.
	Readiness Readiness

	// Statuses selects exactly these statuses. Empty means the default, which is
	// every status but closed unless IncludeClosed widens it.
	Statuses []Status
	// IncludeClosed widens whatever status set is in force to include closed
	// issues.
	IncludeClosed bool
	// IncludeArchived is reserved for explicit history/export paths. Ordinary
	// listings and every target picker leave it false.
	IncludeArchived bool
	// BoardOnly removes issues explicitly hidden from board projections.
	BoardOnly bool
	// ClosedAfter keeps closed issues only when their most recent close is at
	// or after this timestamp. Empty applies no age limit.
	ClosedAfter string

	Types      []Type
	Priorities []int
	// PriorityMax is inclusive and reads as urgency, not as a number: because 0
	// is the highest priority, a PriorityMax of 1 means P0 and P1.
	PriorityMax *int

	Labels []string
	// Assignees selects these assignees. Unassigned selects the empty one
	// instead; the two are mutually exclusive, which the adapters enforce.
	Assignees  []string
	Unassigned bool

	Workspaces []string
	// ExcludeIDs removes exact issue IDs. It is board-internal: default and
	// named board presentation preferences use it to omit epic lanes without
	// changing the issues or a saved view's shared filter definition.
	ExcludeIDs []string
	// Parent selects the direct children of that issue — the issues whose
	// has-parent relation names it — not the whole subtree.
	Parent string
	// Epic selects direct same-workspace membership in one epic. A non-nil empty
	// value selects issues without such a membership. It is board-internal and
	// is not exposed as a general listing filter.
	Epic *string

	// Terms are search's literal terms. Each is wrapped in double quotes before
	// it reaches FTS5, so no operator, wildcard or column prefix is passed
	// through and no input can produce a query syntax error.
	Terms []string

	// ListingFilter is the web listing's case-insensitive substring filter over
	// displayed values. Whitespace-separated words must all match. It remains
	// distinct from Terms, whose whole-token FTS semantics power search itself.
	ListingFilter string

	// Limit caps the results; nil means no cap, and zero returns none. Offset
	// skips rows; it has no CLI flag and exists for the API's paging.
	Limit  *int
	Offset *int

	Sort Sort
}

// EffectiveStatuses is the status set the filter selects, resolving the
// default and IncludeClosed.
func (f *Filter) EffectiveStatuses() []Status {
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = NotClosedStatuses
	}
	if !f.IncludeClosed {
		return statuses
	}
	if containsStatus(statuses, StatusClosed) {
		return statuses
	}
	widened := make([]Status, 0, len(statuses)+1)
	widened = append(widened, statuses...)
	return append(widened, StatusClosed)
}

func containsStatus(statuses []Status, want Status) bool {
	for _, s := range statuses {
		if s == want {
			return true
		}
	}
	return false
}
