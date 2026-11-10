package chat

import (
	"bytes"
	"html"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var findMarkdownPool = sync.Pool{New: func() any { return goldmark.New(goldmark.WithExtensions(extension.GFM)) }}
var findUserMarkdownPool = sync.Pool{New: func() any { return newUserFindMarkdown() }}

// newUserFindMarkdown parses the markdown the chat renders in a person's own
// message (UserMarkdown in ChatMarkdown.tsx): lists, task boxes, block quotes,
// fenced code, code spans, emphasis and links. Headings, rules, indented code,
// HTML, tables, strikethrough and link definitions stay literal text there, so
// they have no parser here. Change both together.
func newUserFindMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithParser(parser.NewParser(
			parser.WithBlockParsers(
				util.Prioritized(parser.NewListParser(), 300),
				util.Prioritized(parser.NewListItemParser(), 400),
				util.Prioritized(parser.NewFencedCodeBlockParser(), 700),
				util.Prioritized(parser.NewBlockquoteParser(), 800),
				util.Prioritized(parser.NewParagraphParser(), 1000),
			),
			parser.WithInlineParsers(
				util.Prioritized(parser.NewCodeSpanParser(), 100),
				util.Prioritized(parser.NewLinkParser(), 200),
				util.Prioritized(parser.NewAutoLinkParser(), 300),
				util.Prioritized(parser.NewEmphasisParser(), 500),
			),
		)),
		goldmark.WithExtensions(extension.Linkify, extension.TaskList),
	)
}

// FindBlocks projects message prose into independent displayed text blocks:
// agent messages as full Markdown, or the whole source as one plain block.
func FindBlocks(source string, markdown bool) []string {
	if !markdown {
		return []string{source}
	}
	md, ok := findMarkdownPool.Get().(goldmark.Markdown)
	if !ok {
		md = goldmark.New(goldmark.WithExtensions(extension.GFM))
	}
	defer findMarkdownPool.Put(md)
	return findMarkdownBlocks(md, []byte(source), false)
}

// FindUserBlocks projects a person's own message through the light Markdown
// subset its bubble renders.
func FindUserBlocks(source string) []string {
	md, ok := findUserMarkdownPool.Get().(goldmark.Markdown)
	if !ok {
		md = newUserFindMarkdown()
	}
	defer findUserMarkdownPool.Put(md)
	return findMarkdownBlocks(md, []byte(source), true)
}

// In a person's message image syntax is not parsed: `![alt](url)` shows as a
// "!" before a link, and every fence, mermaid and math included, is shown as code.
func findMarkdownBlocks(md goldmark.Markdown, input []byte, user bool) []string {
	root := md.Parser().Parse(text.NewReader(input))
	var blocks []string
	var body strings.Builder
	flush := func() {
		if body.Len() > 0 {
			blocks = append(blocks, body.String())
			body.Reset()
		}
	}
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if node.Type() == ast.TypeBlock {
				flush()
			}
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Image:
			if user {
				body.WriteByte('!')
				return ast.WalkContinue, nil
			}
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			body.Write(n.Label(input))
		case *ast.FencedCodeBlock:
			flush()
			language := strings.ToLower(string(n.Language(input)))
			if user || language != "mermaid" && language != "math" {
				var code bytes.Buffer
				for i := 0; i < n.Lines().Len(); i++ {
					line := n.Lines().At(i)
					code.Write(line.Value(input))
				}
				blocks = append(blocks, code.String())
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			flush()
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				body.Write(line.Value(input))
			}
			flush()
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			value := string(n.Segment.Value(input))
			// Goldmark retains backslash escapes and entities in ordinary text.
			if _, code := node.Parent().(*ast.CodeSpan); !code {
				value = unescapeFindText(value)
			}
			body.WriteString(value)
			if n.SoftLineBreak() || n.HardLineBreak() {
				body.WriteByte('\n')
			}
		case *ast.String:
			body.Write(n.Value)
		case *ast.RawHTML:
			// React escapes raw HTML rather than interpreting it.
			for i := 0; i < n.Segments.Len(); i++ {
				segment := n.Segments.At(i)
				body.Write(segment.Value(input))
			}
		case *ast.HTMLBlock:
			flush()
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				body.Write(line.Value(input))
			}
			if n.HasClosure() {
				body.Write(n.ClosureLine.Value(input))
			}
			flush()
			return ast.WalkSkipChildren, nil
		default:
			if node.Type() == ast.TypeBlock {
				flush()
			}
		}
		return ast.WalkContinue, nil
	})
	flush()
	return blocks
}

func unescapeFindText(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(s[i+1])) {
			i++
		}
		out.WriteByte(s[i])
	}
	return html.UnescapeString(out.String())
}

type findText struct {
	text         string
	starts, ends []int
	identity     bool
}

func (f findText) startAt(index int) int {
	if f.identity {
		return index
	}
	return f.starts[index]
}
func (f findText) endAt(index int) int {
	if f.identity {
		return index + 1
	}
	return f.ends[index]
}

// Offsets are UTF-16 positions in the original displayed block, not in its
// folded representation. Canonical segments map expansions back to whole glyphs.
func foldFindText(source string) findText {
	// Most transcript text is ASCII. Avoid grapheme segmentation and per-glyph
	// case-fold allocations on this path (large code blocks in particular).
	ascii, collapsed, previousSpace := true, false, false
	for i := range len(source) {
		if source[i] >= 128 {
			ascii = false
			break
		}
		space := source[i] == ' ' || source[i] >= '\t' && source[i] <= '\r'
		collapsed = collapsed || space && previousSpace
		previousSpace = space
	}
	if ascii {
		if !collapsed {
			buffer := []byte(source)
			for i, c := range buffer {
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				if c >= '\t' && c <= '\r' {
					c = ' '
				}
				buffer[i] = c
			}
			return findText{text: string(buffer), identity: true}
		}
		result := findText{starts: make([]int, 0, len(source)), ends: make([]int, 0, len(source))}
		buffer := make([]byte, 0, len(source))
		for i := range len(source) {
			c := source[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c == ' ' || c >= '\t' && c <= '\r' {
				c = ' '
				if len(buffer) > 0 && buffer[len(buffer)-1] == ' ' {
					result.ends[len(result.ends)-1] = i + 1
					continue
				}
			}
			buffer = append(buffer, c)
			result.starts = append(result.starts, i)
			result.ends = append(result.ends, i+1)
		}
		result.text = string(buffer)
		return result
	}
	var result findText
	var out strings.Builder
	iter := uniseg.NewGraphemes(source)
	folder := cases.Fold()
	offset := 0
	space := false
	for iter.Next() {
		segment := iter.Str()
		end := offset + len(utf16.Encode([]rune(segment)))
		for _, r := range folder.String(norm.NFD.String(segment)) {
			if unicode.IsSpace(r) || r == '\uFEFF' {
				if space {
					if len(result.ends) > 0 {
						result.ends[len(result.ends)-1] = end
					}
					continue
				}
				r = ' '
				space = true
			} else {
				space = false
			}
			value := string(r)
			out.WriteString(value)
			for range len(value) {
				result.starts = append(result.starts, offset)
				result.ends = append(result.ends, end)
			}
		}
		offset = end
	}
	result.text = out.String()
	return result
}
