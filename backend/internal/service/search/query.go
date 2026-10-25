package search

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// queryWords are dropped from natural-language queries ("where do we close
// the palette" → close, palette). Content words only.
var queryWords = wordSet("a an and are as at be by do does did for from how i in is it of on or the this that to we where what when which who why with our us you your can should get there here instead into out me my was were has have had about any some also just then so not no yes please pls")

// softWords describe what the user wants rather than what the code says.
// They still count, at a third of their weight, so a rare filler word cannot
// outrank the content words of the same query.
var softWords = wordSet("detect detects detecting handle handles handled handling check checks checking make makes find finds show shows shown use uses used using implement implemented implementation logic code function functions thing things stuff way ways work works happen happens done want need needs see look looking keep trigger triggers place part")

// synonyms is a small code-vocabulary thesaurus, applied at query time only
// and at reduced weight.
var synonyms = map[string][]string{
	"cmd": {"command"}, "command": {"cmd"}, "dir": {"directory", "path"}, "directory": {"dir"},
	"cfg": {"config"}, "config": {"cfg", "settings"}, "settings": {"config", "preferences"},
	"repo": {"repository"}, "msg": {"message"}, "message": {"msg"}, "err": {"error"}, "error": {"err"},
	"ctx": {"context"}, "db": {"database", "sqlite"}, "pr": {"pull"}, "auth": {"login", "token"},
	"login": {"auth", "signin"}, "delete": {"remove"}, "remove": {"delete"}, "close": {"dismiss"},
	"shortcut": {"keybinding", "hotkey"}, "keybinding": {"shortcut"}, "terminal": {"pty", "xterm"},
	"freeze": {"snapshot", "frozen"}, "flash": {"flicker"}, "descriptors": {"fd", "nofile"},
	"bug": {"fix", "issue"}, "fix": {"bug"}, "slow": {"perf", "latency"}, "perf": {"performance", "slow"},
}

// appAliases are names users type for AO features.
var appAliases = []struct{ alias, meaning string }{
	{"cmd+k", "command palette"}, {"cmd k", "command palette"}, {"cmdk", "command palette"},
	{"ctrl+k", "command palette"}, {"ctrl k", "command palette"}, {"⌘k", "command palette"},
}

func wordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

// codeLike marks queries that are identifiers or code: camelCase, snake_case,
// ALL_CAPS constants, paths, calls, member access.
var codeLike = regexp.MustCompile(`[a-z][A-Z]|_|\.|/|\(|\{|::|->|=|^[A-Z0-9_]{4,}$`)

// parsedQuery is a user query split for retrieval and scoring.
type parsedQuery struct {
	raw string
	// needle is the phrase to find verbatim: the quoted part if the user
	// quoted something, otherwise the whole query, lowercased and with
	// whitespace collapsed.
	needle string
	// words are the content words after stop-word removal.
	words []string
	// literal queries (code-like or quoted) favor exact matches.
	literal  bool
	wantTest bool
}

func parseQuery(raw string) parsedQuery {
	q := parsedQuery{raw: raw}
	s := strings.TrimSpace(raw)
	quoted := ""
	if strings.Count(s, `"`) >= 2 {
		i, j := strings.Index(s, `"`), strings.LastIndex(s, `"`)
		quoted = strings.ToLower(s[i+1 : j])
		q.literal = true
	}
	if codeLike.MatchString(s) {
		q.literal = true
	}
	q.needle = collapse(strings.ToLower(strings.ReplaceAll(s, `"`, "")))
	if quoted != "" {
		q.needle = collapse(quoted)
	}
	lowered := strings.ToLower(s)
	for _, a := range appAliases {
		lowered = strings.ReplaceAll(lowered, a.alias, a.meaning)
	}
	for _, w := range strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if !queryWords[w] {
			q.words = append(q.words, w)
		}
	}
	if len(q.words) == 0 {
		q.words = splitWords(lowered)
	}
	q.wantTest = strings.Contains(lowered, "test") || strings.Contains(lowered, "spec")
	return q
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// queryTerm is one content word with everything scoring needs about it.
type queryTerm struct {
	word  string
	stem  string
	parts []string // identifier parts, when the word is a compound
	syns  []string // stems of synonyms
	// corrected is the "did you mean" replacement for a word the index has
	// never seen.
	corrected string
	unknown   bool
	weight    float64 // IDF, reduced for soft words
}

// idfFunc returns the inverse document frequency of an indexed term and
// whether the term occurs at all.
type idfFunc func(stem string) (float64, bool)

// prepareTerms resolves each content word against the index. Words the
// analyzer drops (stop words) are left out entirely: requiring them would
// make "stack prs on top of each other" demand words no document has.
func prepareTerms(q parsedQuery, a *analyzers, idf idfFunc, correct func(string) string) []queryTerm {
	out := make([]queryTerm, 0, len(q.words))
	for _, w := range q.words {
		stem := a.stemOf(w)
		if stem == "" {
			continue
		}
		t := queryTerm{word: w, stem: stem}
		if ex := a.word(w).exact; len(ex) > 1 {
			t.parts = ex[1:]
		}
		for _, s := range synonyms[w] {
			if st := a.stemOf(s); st != "" {
				t.syns = append(t.syns, st)
			}
		}
		weight, known := idf(stem)
		t.weight, t.unknown = weight, !known
		if softWords[w] && len(q.words) > 2 {
			t.weight /= 3
		}
		if t.unknown && correct != nil && len(w) >= 4 {
			t.corrected = correct(w)
		}
		out = append(out, t)
	}
	return out
}

// correctedNeedle is the phrase the user meant, with each corrected word
// swapped in.
func correctedNeedle(needle string, ts []queryTerm) string {
	words := strings.Fields(needle)
	changed := false
	for i, w := range words {
		bare := strings.Trim(w, ".,:;!?()[]{}\"'`*")
		for _, t := range ts {
			if t.corrected != "" && bare == t.word {
				words[i] = strings.Replace(w, bare, t.corrected, 1)
				changed = true
			}
		}
	}
	if !changed {
		return needle
	}
	return strings.Join(words, " ")
}

// bm25IDF matches Bleve's BM25 idf so query-side weights follow the index.
func bm25IDF(docs, df float64) float64 {
	return math.Log(1 + (docs-df+0.5)/(df+0.5))
}
