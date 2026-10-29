package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// SessionSearchExpansionMarker separates a document's own text from the
// identifier parts appended to it for indexing ("handleSubmit" also indexes
// "handle submit"). The tokenizer treats it as a separator, so it changes
// nothing about matching; the snippet builder stops at it, so the appended
// parts never show up as if they were part of the message.
const SessionSearchExpansionMarker = "\x1f"

// SessionSearchSnippetStart and SessionSearchSnippetEnd bracket a matched term
// inside a snippet. They are control characters, so no transcript contains them,
// and the service turns them into offsets before anything reaches a renderer.
const (
	SessionSearchSnippetStart = "\x02"
	SessionSearchSnippetEnd   = "\x03"
)

// SessionSearchHighlightTerm is one term a snippet marks: the folded text of a
// query token, and whether it matches as a prefix.
type SessionSearchHighlightTerm struct {
	Text   string
	Prefix bool
}

// sessionSearchSnippetTokens is how many tokens a snippet shows. It matches the
// FTS5 snippet() length the surfaces were designed around.
const sessionSearchSnippetTokens = 14

// SessionSearchSnippet returns the part of text that best shows why it matched,
// with each matched token bracketed by SessionSearchSnippetStart/End.
//
// It replaces FTS5's snippet() for result pages. snippet() re-runs the full
// MATCH, and for a prefix term FTS5 rebuilds the merged posting list of every
// token sharing the prefix on each run: on a 22,000-document archive that was
// 600 ms of a 900 ms keystroke. The page's rows already carry their text, so the
// snippet is cut here instead, with the same tokenization rules as the index
// (unicode61: letters and digits form tokens, case and diacritics fold).
//
// The window is the run of sessionSearchSnippetTokens tokens holding the most
// distinct query terms, then the most matches, then the earliest; it starts a
// couple of tokens before its first match so the match has context.
func SessionSearchSnippet(text string, terms []SessionSearchHighlightTerm) string {
	if cut := strings.Index(text, SessionSearchExpansionMarker); cut >= 0 {
		text = text[:cut]
	}
	tokens := snippetTokens(text)
	if len(tokens) == 0 {
		return ""
	}
	matched := make([]int, len(tokens)) // index into terms + 1, or 0
	for index := range tokens {
		matched[index] = matchSnippetToken(text[tokens[index].start:tokens[index].end], terms)
	}
	start := bestSnippetWindow(matched, len(terms))
	// A window near the end slides back so it is full: a short text shows whole.
	start = max(0, min(start, len(tokens)-sessionSearchSnippetTokens))
	end := min(start+sessionSearchSnippetTokens, len(tokens))

	var builder strings.Builder
	cursor := tokens[start].start
	if start > 0 {
		builder.WriteString("…")
	} else {
		// From the very start, keep any leading punctuation ("--- FAIL:").
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

// snippetTokens splits text into unicode61 tokens: maximal runs of letters,
// digits and combining marks.
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

// matchSnippetToken returns 1 + the index of the first term the token matches,
// or 0. A token matches a term whole, by prefix when the term is a prefix, or
// through one of its camel-case parts, so "submit" marks "handleSubmit" the way
// the index found it.
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

// SessionSearchTextHasAllTerms reports whether every term appears in text,
// matched the way the snippet marks them (whole, by prefix, or through a
// camel-case part). The ranking's name tier uses it on session names: whether a
// name holds every typed word depends only on the name and the query, not on
// any index statistics.
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

// bestSnippetWindow picks the first token of the snippet.
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

// writeCollapsed writes the separator between two tokens with any run of
// whitespace reduced to one space, so a snippet of command output reads as one
// line.
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

// FoldSessionSearchText lowercases text and removes diacritics, the way the
// index's unicode61 tokenizer (remove_diacritics 2) folds it, so "Café" and
// "cafe" compare equal.
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

// sessionSearchHighlightTerms turns one FTS term (a word or a phrase) into the
// tokens a snippet should mark. A phrase marks each of its tokens; a prefix
// applies to the phrase's last token, as it does in FTS5.
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
