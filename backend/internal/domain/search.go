package domain

import "strings"

// searchFillerWords carry no meaning in a palette search ("where do we close
// the palette"); matching them would only dilute or slow it.
var searchFillerWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "be": true, "do": true, "does": true, "for": true,
	"how": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "the": true,
	"to": true, "we": true, "what": true, "when": true, "where": true, "which": true, "why": true, "with": true,
}

// SearchTerms splits a palette query on whitespace and drops filler words,
// unless filler is all there is.
func SearchTerms(query string) []string {
	words := strings.Fields(query)
	terms := make([]string, 0, len(words))
	for _, word := range words {
		if !searchFillerWords[strings.ToLower(word)] {
			terms = append(terms, word)
		}
	}
	if len(terms) == 0 {
		return words
	}
	return terms
}

// ChatSearchHit is one chat message matching a palette search, with where to
// open it. Snippet is a window of its text with matches wrapped in \x02 and
// \x03.
type ChatSearchHit struct {
	MessageID string
	Snippet   string
	SessionID string
	ProjectID string
	ReviewID  string
	Title     string
}
