package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const SessionSearchExpansionMarker = "\x1f"

const (
	SessionSearchSnippetStart = "\x02"
	SessionSearchSnippetEnd   = "\x03"
)

type SessionSearchHighlightTerm struct {
	Text   string
	Prefix bool
}

const sessionSearchSnippetTokens = 14

func SessionSearchSnippet(text string, terms []SessionSearchHighlightTerm) string {
	if cut := strings.Index(text, SessionSearchExpansionMarker); cut >= 0 {
		text = text[:cut]
	}
	tokens := snippetTokens(text)
	if len(tokens) == 0 {
		return ""
	}
	matched := make([]int, len(tokens))
	for index := range tokens {
		matched[index] = matchSnippetToken(text[tokens[index].start:tokens[index].end], terms)
	}
	start := bestSnippetWindow(matched, len(terms))
	start = max(0, min(start, len(tokens)-sessionSearchSnippetTokens))
	end := min(start+sessionSearchSnippetTokens, len(tokens))

	var builder strings.Builder
	cursor := tokens[start].start
	if start > 0 {
		builder.WriteString("…")
	} else {
		cursor = 0
	}
	for index := start; index < end; index++ {
		token := tokens[index]
		writeCollapsed(&builder, text[cursor:token.start], index == start)
		if matched[index] > 0 {
			builder.WriteString(SessionSearchSnippetStart)
			builder.WriteString(text[token.start:token.end])
			builder.WriteString(SessionSearchSnippetEnd)
		} else {
			builder.WriteString(text[token.start:token.end])
		}
		cursor = token.end
	}
	if end < len(tokens) {
		builder.WriteString("…")
	} else {
		writeCollapsed(&builder, text[cursor:], false)
	}
	return strings.TrimSpace(builder.String())
}

type snippetToken struct{ start, end int }

func snippetTokens(text string) []snippetToken {
	var tokens []snippetToken
	start := -1
	for offset, r := range text {
		inside := unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
		switch {
		case inside && start < 0:
			start = offset
		case !inside && start >= 0:
			tokens = append(tokens, snippetToken{start, offset})
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, snippetToken{start, len(text)})
	}
	return tokens
}

func matchSnippetToken(token string, terms []SessionSearchHighlightTerm) int {
	folded := FoldSessionSearchText(token)
	var parts []string
	for index, term := range terms {
		if term.Text == "" {
			continue
		}
		if folded == term.Text || (term.Prefix && strings.HasPrefix(folded, term.Text)) {
			return index + 1
		}
		if parts == nil {
			parts = camelCaseParts(token)
			if parts == nil {
				parts = []string{}
			}
		}
		for _, part := range parts {
			part = FoldSessionSearchText(part)
			if part == term.Text || (term.Prefix && strings.HasPrefix(part, term.Text)) {
				return index + 1
			}
		}
	}
	return 0
}

func SessionSearchTextHasAllTerms(text string, terms []SessionSearchHighlightTerm) bool {
	if len(terms) == 0 {
		return false
	}
	seen := make([]bool, len(terms))
	missing := len(terms)
	for _, token := range snippetTokens(text) {
		word := text[token.start:token.end]
		for index, term := range terms {
			if seen[index] || matchSnippetToken(word, []SessionSearchHighlightTerm{term}) == 0 {
				continue
			}
			seen[index] = true
			missing--
		}
		if missing == 0 {
			return true
		}
	}
	return false
}

func bestSnippetWindow(matched []int, termCount int) int {
	best, bestDistinct, bestHits := 0, -1, -1
	seen := make([]bool, termCount+1)
	for first := range matched {
		if matched[first] == 0 {
			continue
		}
		start := max(first-2, 0)
		end := min(start+sessionSearchSnippetTokens, len(matched))
		clear(seen)
		distinct, hits := 0, 0
		for index := start; index < end; index++ {
			if term := matched[index]; term > 0 {
				hits++
				if !seen[term] {
					seen[term] = true
					distinct++
				}
			}
		}
		if distinct > bestDistinct || (distinct == bestDistinct && hits > bestHits) {
			best, bestDistinct, bestHits = start, distinct, hits
		}
	}
	return best
}

func writeCollapsed(builder *strings.Builder, separator string, leading bool) {
	if separator == "" {
		return
	}
	if leading {
		separator = strings.TrimLeftFunc(separator, unicode.IsSpace)
	}
	space := false
	for _, r := range separator {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			builder.WriteByte(' ')
			space = false
		}
		builder.WriteRune(r)
	}
	if space {
		builder.WriteByte(' ')
	}
}

func FoldSessionSearchText(text string) string {
	lower := strings.ToLower(text)
	ascii := true
	for index := 0; index < len(lower); index++ {
		if lower[index] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return lower
	}
	var builder strings.Builder
	for _, r := range norm.NFD.String(lower) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func sessionSearchHighlightTerms(term string, prefix bool) []SessionSearchHighlightTerm {
	tokens := snippetTokens(term)
	out := make([]SessionSearchHighlightTerm, 0, len(tokens))
	for index, token := range tokens {
		out = append(out, SessionSearchHighlightTerm{
			Text:   FoldSessionSearchText(term[token.start:token.end]),
			Prefix: prefix && index == len(tokens)-1,
		})
	}
	return out
}
