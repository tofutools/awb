package domain

import (
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// SortKey names an ordering for a listing.
type SortKey string

const (
	SortPriority  SortKey = "priority"
	SortCreated   SortKey = "created"
	SortUpdated   SortKey = "updated"
	SortID        SortKey = "id"
	SortProject   SortKey = "project"
	SortStatus    SortKey = "status"
	SortAssignee  SortKey = "assignee"
	SortType      SortKey = "type"
	SortBlockers  SortKey = "blockers"
	SortRelevance SortKey = "relevance"
)

// SortKeys lists the keys every listing accepts. relevance is deliberately not
// among them: it is search's alone.
var SortKeys = []SortKey{
	SortPriority, SortCreated, SortUpdated, SortID, SortProject, SortStatus,
	SortAssignee, SortType, SortBlockers,
}

// Sort is one parsed --sort value.
//
// Every sort ends with id ascending as a final tiebreak, so the order is total
// and two invocations against unchanged data agree. priority inserts
// created_at ascending before that tiebreak — oldest first within a priority —
// so --sort priority is exactly the default order; the other keys use the
// tiebreak alone. The Desc prefix reverses the named key only: the created_at
// and id tiebreaks stay ascending whatever it says.
type Sort struct {
	Key  SortKey
	Desc bool
}

// DefaultSort is the order every listing but search uses.
var DefaultSort = Sort{Key: SortPriority}

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

// ProjectSortKey names an ordering for project listings. Project ordering is
// separate from issue ordering because "active" is a derived project count.
type ProjectSortKey string

const (
	ProjectSortByKey   ProjectSortKey = "key"
	ProjectSortActive  ProjectSortKey = "active"
	ProjectSortUpdated ProjectSortKey = "updated"
)

// ProjectSort is one parsed project-list ordering.
type ProjectSort struct {
	Key  ProjectSortKey
	Desc bool
}

// DefaultProjectSort is the stable key-ascending order used by the CLI and by
// API callers that omit sort.
var DefaultProjectSort = ProjectSort{Key: ProjectSortByKey}

// ProjectSortKeys lists every key ParseProjectSort accepts.
var ProjectSortKeys = []ProjectSortKey{ProjectSortByKey, ProjectSortActive, ProjectSortUpdated}

// ProjectSortAlternatives and ProjectSortHelp are SortAlternatives and
// SortHelp for project listings, and exist for the same reason: the command
// line offers and validates against the parser's own vocabulary rather than a
// second copy of it.
func ProjectSortAlternatives() []string {
	values := make([]string, 0, 2*len(ProjectSortKeys))
	for _, key := range ProjectSortKeys {
		values = append(values, string(key), "-"+string(key))
	}
	return values
}

func ProjectSortHelp() string {
	return join(ProjectSortKeys) + `, optionally prefixed with "-"`
}

// ParseProjectSort reads a project ordering, optionally prefixed with "-".
func ParseProjectSort(s string) (ProjectSort, error) {
	desc := false
	if rest, found := strings.CutPrefix(s, "-"); found {
		desc = true
		s = rest
	}
	key := ProjectSortKey(s)
	for _, accepted := range ProjectSortKeys {
		if key == accepted {
			return ProjectSort{Key: key, Desc: desc}, nil
		}
	}
	return ProjectSort{}, awberr.Usagef("invalid project sort %q: must be one of %s",
		s, ProjectSortHelp())
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

	Projects []string
	// Parent selects the direct children of that issue — the issues whose
	// has-parent relation names it — not the whole subtree.
	Parent string

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
