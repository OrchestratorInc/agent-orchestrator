package search

import (
	"encoding/json"
	"strings"
	"sync"
)

// vocabulary counts the surface words an index has seen, for "did you
// mean": a query word the index has never seen becomes the most frequent
// known word one edit away (two for words of 7+ letters). Counts only grow between
// rebuilds; they rank corrections, they are not exact frequencies.
type vocabulary struct {
	mu     sync.RWMutex
	counts map[string]int
	byLen  map[int][]string
}

const (
	minVocabWord = 3
	maxVocabWord = 40
)

func newVocabulary() *vocabulary {
	return &vocabulary{counts: make(map[string]int), byLen: make(map[int][]string)}
}

func (v *vocabulary) addText(text string) {
	words := splitWords(text)
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, w := range words {
		if len(w) < minVocabWord || len(w) > maxVocabWord {
			continue
		}
		v.addLocked(strings.ToLower(w))
		if parts := splitIdent(w); len(parts) > 1 {
			for _, p := range parts {
				if len(p) >= minVocabWord {
					v.addLocked(strings.ToLower(p))
				}
			}
		}
	}
}

func (v *vocabulary) addLocked(w string) {
	if v.counts[w] == 0 {
		v.byLen[len(w)] = append(v.byLen[len(w)], w)
	}
	v.counts[w]++
}

// correct returns the best known word within one edit of w (two edits for
// words of 7+ letters, which covers a doubled letter typed in the wrong
// place), or "".
func (v *vocabulary) correct(w string) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	best, bestN := "", 0
	try := func(maxEdits int) {
		for l := len(w) - maxEdits; l <= len(w)+maxEdits; l++ {
			for _, cand := range v.byLen[l] {
				if n := v.counts[cand]; n > bestN && withinEdits(w, cand, maxEdits) {
					best, bestN = cand, n
				}
			}
		}
	}
	try(1)
	if best == "" && len(w) >= 7 {
		try(2)
	}
	return best
}

func (v *vocabulary) MarshalJSON() ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return json.Marshal(v.counts)
}

func (v *vocabulary) UnmarshalJSON(data []byte) error {
	counts := make(map[string]int)
	if err := json.Unmarshal(data, &counts); err != nil {
		return err
	}
	byLen := make(map[int][]string)
	for w := range counts {
		byLen[len(w)] = append(byLen[len(w)], w)
	}
	v.mu.Lock()
	v.counts, v.byLen = counts, byLen
	v.mu.Unlock()
	return nil
}

// mergeCorrect asks each vocabulary and keeps the most frequent answer.
func mergeCorrect(vs ...*vocabulary) func(string) string {
	return func(w string) string {
		best, bestN := "", 0
		for _, v := range vs {
			if v == nil {
				continue
			}
			if c := v.correct(w); c != "" {
				v.mu.RLock()
				n := v.counts[c]
				v.mu.RUnlock()
				if n > bestN {
					best, bestN = c, n
				}
			}
		}
		return best
	}
}

// within1 reports whether a and b differ by at most one insertion,
// deletion, substitution or adjacent transposition.
func within1(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i := 0
	for i < la && i < lb && a[i] == b[i] {
		i++
	}
	if la == lb {
		if i+1 < la && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] {
			return true
		}
		return a[i+1:] == b[i+1:]
	}
	if la > lb {
		return a[i+1:] == b[i:]
	}
	return a[i:] == b[i+1:]
}

// withinEdits is a bounded Damerau-Levenshtein check.
func withinEdits(a, b string, k int) bool {
	if k <= 1 {
		return within1(a, b)
	}
	la, lb := len(a), len(b)
	if la-lb > k || lb-la > k {
		return false
	}
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > k {
			return false
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[lb] <= k
}
