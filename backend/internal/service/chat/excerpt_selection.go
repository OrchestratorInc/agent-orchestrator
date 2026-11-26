package chat

import (
	"bytes"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	mdtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The browser selects rendered text, whereas assistant messages are stored as
// Markdown. Verify against readable text from the durable source, never against
// a client-supplied rendering of that source.
func sourceContainsExcerptSelection(source domain.ConversationMessage, selection string) bool {
	needle := normalizeExcerptWhitespace(selection)
	if needle == "" {
		return false
	}
	if source.Role != domain.MessageRoleAssistant {
		return strings.Contains(normalizeExcerptWhitespace(source.Text), needle)
	}
	return strings.Contains(normalizeExcerptWhitespace(renderedExcerptText(source.Text)), needle) ||
		strings.Contains(normalizeExcerptWhitespace(renderedExcerptTextWithMarkers(source.Text, true)), needle)
}

func normalizeExcerptWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func renderedExcerptText(markdown string) string {
	return renderedExcerptTextWithMarkers(markdown, false)
}

// Native selectable text includes list markers, table separators, code language
// labels and image captions. Derive those from the durable Markdown too; neither
// client gets to supply the source against which a selection is verified.
func renderedExcerptTextWithMarkers(markdown string, native bool) string {
	source := []byte(markdown)
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(mdtext.NewReader(source))
	var visible strings.Builder
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if node.Type() == ast.TypeBlock {
			visible.WriteByte(' ')
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.ListItem:
			if native {
				if first := n.FirstChild(); first != nil && first.FirstChild() != nil && first.FirstChild().Kind() == extast.KindTaskCheckBox {
					break // the checkbox itself supplies the native marker
				}
				list, ok := n.Parent().(*ast.List)
				if ok && list.IsOrdered() {
					index := nativeListIndex(n, source)
					visible.WriteString(strconv.Itoa(index) + ". ")
				} else {
					visible.WriteString("• ")
				}
			}
		case *extast.TaskCheckBox:
			if native {
				if n.IsChecked {
					visible.WriteString("☑ ")
				} else {
					visible.WriteString("☐ ")
				}
			}
		case *extast.TableCell:
			if native && n.PreviousSibling() != nil {
				visible.WriteString(" | ")
			}
		case *ast.ThematicBreak:
			if native {
				visible.WriteString("────────")
			}
		case *ast.Image:
			if native {
				var alt strings.Builder
				_ = ast.Walk(n, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
					if entering {
						switch value := child.(type) {
						case *ast.Text:
							alt.WriteString(excerptTextValue(value.Value(source), value.IsRaw()))
						case *ast.String:
							alt.WriteString(excerptTextValue(value.Value, value.IsRaw() || value.IsCode()))
						}
					}
					return ast.WalkContinue, nil
				})
				caption := alt.String()
				if caption == "" {
					caption = string(n.Destination)
				}
				visible.WriteString(caption)
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			// React Markdown escapes raw HTML, leaving the markup selectable as text.
			for i := 0; i < n.Segments.Len(); i++ {
				segment := n.Segments.At(i)
				visible.Write(segment.Value(source))
			}
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				visible.Write(line.Value(source))
			}
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			visible.WriteString(excerptTextValue(n.Value(source), n.IsRaw()))
			if n.SoftLineBreak() || n.HardLineBreak() {
				visible.WriteByte(' ')
			}
		case *ast.String:
			visible.WriteString(excerptTextValue(n.Value, n.IsRaw() || n.IsCode()))
		case *ast.AutoLink:
			visible.Write(n.Label(source))
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock, *ast.FencedCodeBlock:
			if fenced, ok := node.(*ast.FencedCodeBlock); native && ok {
				if fenced.Info != nil {
					visible.Write(fenced.Info.Value(source))
				}
				visible.WriteByte(' ')
			}
			lines := node.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				visible.Write(line.Value(source))
				visible.WriteByte(' ')
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return visible.String()
}

var nativeOrderedItem = regexp.MustCompile(`^\s*\d+\.\s+.+$`)

// The mobile text surface flattens consecutive numbered lines (including nested
// items) and counts from one. Use their durable source lines for the same count.
func nativeListIndex(item *ast.ListItem, source []byte) int {
	first := item.FirstChild()
	if first == nil || first.Lines().Len() == 0 {
		return 1
	}
	line := first.Lines().At(0)
	start := bytes.LastIndexByte(source[:line.Start], '\n') + 1
	index := 1
	for start > 0 {
		end := start - 1
		start = bytes.LastIndexByte(source[:end], '\n') + 1
		if !nativeOrderedItem.Match(source[start:end]) {
			break
		}
		index++
	}
	return index
}

func excerptTextValue(value []byte, raw bool) string {
	if raw {
		return string(value)
	}
	return html.UnescapeString(string(util.UnescapePunctuations(value)))
}
