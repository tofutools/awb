package domain

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The fast-path predicate must be a superset: it may parse prose that has no
// links, but it must never skip Markdown from which the pinned parser extracts
// one. Generated surrounding text exercises every predicate marker against the
// unconditional parser, which remains the oracle for whether each case links.
func TestExtractLinksFastPathMatchesParser(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-*`\\\n"
	markers := []string{
		"plain prose",
		"[label](destination)",
		"<https://example.com/path>",
		"name@example.com",
		"https://example.com/path",
		"www.example.com/path",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 5_000 {
		var description strings.Builder
		for range rng.IntN(50) {
			description.WriteByte(alphabet[rng.IntN(len(alphabet))])
		}
		description.WriteString(markers[i%len(markers)])
		for range rng.IntN(50) {
			description.WriteByte(alphabet[rng.IntN(len(alphabet))])
		}
		source := description.String()
		assert.Equal(t, extractLinks(source), ExtractLinks(source), source)
	}
}
