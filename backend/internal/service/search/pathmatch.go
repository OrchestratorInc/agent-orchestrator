package search

import (
	"math"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Fuzzy file-name matching runs in memory over the session's path list; no
// index is involved. Each space-separated query word must match the path as
// a subsequence. Scoring finds the best placement of the letters (fzf-style
// dynamic programming), not the first one, and rewards word starts,
// camelCase humps, consecutive letters and matches inside the file name over
// the folders.
const (
	matchPoints   = 16
	boundaryBonus = 10
	camelBonus    = 9
	consecBonus   = 8
	basenameBonus = 6
	gapStart      = -3
	gapExtend     = -1
	negInf        = -1 << 30
)

var dpBuffers = sync.Pool{New: func() any { return new([2][]int) }}

func boundaryAt(orig string, i int) int {
	if i == 0 {
		return boundaryBonus
	}
	p, c := rune(orig[i-1]), rune(orig[i])
	if strings.ContainsRune("/_-. ", p) {
		return boundaryBonus
	}
	if unicode.IsLower(p) && unicode.IsUpper(c) {
		return camelBonus
	}
	return 0
}

// hasSubsequence is the cheap prefilter: most paths fail it, and the dynamic
// program only runs on the ones that pass.
func hasSubsequence(q, s string) bool {
	qi := 0
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] == q[qi] {
			qi++
		}
	}
	return qi == len(q)
}

// fuzzyScore scores lowercase query q against p. ok is false when q's
// letters do not all appear in order.
func fuzzyScore(q, p string) (int, bool) {
	lp := strings.ToLower(p)
	if !hasSubsequence(q, lp) {
		return 0, false
	}
	orig := p
	if len(orig) != len(lp) {
		orig = lp // non-ASCII case folding changed byte offsets; skip camelCase bonuses
	}
	n, m := len(q), len(lp)
	if n == 0 {
		return 0, true
	}
	base := strings.LastIndex(lp, "/") + 1
	bufs, ok := dpBuffers.Get().(*[2][]int)
	if !ok {
		bufs = new([2][]int)
	}
	defer dpBuffers.Put(bufs)
	if cap(bufs[0]) < m {
		bufs[0], bufs[1] = make([]int, m*2), make([]int, m*2)
	}
	prev, cur := bufs[0][:m], bufs[1][:m]
	for i := 0; i < n; i++ {
		best := negInf // best earlier placement of q[i-1], carried across a gap
		for j := 0; j < m; j++ {
			cur[j] = negInf
			if i > 0 && j >= 2 && prev[j-2] > negInf {
				if c := prev[j-2] + gapStart; c > best {
					best = c
				}
			}
			if lp[j] != q[i] {
				if best > negInf {
					best += gapExtend
				}
				continue
			}
			b := boundaryAt(orig, j)
			if j >= base {
				b += basenameBonus
			}
			s := negInf
			if i == 0 {
				s = matchPoints + b*2
			} else {
				if j >= 1 && prev[j-1] > negInf {
					s = prev[j-1] + matchPoints + max(b, consecBonus)
				}
				if best > negInf && best+matchPoints+b > s {
					s = best + matchPoints + b
				}
			}
			cur[j] = s
			if best > negInf {
				best += gapExtend
			}
		}
		prev, cur = cur, prev
	}
	top := negInf
	for _, v := range prev {
		top = max(top, v)
	}
	if top == negInf {
		return 0, false
	}
	return top*10 - len(p), true
}

// pathMatch is one file-name hit on the shared score scale.
type pathMatch struct {
	path  string
	score float64
	// exactName: the query is the file's name ("tokens.css", "ui-store").
	exactName bool
}

const (
	pathNameWeight  = 3.0
	exactNameWeight = 6.0
)

// matchPaths returns the best limit paths for query raw. strong reports a
// near-exact file-name match, used to stop "did you mean" from rewriting an
// abbreviation ("buildchan", "cfggo") into some other word.
func matchPaths(raw string, paths []string, q parsedQuery, limit int) (matches []pathMatch, strong bool) {
	words := strings.Fields(strings.ToLower(strings.ReplaceAll(raw, `"`, "")))
	if len(words) == 0 || len(words) > 3 {
		return nil, false
	}
	joined := strings.Join(words, "")
	self, _ := fuzzyScore(joined, joined)
	type hit struct {
		p string
		s int
	}
	var hits []hit
	for _, p := range paths {
		total, ok := 0, true
		for _, w := range words {
			s, m := fuzzyScore(w, p)
			if !m {
				ok = false
				break
			}
			total += s
		}
		if ok {
			hits = append(hits, hit{p, total})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		return hits[i].p < hits[j].p
	})
	// The exact-name bonus and file priors can reorder close raw scores, so
	// score a generous head of the raw ranking before cutting to limit.
	unseparated := strings.NewReplacer("-", "", "_", "")
	for i := 0; i < len(hits) && i < max(limit*4, 100); i++ {
		norm := math.Min(1, float64(hits[i].s)/float64(max(self, 1)))
		base := strings.ToLower(path.Base(hits[i].p))
		stem, _, _ := strings.Cut(base, ".")
		exact := base == joined || stem == joined || unseparated.Replace(stem) == joined
		if exact || norm >= 0.75 {
			strong = true
		}
		score := pathNameWeight * norm
		if exact {
			score += exactNameWeight
		}
		matches = append(matches, pathMatch{path: hits[i].p, score: score * filePrior(hits[i].p, q), exactName: exact})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, strong
}
