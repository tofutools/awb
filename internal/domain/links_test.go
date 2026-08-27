package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tofutools/awb/internal/domain"
)

func TestExtractLinks(t *testing.T) {
	cases := []struct {
		name string
		desc string
		want []domain.Link
	}{
		{
			name: "empty description",
			desc: "",
			want: []domain.Link{},
		},
		{
			name: "no links",
			desc: "Just prose, with *emphasis* and `code`.",
			want: []domain.Link{},
		},
		{
			name: "inline link",
			desc: "Reproduced on Linux. See [CI run](https://ci.example.com/1).",
			want: []domain.Link{{Text: "CI run", URL: "https://ci.example.com/1"}},
		},
		{
			name: "inline markup is stripped from the text",
			desc: "[**CI** run](https://ci.example.com/1)",
			want: []domain.Link{{Text: "CI run", URL: "https://ci.example.com/1"}},
		},
		{
			name: "whitespace in the text is collapsed",
			desc: "[CI\n   run](https://ci.example.com/1)",
			want: []domain.Link{{Text: "CI run", URL: "https://ci.example.com/1"}},
		},
		{
			name: "reference link",
			desc: "See [the run][r].\n\n[r]: https://ci.example.com/1\n",
			want: []domain.Link{{Text: "the run", URL: "https://ci.example.com/1"}},
		},
		{
			name: "CommonMark autolink: text is the destination",
			desc: "See <https://ci.example.com/1>.",
			want: []domain.Link{{Text: "https://ci.example.com/1", URL: "https://ci.example.com/1"}},
		},
		{
			name: "GFM bare autolink",
			desc: "See https://ci.example.com/1 for details.",
			want: []domain.Link{{Text: "https://ci.example.com/1", URL: "https://ci.example.com/1"}},
		},
		{
			name: "GFM www autolink gets an http:// the source did not have",
			desc: "See www.example.com for details.",
			want: []domain.Link{{Text: "www.example.com", URL: "http://www.example.com"}},
		},
		{
			name: "GFM email autolink gets a mailto:",
			desc: "Ask dev@example.com about it.",
			want: []domain.Link{{Text: "dev@example.com", URL: "mailto:dev@example.com"}},
		},
		{
			name: "images are ignored",
			desc: "![alt](https://example.com/i.png)",
			want: []domain.Link{},
		},
		{
			name: "an image inside a link contributes no text",
			desc: "[![alt](i.png)](https://ci.example.com/1)",
			want: []domain.Link{{Text: "", URL: "https://ci.example.com/1"}},
		},
		{
			name: "raw HTML anchors are not links",
			desc: `<a href="https://ci.example.com/1">raw</a>`,
			want: []domain.Link{},
		},
		{
			name: "relative destinations are extracted as written",
			desc: "[notes](./notes.md)",
			want: []domain.Link{{Text: "notes", URL: "./notes.md"}},
		},
		{
			name: "each distinct destination appears once, with the first text",
			desc: "[first](https://ci.example.com/1) then [second](https://ci.example.com/1)",
			want: []domain.Link{{Text: "first", URL: "https://ci.example.com/1"}},
		},
		{
			name: "destinations differing byte for byte are distinct",
			desc: "[a](https://example.com/x) [b](https://example.com/X)",
			want: []domain.Link{
				{Text: "a", URL: "https://example.com/x"},
				{Text: "b", URL: "https://example.com/X"},
			},
		},
		{
			name: "order is first occurrence",
			desc: "[c](https://example.com/3) [a](https://example.com/1) [b](https://example.com/2)",
			want: []domain.Link{
				{Text: "c", URL: "https://example.com/3"},
				{Text: "a", URL: "https://example.com/1"},
				{Text: "b", URL: "https://example.com/2"},
			},
		},
		{
			name: "links inside a table are extracted",
			desc: "| a | b |\n| - | - |\n| [x](https://example.com/1) | y |\n",
			want: []domain.Link{{Text: "x", URL: "https://example.com/1"}},
		},
		{
			name: "links inside a task list are extracted",
			desc: "- [ ] do [x](https://example.com/1)\n",
			want: []domain.Link{{Text: "x", URL: "https://example.com/1"}},
		},
		{
			name: "a link in a code span is not a link",
			desc: "`[x](https://example.com/1)`",
			want: []domain.Link{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, domain.ExtractLinks(tc.desc))
		})
	}
}

// A GFM autolink needs a domain with a period, so a hostname like "ci" is not
// autolinked. That is the extension's own algorithm, which SPEC §2.4 defers to.
func TestExtractLinksBareHostWithoutPeriodIsNotAutolinked(t *testing.T) {
	assert.Equal(t, []domain.Link{}, domain.ExtractLinks("see https://ci/1 for details"))
	assert.Equal(t,
		[]domain.Link{{Text: "https://ci/1", URL: "https://ci/1"}},
		domain.ExtractLinks("see <https://ci/1> for details"),
		"an explicit CommonMark autolink needs no valid domain")
}

// Trailing punctuation is trimmed by the extension, not by awb.
func TestExtractLinksTrailingPunctuation(t *testing.T) {
	assert.Equal(t,
		[]domain.Link{{Text: "https://example.com/1", URL: "https://example.com/1"}},
		domain.ExtractLinks("see https://example.com/1."))
}

func TestExtractLinksIsDeterministic(t *testing.T) {
	desc := "[a](https://example.com/1) ![i](p.png) <https://example.com/2> www.example.com"
	first := domain.ExtractLinks(desc)
	for range 5 {
		assert.Equal(t, first, domain.ExtractLinks(desc))
	}
}

func TestExtractLinksLargeDescription(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		b.WriteString("para ")
		b.WriteString(strings.Repeat("x", 50))
		b.WriteString(" [l](https://example.com/")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(")\n\n")
	}
	assert.Len(t, domain.ExtractLinks(b.String()), 26)
}
