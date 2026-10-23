package search

import (
	"strings"
	"sync"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/lang/en"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	unicodetokenizer "github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/registry"
	index "github.com/blevesearch/bleve_index_api"
)

const (
	identFilterName = "ao_ident"
	// codeAnalyzer splits identifiers, lowercases, drops English stop words
	// and stems, so "closing the palette" meets closePalette.
	codeAnalyzer = "ao_code"
	// exactAnalyzer splits identifiers and lowercases, nothing else.
	exactAnalyzer = "ao_exact"
	// proseAnalyzer is codeAnalyzer without stop-word removal, for chats:
	// a remembered sentence is mostly stop words ("we will deploy it after"),
	// and an exact-phrase query can only match words the index kept.
	proseAnalyzer = "ao_prose"

	fieldContent   = "content"
	fieldStartLine = "start_line"
)

func init() {
	if err := registry.RegisterTokenFilter(identFilterName, func(map[string]interface{}, *registry.Cache) (analysis.TokenFilter, error) {
		return identFilter{}, nil
	}); err != nil {
		panic(err) // a duplicate name is a programming error
	}
}

// identFilter keeps each whole identifier and also emits its camelCase,
// snake_case, kebab-case and digit-boundary parts at the same position:
// getUserByID indexes "getuserbyid", "get", "user", "by", "id".
type identFilter struct{}

func (identFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
	out := make(analysis.TokenStream, 0, len(input)*2)
	for _, tok := range input {
		term := string(tok.Term)
		whole := strings.Trim(term, "_-")
		if whole == "" {
			continue
		}
		t := *tok
		t.Term = []byte(whole)
		out = append(out, &t)
		if parts := splitIdent(whole); len(parts) > 1 {
			for _, p := range parts {
				pt := *tok
				pt.Term = []byte(p)
				out = append(out, &pt)
			}
		}
	}
	return out
}

// splitIdent splits on _ and -, lower→upper case changes, the end of an
// acronym ("HTTPServer" → HTTP, Server) and letter/digit boundaries.
func splitIdent(s string) []string {
	var parts []string
	rs := []rune(s)
	start := 0
	flush := func(end int) {
		if end > start {
			parts = append(parts, string(rs[start:end]))
		}
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '_' || r == '-' {
			flush(i)
			start = i + 1
			continue
		}
		if i > start {
			prev := rs[i-1]
			lowerToUpper := unicode.IsLower(prev) && unicode.IsUpper(r)
			acronymEnd := unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			digitEdge := unicode.IsDigit(prev) != unicode.IsDigit(r)
			if lowerToUpper || acronymEnd || digitEdge {
				flush(i)
				start = i
			}
		}
	}
	flush(len(rs))
	return parts
}

// newIndexMapping builds the file or chat index mapping. Content is never
// stored: snippets and highlights are recomputed from the source text at
// query time. Exact-phrase queries need word positions (term vectors) and
// every word of the phrase; that nearly triples a file index, so only the
// chat index, which is small and where users search remembered sentences,
// keeps positions and stop words.
func newIndexMapping(chats bool) (*mapping.IndexMappingImpl, error) {
	im := bleve.NewIndexMapping()
	im.ScoringModel = index.BM25Scoring
	if err := im.AddCustomAnalyzer(codeAnalyzer, map[string]interface{}{
		"type":          custom.Name,
		"tokenizer":     unicodetokenizer.Name,
		"token_filters": []interface{}{identFilterName, lowercase.Name, en.StopName, en.SnowballStemmerName},
	}); err != nil {
		return nil, err
	}
	if err := im.AddCustomAnalyzer(proseAnalyzer, map[string]interface{}{
		"type":          custom.Name,
		"tokenizer":     unicodetokenizer.Name,
		"token_filters": []interface{}{identFilterName, lowercase.Name, en.SnowballStemmerName},
	}); err != nil {
		return nil, err
	}
	if err := im.AddCustomAnalyzer(exactAnalyzer, map[string]interface{}{
		"type":          custom.Name,
		"tokenizer":     unicodetokenizer.Name,
		"token_filters": []interface{}{identFilterName, lowercase.Name},
	}); err != nil {
		return nil, err
	}
	dm := bleve.NewDocumentMapping()
	content := bleve.NewTextFieldMapping()
	content.Analyzer = codeAnalyzer
	if chats {
		content.Analyzer = proseAnalyzer
	}
	content.Store = false
	content.IncludeInAll = false
	content.IncludeTermVectors = chats
	content.DocValues = false
	dm.AddFieldMappingsAt(fieldContent, content)
	line := bleve.NewNumericFieldMapping()
	line.Index = false
	line.Store = true
	line.IncludeInAll = false
	dm.AddFieldMappingsAt(fieldStartLine, line)
	im.DefaultMapping = dm
	im.DefaultAnalyzer = codeAnalyzer
	return im, nil
}

// analyzers tokenizes query terms and candidate text with the same analysis
// the index uses. Per-word results are cached: candidate text repeats the
// same vocabulary across hits, and reranking re-tokenizes it every query.
type analyzers struct {
	exact analysis.Analyzer
	stem  analysis.Analyzer

	mu    sync.Mutex
	words map[string]*wordTokens
}

type wordTokens struct {
	lower string
	exact []string
	stem  []string
}

const maxCachedWords = 200_000

func newAnalyzers(m *mapping.IndexMappingImpl) *analyzers {
	return &analyzers{
		exact: m.AnalyzerNamed(exactAnalyzer),
		stem:  m.AnalyzerNamed(codeAnalyzer),
		words: make(map[string]*wordTokens),
	}
}

func terms(a analysis.Analyzer, s string) []string {
	ts := a.Analyze([]byte(s))
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t.Term)
	}
	return out
}

func (a *analyzers) word(w string) *wordTokens {
	a.mu.Lock()
	wt, ok := a.words[w]
	a.mu.Unlock()
	if ok {
		return wt
	}
	wt = &wordTokens{lower: strings.ToLower(w), exact: terms(a.exact, w), stem: terms(a.stem, w)}
	a.mu.Lock()
	if len(a.words) >= maxCachedWords {
		a.words = make(map[string]*wordTokens)
	}
	a.words[w] = wt
	a.mu.Unlock()
	return wt
}

// stemOf returns the indexed form of a single word, or "" when the analyzer
// drops it (a stop word).
func (a *analyzers) stemOf(w string) string {
	if st := a.word(w).stem; len(st) > 0 {
		return st[0]
	}
	return ""
}

// splitWords cuts text into identifier-shaped words (letters, digits, _).
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
}
