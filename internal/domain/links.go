package domain

import (
	"bytes"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// markdown is the pinned parser: CommonMark plus GFM's tables, task lists,
// strikethrough, autolink extension and disallowed-raw-HTML rule, and nothing
// beyond that. extension.GFM is exactly that set.
//
// The dialect is pinned because links is a specified output: the same
// description must always yield the same array, whoever parses it. The web UI
// configures its own renderer to the same set, and a divergence there is a bug
// in one of the two rather than a choice either is free to make.
var markdown = sync.OnceValue(func() goldmark.Markdown {
	return goldmark.New(goldmark.WithExtensions(extension.GFM))
})

// ExtractLinks returns the Markdown links in a description, in the order they
// first occur.
//
// It takes inline links, reference links and autolinks of both kinds —
// CommonMark's <https://example.com/1> and GFM's bare https://example.com/1 —
// and ignores images. Each distinct destination appears once, with the link
// text of its first occurrence. Two destinations are distinct when they differ
// byte for byte; no normalisation, resolution or validation is applied, so a
// relative destination such as ./notes.md is extracted as written.
//
// The text is the link's rendered plain text, with inline markup removed and
// whitespace collapsed, so [**CI** run](u) yields "CI run". An image inside a
// link contributes nothing, images being ignored, so [![alt](i.png)](u) is
// extracted with an empty text. Raw HTML is not a source of links either: an
// <a href=...> written out by hand is raw HTML to the parser and not a
// Markdown link.
func ExtractLinks(description string) []Link {
	if description == "" {
		return []Link{}
	}

	source := []byte(description)
	doc := markdown().Parser().Parse(text.NewReader(source))

	links := []Link{}
	seen := make(map[string]struct{})
	add := func(url, linkText string) {
		if _, dup := seen[url]; dup {
			return
		}
		seen[url] = struct{}{}
		links = append(links, Link{Text: linkText, URL: url})
	}

	// ast.Walk is a preorder depth-first traversal, so links arrive in the order
	// they occur in the source.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Image:
			// Images are ignored, alt text included, so nothing under one contributes a
			// link or a character of text.
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			add(string(v.Destination), plainText(v, source))
		case *ast.AutoLink:
			add(autoLinkURL(v, source), string(v.Label(source)))
		}
		return ast.WalkContinue, nil
	})

	return links
}

// autoLinkURL is the destination the GFM autolink extension yields, including
// the two prefixes worth calling out: the http:// goldmark puts in front of a
// www. host, and the mailto: in front of an email address. Those prefixes come
// from the parser and are the one case where a destination is not a substring
// of the description.
//
// goldmark supplies the first through AutoLink.URL but adds the second only at
// render time, so it is added here to keep both surfaces agreeing on what the
// extension yields.
func autoLinkURL(n *ast.AutoLink, source []byte) string {
	url := n.URL(source)
	if n.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(url), []byte("mailto:")) {
		return "mailto:" + string(url)
	}
	return string(url)
}

// plainText renders a link's label as text: inline markup removed, images
// skipped, whitespace collapsed to single spaces and trimmed.
func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			switch v := child.(type) {
			case *ast.Image:
				// contributes nothing, not even its alt text
				continue
			case *ast.Text:
				b.Write(v.Segment.Value(source))
				if v.SoftLineBreak() || v.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(v.Value)
			case *ast.AutoLink:
				b.Write(v.Label(source))
			case *ast.RawHTML:
				// raw HTML is markup, not text
				continue
			default:
				walk(child)
			}
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
