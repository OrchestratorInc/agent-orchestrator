package domain

import "strings"

var searchFillerWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "be": true, "do": true, "does": true, "for": true,
	"how": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "the": true,
	"to": true, "we": true, "what": true, "when": true, "where": true, "which": true, "why": true, "with": true,
}

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

type ChatSearchHit struct {
	MessageID string
	Snippet   string
	SessionID string
	ProjectID string
	ReviewID  string
	Title     string
}
