package config

import (
	"context"
	"strings"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/parse"
)

// Document is a parsed configuration. It is immutable and safe for
// concurrent use.
type Document struct {
	dialect    Dialect
	content    *parse.Content
	root       *parse.Node
	normalized []string
	digest     evidence.Digest
}

// Parse indexes text under limits and builds the tree, normalized text and
// digest of a configuration in dialect d.
func Parse(ctx context.Context, text string, d Dialect, limits parse.Limits) (*Document, error) {
	content, err := parse.New(ctx, strings.ReplaceAll(text, "\x00", ""), limits)
	if err != nil {
		return nil, err
	}
	doc := &Document{dialect: d, content: content}
	doc.root = parse.Tree(content.All(), parse.TreeOptions{
		Reset: func(l parse.Line) bool { return d.separator(l.Text) },
		Skip:  doc.skip,
	})
	for n := range doc.root.Walk() {
		doc.normalized = append(doc.normalized, strings.Repeat(" ", n.Depth)+n.Text)
	}
	doc.digest = evidence.DigestString(strings.Join(doc.normalized, "\n"))
	return doc, nil
}

// Dialect returns the dialect the document was parsed with.
func (doc *Document) Dialect() Dialect { return doc.dialect }

// Content returns the indexed original text.
func (doc *Document) Content() *parse.Content { return doc.content }

// Root returns the root of the configuration tree. Top-level commands are
// its children.
func (doc *Document) Root() *parse.Node { return doc.root }

// Section follows a path of exact command texts from the top level.
func (doc *Document) Section(path ...string) (*parse.Node, bool) { return doc.root.Find(path...) }

// Sections returns the top-level commands starting with keyword, such as
// every "interface".
func (doc *Document) Sections(keyword string) []*parse.Node {
	return doc.root.ChildrenWithPrefix(keyword)
}

// Normalized returns the configuration without comments, separators,
// terminators, blank and volatile lines, re-indented one space per level.
// Two saves of an unchanged configuration normalize identically.
func (doc *Document) Normalized() []string { return append([]string(nil), doc.normalized...) }

// Masked returns Normalized with every secret masked, for display and
// reports.
func (doc *Document) Masked() []string {
	out := make([]string, len(doc.normalized))
	for i, l := range doc.normalized {
		out[i] = doc.dialect.Mask(l)
	}
	return out
}

// Digest returns the digest of the normalized text. It identifies the
// configuration content independently of timestamps and formatting.
func (doc *Document) Digest() evidence.Digest { return doc.digest }

// RawDigest returns the digest of the original text.
func (doc *Document) RawDigest() evidence.Digest { return doc.content.Digest() }

func (doc *Document) skip(l parse.Line) bool {
	t := strings.TrimSpace(l.Text)
	return t == "" || doc.dialect.comment(l.Text) || doc.dialect.terminator(l.Text) || doc.dialect.volatile(l.Text)
}
