package search

import (
	"math"
	"regexp"
	"strings"
)

// Weights of the reranker. Each feature is computed from a candidate's own
// text the same way for files and chat messages, so their scores share one
// scale. Values come from the tuning harness (see the design doc); change them
// only together with the relevance tests in score_test.go.
const (
	weightCoverage    = 2.0
	weightPhrase      = 2.5
	weightLiteralBump = 1.5 // extra phrase weight for code-like or quoted queries
	weightFuzzyPhrase = 0.7 // share of the phrase weight for a typo-tolerant phrase
	weightProximity   = 0.8
	weightBM25        = 0.3 // tie-break only; normalized per retrieval pass
	weightPathWords   = 0.6
	weightPathLiteral = 1.6 // multiplier on weightPathWords for literal queries
	weightDefinition  = 1.2

	// Credit a query word earns, by its best kind of match in a candidate.
	creditExact     = 1.0
	creditCorrected = 0.9
	creditStem      = 0.8
	creditParts     = 0.7
	creditPrefix    = 0.6
	creditTypo      = 0.5
	creditSynonym   = 0.4

	proximityWindow = 30
)

// features are what the reranker reads off one candidate text.
type features struct {
	// coverage is the IDF-weighted share of query words the text matches,
	// each at its best credit.
	coverage float64
	// phrase is 1 when the query appears verbatim, 0.95 when the
	// spell-corrected query does.
	phrase float64
	// fuzzyPhrase is 1 when every word matched (typos included) inside a
	// window barely wider than the query.
	fuzzyPhrase float64
	// prox is 1 when the matched words sit next to each other, falling to 0
	// across a 30-word window.
	prox float64
	// hits maps each lowercase word in the text that matched to how many
	// query words it answers (closePalette answers both "close" and
	// "palette"), for picking and highlighting the snippet line.
	hits map[string]int
}

func (a *analyzers) features(q parsedQuery, ts []queryTerm, text string) features {
	words := splitWords(text)
	seq := make([]*wordTokens, len(words))
	exact := make(map[string]struct{}, len(words))
	stems := make(map[string]struct{}, len(words))
	for i, w := range words {
		wt := a.word(w)
		seq[i] = wt
		for _, t := range wt.exact {
			exact[t] = struct{}{}
		}
		for _, t := range wt.stem {
			stems[t] = struct{}{}
		}
	}
	f := features{hits: make(map[string]int)}
	credits := make([]float64, len(ts))
	var num, den float64
	for i, t := range ts {
		den += t.weight
		c := termCredit(t, exact, stems, a)
		credits[i] = c
		num += t.weight * c
	}
	if den > 0 {
		f.coverage = num / den
	}

	normText := collapse(strings.ToLower(text))
	if (len(strings.Fields(q.needle)) >= 2 || q.literal) && len(q.needle) >= 4 && strings.Contains(normText, q.needle) {
		f.phrase = 1
	} else if fixed := correctedNeedle(q.needle, ts); fixed != q.needle && strings.Contains(normText, fixed) {
		f.phrase = 0.95
	}

	var active []int
	allMatched := true
	for i := range ts {
		if credits[i] >= creditTypo {
			active = append(active, i)
		} else {
			allMatched = false
		}
	}
	hitsAt := make([][]int, len(seq))
	for p, wt := range seq {
		for _, i := range active {
			if wordMatches(ts[i], wt) {
				hitsAt[p] = append(hitsAt[p], i)
			}
		}
		if n := len(hitsAt[p]); n > f.hits[wt.lower] {
			f.hits[wt.lower] = n
		}
	}
	if len(active) >= 2 {
		if best := smallestWindow(hitsAt, len(ts), len(active)); best > 0 {
			f.prox = math.Max(0, 1-float64(best-len(active))/proximityWindow)
			if f.phrase == 0 && allMatched && best <= len(strings.Fields(q.needle))+1 {
				f.fuzzyPhrase = 1
			}
		}
	}
	return f
}

// termCredit is the best credit query word t earns against a text's exact
// and stemmed token sets.
func termCredit(t queryTerm, exact, stems map[string]struct{}, a *analyzers) float64 {
	if _, ok := exact[t.word]; ok {
		return creditExact
	}
	if _, ok := stems[t.stem]; ok {
		return creditStem
	}
	if len(t.parts) > 1 {
		all := true
		for _, p := range t.parts {
			if _, ok := exact[p]; !ok {
				all = false
				break
			}
		}
		if all {
			return creditParts
		}
	}
	if t.corrected != "" {
		if _, ok := exact[t.corrected]; ok {
			return creditCorrected
		}
		if st := a.stemOf(t.corrected); st != "" {
			if _, ok := stems[st]; ok {
				return creditCorrected
			}
		}
	}
	if len(t.word) >= 4 {
		for w := range exact {
			if len(w) > len(t.word) && strings.HasPrefix(w, t.word) {
				return creditPrefix
			}
		}
	}
	if len(t.word) >= 5 {
		for w := range exact {
			if within1(t.word, w) {
				if t.unknown {
					return creditCorrected
				}
				return creditTypo
			}
		}
	}
	for _, s := range t.syns {
		if _, ok := stems[s]; ok {
			return creditSynonym
		}
	}
	return 0
}

func wordMatches(t queryTerm, wt *wordTokens) bool {
	for _, x := range wt.exact {
		if x == t.word {
			return true
		}
	}
	for _, x := range wt.stem {
		if x == t.stem {
			return true
		}
	}
	if t.corrected != "" && wt.lower == t.corrected {
		return true
	}
	if len(t.word) >= 4 && len(wt.lower) > len(t.word) && strings.HasPrefix(wt.lower, t.word) {
		return true
	}
	return len(t.word) >= 5 && within1(t.word, wt.lower)
}

// smallestWindow returns the width of the shortest run of word positions
// holding every active term, or 0 if there is none.
func smallestWindow(hitsAt [][]int, nTerms, need int) int {
	count := make([]int, nTerms)
	have, best, lo := 0, math.MaxInt, 0
	for hi := range hitsAt {
		for _, i := range hitsAt[hi] {
			if count[i] == 0 {
				have++
			}
			count[i]++
		}
		for have == need && lo <= hi {
			best = min(best, hi-lo+1)
			for _, i := range hitsAt[lo] {
				count[i]--
				if count[i] == 0 {
					have--
				}
			}
			lo++
		}
	}
	if best == math.MaxInt {
		return 0
	}
	return best
}

// contentScore combines features into the shared scale.
func contentScore(q parsedQuery, f features, bm25 float64) float64 {
	phraseWeight := weightPhrase
	if q.literal {
		phraseWeight += weightLiteralBump
	}
	return weightCoverage*f.coverage +
		phraseWeight*f.phrase +
		weightFuzzyPhrase*phraseWeight*f.fuzzyPhrase +
		weightProximity*f.prox*f.coverage +
		weightBM25*bm25
}

// decisive marks content evidence strong enough to take the top slot from a
// matching name in the palette: a quoted or code-like query, or an exact
// phrase of three or more content words.
func decisive(q parsedQuery, ts []queryTerm, f features) bool {
	return (q.literal && f.coverage > 0) || (f.phrase >= 0.95 && len(ts) >= 3)
}

var definitionRe = regexp.MustCompile(`(?m)^\s*(export\s+)?(async\s+)?(func(\s*\([^)]*\))?|function|class|type|interface|const|let|var|def|struct|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// definesQueried reports whether text defines an identifier the query names,
// so `func resolveDataDir` outranks a call site.
func definesQueried(text string, ts []queryTerm) bool {
	for _, m := range definitionRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[5])
		for _, t := range ts {
			if name == t.word {
				return true
			}
		}
	}
	return false
}

func isTestPath(p string) bool {
	return strings.Contains(p, "_test.") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") || strings.Contains(p, "/e2e/")
}

// filePrior scales file results: tests, generated code, lockfiles and
// translation catalogs rarely answer a search unless asked for.
func filePrior(p string, q parsedQuery) float64 {
	m := 1.0
	switch {
	case strings.Contains(p, "/i18n/") && strings.HasSuffix(p, ".json") && !strings.HasSuffix(p, "en.json"):
		m = 0.3
	case strings.Contains(p, "/i18n/") && strings.HasSuffix(p, ".json") && !q.literal:
		m = 0.6
	case strings.HasSuffix(p, "package-lock.json") || strings.HasSuffix(p, ".lock") || strings.HasSuffix(p, "go.sum"):
		m = 0.3
	case strings.Contains(p, "/gen/") || strings.Contains(p, ".gen.") || strings.HasSuffix(p, ".pb.go") || strings.Contains(p, "apispec/openapi"):
		m = 0.5
	case strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "docs/"):
		m = 0.9
	}
	if isTestPath(p) && !q.wantTest {
		m *= 0.75
	}
	return m
}

// Snippet is one line of a hit with the matched words marked.
type Snippet struct {
	Text string
	// Line is 1-based within the file, or 0 for chat messages.
	Line       int
	Highlights [][2]int // byte ranges in Text
}

const maxSnippetLen = 200

// bestSnippet picks the line of text that answers the most query words and
// marks the matched words. firstLine is the 1-based line number of text's
// first line.
func bestSnippet(text string, hits map[string]int, firstLine int) Snippet {
	lines := strings.Split(text, "\n")
	bestIdx, bestCount := -1, 0
	for i, line := range lines {
		n := 0
		for _, w := range splitWords(line) {
			n += hits[strings.ToLower(w)]
		}
		if n > bestCount {
			bestIdx, bestCount = i, n
		}
	}
	if bestIdx < 0 {
		for i, line := range lines {
			if strings.TrimSpace(line) != "" {
				bestIdx = i
				break
			}
		}
		if bestIdx < 0 {
			return Snippet{}
		}
	}
	line := strings.TrimRight(strings.TrimLeft(lines[bestIdx], " \t"), " \t\r")
	s := Snippet{Text: line}
	if firstLine > 0 {
		s.Line = firstLine + bestIdx
	}
	s.Highlights = highlightRanges(line, hits)
	if len(s.Text) > maxSnippetLen {
		s = clipSnippet(s)
	}
	return s
}

func highlightRanges(line string, hits map[string]int) [][2]int {
	var out [][2]int
	i := 0
	for i < len(line) {
		j := i
		for j < len(line) && isWordByte(line[j]) {
			j++
		}
		if j > i {
			// Lowercase the word alone: folding can change byte lengths
			// ("İ"), so offsets must stay in the original line.
			if hits[strings.ToLower(line[i:j])] > 0 {
				out = append(out, [2]int{i, j})
			}
			i = j
			continue
		}
		i++
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// clipSnippet keeps a maxSnippetLen window around the first highlight.
func clipSnippet(s Snippet) Snippet {
	start := 0
	if len(s.Highlights) > 0 {
		start = max(0, s.Highlights[0][0]-maxSnippetLen/4)
	}
	end := min(len(s.Text), start+maxSnippetLen)
	for start > 0 && start < len(s.Text) && s.Text[start]&0xC0 == 0x80 {
		start--
	}
	for end < len(s.Text) && s.Text[end]&0xC0 == 0x80 {
		end++
	}
	out := Snippet{Text: s.Text[start:end], Line: s.Line}
	for _, h := range s.Highlights {
		if h[0] >= start && h[1] <= end {
			out.Highlights = append(out.Highlights, [2]int{h[0] - start, h[1] - start})
		}
	}
	return out
}
