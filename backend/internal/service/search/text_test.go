package search

import (
	"reflect"
	"strings"
	"testing"
)

// Deliberate typos: the spelling corrector is what these tests exercise.
const (
	typoPalette       = "pallete"      //nolint:misspell // deliberate typo under test
	typoConfiguration = "configuraton" //nolint:misspell // deliberate typo under test
)

func testAnalyzers(t *testing.T) *analyzers {
	t.Helper()
	m, err := newIndexMapping(false)
	if err != nil {
		t.Fatal(err)
	}
	return newAnalyzers(m)
}

func TestSplitIdent(t *testing.T) {
	for in, want := range map[string][]string{
		"getUserByID":    {"get", "User", "By", "ID"},
		"HTTPServer":     {"HTTP", "Server"},
		"snake_case_var": {"snake", "case", "var"},
		"kebab-name":     {"kebab", "name"},
		"utf8Decode":     {"utf", "8", "Decode"},
		"plain":          {"plain"},
	} {
		if got := splitIdent(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitIdent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAnalyzersSplitIdentifiersAndStemWords(t *testing.T) {
	a := testAnalyzers(t)
	exact := terms(a.exact, "closePalette")
	if !reflect.DeepEqual(exact, []string{"closepalette", "close", "palette"}) {
		t.Fatalf("exact = %v", exact)
	}
	// "closing" and "closePalette" meet on the stem "close".
	if a.stemOf("closing") != a.stemOf("close") {
		t.Fatalf("closing stems to %q, close to %q", a.stemOf("closing"), a.stemOf("close"))
	}
	if a.stemOf("the") != "" {
		t.Fatalf("stop word kept: %q", a.stemOf("the"))
	}
}

func TestParseQuery(t *testing.T) {
	q := parseQuery("Where do we close the command palette?")
	if q.literal || !reflect.DeepEqual(q.words, []string{"close", "command", "palette"}) {
		t.Fatalf("natural query = %+v", q)
	}
	if q.needle != "where do we close the command palette?" {
		t.Fatalf("needle = %q", q.needle)
	}
	for _, code := range []string{"resolveDataDir", "MAX_RETRIES", "config.go", "a/b", "fn()", "x = y"} {
		if !parseQuery(code).literal {
			t.Errorf("%q not literal", code)
		}
	}
	quoted := parseQuery(`find "Some workspace files" here`)
	if !quoted.literal || quoted.needle != "some workspace files" {
		t.Fatalf("quoted = %+v", quoted)
	}
	alias := parseQuery("cmd k search")
	if !reflect.DeepEqual(alias.words, []string{"command", "palette", "search"}) {
		t.Fatalf("alias words = %v", alias.words)
	}
	if !parseQuery("palette test").wantTest {
		t.Fatal("wantTest not set")
	}
}

func TestPrepareTermsDropsWordsTheIndexDrops(t *testing.T) {
	a := testAnalyzers(t)
	idf := func(string) (float64, bool) { return 1, true }
	// "each" and "other" are not query stop words but the analyzer drops
	// them; requiring them made "stack prs on top of each other" miss.
	ts := prepareTerms(parseQuery("stack prs on top of each other"), a, idf, nil)
	var words []string
	for _, tm := range ts {
		words = append(words, tm.word)
	}
	if !reflect.DeepEqual(words, []string{"stack", "prs", "top"}) {
		t.Fatalf("terms = %v", words)
	}
}

func TestPrepareTermsSoftensFillerWordsAndCorrectsUnknownWords(t *testing.T) {
	a := testAnalyzers(t)
	idf := func(stem string) (float64, bool) { return 3, stem != a.stemOf(typoPalette) }
	correct := func(w string) string {
		if w == typoPalette {
			return "palette"
		}
		return ""
	}
	ts := prepareTerms(parseQuery("detect "+typoPalette+" close events"), a, idf, correct)
	if ts[0].word != "detect" || ts[0].weight != 1 {
		t.Fatalf("soft word = %+v, want weight 1 (a third of 3)", ts[0])
	}
	if !ts[1].unknown || ts[1].corrected != "palette" {
		t.Fatalf("typo term = %+v", ts[1])
	}
	if got := correctedNeedle("detect "+typoPalette+" close events", ts); got != "detect palette close events" {
		t.Fatalf("corrected needle = %q", got)
	}
}

func TestEditDistanceHelpers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		one  bool
	}{
		{"palette", "palette", true},
		{typoPalette, "palete", true}, // deletion
		{"palette", "plaette", true},  // transposition
		{"palette", "palettes", true},
		{"palette", "palxtte", true},
		{"palette", "pxlxtte", false},
	} {
		if got := within1(c.a, c.b); got != c.one {
			t.Errorf("within1(%q,%q) = %v", c.a, c.b, got)
		}
	}
	if !withinEdits(typoConfiguration, "configuration", 2) || withinEdits("configuration", "xxnfiguraxxon", 2) {
		t.Fatal("withinEdits(…, 2) wrong")
	}
}

func TestVocabularyCorrectsToMostFrequentNeighbor(t *testing.T) {
	v := newVocabulary()
	v.addText("palette palette palette closePalette palace")
	if got := v.correct(typoPalette); got != "palette" {
		t.Fatalf("correct(%s) = %q", typoPalette, got)
	}
	if got := v.correct("zzzzzz"); got != "" {
		t.Fatalf("correct(zzzzzz) = %q", got)
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	back := newVocabulary()
	if err := back.UnmarshalJSON(raw); err != nil || back.correct(typoPalette) != "palette" {
		t.Fatalf("round trip lost vocabulary: err=%v", err)
	}
}

func TestMatchPathsRanksFileNamesFirst(t *testing.T) {
	paths := []string{
		"frontend/src/renderer/hooks/useCommandPaletteEnabled.ts",
		"frontend/src/renderer/components/CommandPalette.tsx",
		"frontend/src/renderer/components/CommandPalette.test.tsx",
		"frontend/src/renderer/lib/command-palette.ts",
		"frontend/src/renderer/stores/ui-store.ts",
		"frontend/src/styles/tokens.css",
		"cloud/internal/postgres/migrations/00015_session_idle_pause.sql",
		"backend/internal/config/config.go",
	}
	top := func(q string) (string, bool) {
		m, strong := matchPaths(q, paths, parseQuery(q), 5)
		if len(m) == 0 {
			return "", strong
		}
		return m[0].path, strong
	}
	for q, want := range map[string]string{
		"cmdpal":       "frontend/src/renderer/components/CommandPalette.tsx",
		"uiStore":      "frontend/src/renderer/stores/ui-store.ts",
		"tokens.css":   "frontend/src/styles/tokens.css",
		"cfggo":        "backend/internal/config/config.go",
		"palette test": "frontend/src/renderer/components/CommandPalette.test.tsx",
	} {
		if got, _ := top(q); got != want {
			t.Errorf("matchPaths(%q) top = %q, want %q", q, got, want)
		}
	}
	if _, strong := top("tokens.css"); !strong {
		t.Error("exact file name not strong")
	}
	if m, _ := matchPaths("zzqx", paths, parseQuery("zzqx"), 5); len(m) != 0 {
		t.Errorf("non-match returned %v", m)
	}
}

func TestBestSnippetPrefersLinesAnsweringMoreQueryWords(t *testing.T) {
	text := "func render(next PaletteView) {\n\tclosePalette(state)\n}"
	// "paletteview" answers one query word, "closepalette" answers two.
	s := bestSnippet(text, map[string]int{"paletteview": 1, "closepalette": 2}, 1)
	if s.Text != "closePalette(state)" || s.Line != 2 {
		t.Fatalf("snippet = %+v", s)
	}
}

func TestBestSnippetMarksMatchedWords(t *testing.T) {
	text := "package x\n\nfunc closePalette() {\n\treturn\n}"
	s := bestSnippet(text, map[string]int{"closepalette": 2}, 10)
	if s.Text != "func closePalette() {" || s.Line != 12 {
		t.Fatalf("snippet = %+v", s)
	}
	if len(s.Highlights) != 1 || s.Text[s.Highlights[0][0]:s.Highlights[0][1]] != "closePalette" {
		t.Fatalf("highlights = %v", s.Highlights)
	}
	long := strings.Repeat("x ", 300) + "needle " + strings.Repeat("y ", 300)
	clipped := bestSnippet(long, map[string]int{"needle": 1}, 1)
	if len(clipped.Text) > maxSnippetLen || len(clipped.Highlights) != 1 ||
		clipped.Text[clipped.Highlights[0][0]:clipped.Highlights[0][1]] != "needle" {
		t.Fatalf("clipped = %q %v", clipped.Text, clipped.Highlights)
	}
}

func TestSnippetHighlightsSurviveCaseFoldingThatChangesLength(t *testing.T) {
	// "İ" lowercases to a shorter byte sequence, so offsets into a
	// lowercased copy of the line would not fit the original.
	s := bestSnippet("visit İstanbul and Ankara", map[string]int{"istanbul": 1, "ankara": 1}, 1)
	var got []string
	for _, h := range s.Highlights {
		got = append(got, s.Text[h[0]:h[1]])
	}
	if !reflect.DeepEqual(got, []string{"İstanbul", "Ankara"}) {
		t.Fatalf("highlighted %q in %q", got, s.Text)
	}
}
