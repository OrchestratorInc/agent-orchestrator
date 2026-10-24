package search

import (
	"math"
	"strings"

	"github.com/blevesearch/bleve/v2/search/query"
)

func matchQuery(text string, boost float64, fuzziness int) query.Query {
	m := query.NewMatchQuery(text)
	m.SetField(fieldContent)
	m.SetBoost(boost)
	if fuzziness > 0 {
		m.SetFuzziness(fuzziness)
	}
	return m
}

// retrievalQuery finds candidates for the reranker: each content word may
// match as itself, its spell correction, a typo of it or a synonym, and half
// of the words (at least one) must match. An in-order phrase boosts, when the
// index keeps word positions.
func retrievalQuery(q parsedQuery, ts []queryTerm, phrases bool) query.Query {
	per := make([]query.Query, 0, len(ts))
	for _, t := range ts {
		alts := []query.Query{matchQuery(t.word, 1, 0)}
		switch {
		case t.corrected != "":
			alts = append(alts, matchQuery(t.corrected, 1, 0))
		case t.unknown && len(t.word) >= 4:
			alts = append(alts, matchQuery(t.word, 1, 1))
			if len(t.word) >= 8 {
				alts = append(alts, matchQuery(t.word, 0.6, 2))
			}
		case len(t.word) >= 5:
			alts = append(alts, matchQuery(t.word, 0.3, 1))
		}
		for _, syn := range synonyms[t.word] {
			alts = append(alts, matchQuery(syn, 0.4, 0))
		}
		per = append(per, query.NewDisjunctionQuery(alts))
	}
	words := query.NewDisjunctionQuery(per)
	words.SetMin(math.Max(1, math.Ceil(float64(len(per))*0.5)))
	if len(ts) < 2 || !phrases {
		return words
	}
	phrase := query.NewMatchPhraseQuery(strings.Join(q.words, " "))
	phrase.SetField(fieldContent)
	phrase.SetBoost(3)
	return query.NewDisjunctionQuery([]query.Query{words, phrase})
}

// phraseQuery retrieves texts containing needle in order. It runs as its own
// pass so a copied sentence is found even when its words are common.
func phraseQuery(needle string) query.Query {
	p := query.NewMatchPhraseQuery(needle)
	p.SetField(fieldContent)
	return p
}
