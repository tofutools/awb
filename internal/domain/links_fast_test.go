package domain

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The fast-path predicate must be a superset: it may parse prose that has no
// links, but it must never skip Markdown from which the pinned parser extracts
// one. Generated Markdown-shaped input exercises the predicate against the
// unconditional parser rather than duplicating its rules in the test.
func TestExtractLinksFastPathMatchesParser(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 []()<@:/._-*`\\\n"
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200_000 {
		var description strings.Builder
		for range rng.IntN(100) {
			description.WriteByte(alphabet[rng.IntN(len(alphabet))])
		}
		source := description.String()
		assert.Equal(t, extractLinks(source), ExtractLinks(source), source)
	}
}
