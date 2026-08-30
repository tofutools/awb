package domain

import "encoding/json"

// ActivityKind distinguishes prose a person posted from a structured record
// written alongside a mutation.
type ActivityKind string

const (
	ActivityKindComment ActivityKind = "comment"
	ActivityKindChange  ActivityKind = "change"
)

// ActivityChange is one field-level difference recorded by a system event.
// From and To are JSON values kept as text, so scalar and collection fields
// share one stable wire shape without losing their types.
type ActivityChange struct {
	Field string          `json:"field"`
	From  json.RawMessage `json:"from"`
	To    json.RawMessage `json:"to"`
}

// Activity is one append-only entry in an issue's timeline. Comment entries
// carry Body; change entries carry Action and zero or more Changes.
type Activity struct {
	ID        int64            `json:"id"`
	Issue     string           `json:"issue"`
	Kind      ActivityKind     `json:"kind"`
	Actor     string           `json:"actor"`
	Body      string           `json:"body"`
	Action    string           `json:"action"`
	Changes   []ActivityChange `json:"changes"`
	CreatedAt string           `json:"created_at"`
}

// Normalize keeps the stable JSON promise: an event without field changes
// carries [] rather than null.
func (a *Activity) Normalize() {
	if a.Changes == nil {
		a.Changes = []ActivityChange{}
	}
}
