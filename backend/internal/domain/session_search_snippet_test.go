package domain

import (
	"strings"
	"testing"
)

func marked(snippet string) []string {
	var out []string
	for {
		start := strings.Index(snippet, SessionSearchSnippetStart)
		if start < 0 {
			return out
		}
		end := strings.Index(snippet[start:], SessionSearchSnippetEnd)
		out = append(out, snippet[start+1:start+end])
		snippet = snippet[start+end+1:]
	}
}

func TestSessionSearchSnippetMarksMatchesInTheBestWindow(t *testing.T) {
	text := strings.Repeat("filler words that do not match anything at all ", 20) +
		"the flaky login test fails when the token refresh races the cookie write " +
		strings.Repeat("more filler ", 20)
	snippet := SessionSearchSnippet(text, []SessionSearchHighlightTerm{{Text: "flaky"}, {Text: "login"}, {Text: "cookie", Prefix: true}})
	if got := marked(snippet); strings.Join(got, ",") != "flaky,login,cookie" {
		t.Fatalf("marked = %v in %q", got, snippet)
	}
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("a window inside a long text should say it is cut: %q", snippet)
	}
}

func TestSessionSearchSnippetFoldsCaseDiacriticsAndPrefixes(t *testing.T) {
	snippet := SessionSearchSnippet("Le Café ouvre. Testing TESTS tested", []SessionSearchHighlightTerm{{Text: "cafe"}, {Text: "test", Prefix: true}})
	if got := marked(snippet); strings.Join(got, ",") != "Café,Testing,TESTS,tested" {
		t.Fatalf("marked = %v in %q", got, snippet)
	}
}

func TestSessionSearchSnippetMarksIdentifiersByTheirParts(t *testing.T) {
	snippet := SessionSearchSnippet("call handleSubmit before parseHTTPResponse", []SessionSearchHighlightTerm{{Text: "submit"}, {Text: "http"}})
	if got := marked(snippet); strings.Join(got, ",") != "handleSubmit,parseHTTPResponse" {
		t.Fatalf("marked = %v in %q", got, snippet)
	}
}

func TestSessionSearchSnippetIgnoresTheIndexExpansion(t *testing.T) {
	body := SessionSearchSplitIdentifiers("renamed handleSubmit", 10)
	if !strings.Contains(body, SessionSearchExpansionMarker) {
		t.Fatalf("expansion marker missing from %q", body)
	}
	snippet := SessionSearchSnippet(body, []SessionSearchHighlightTerm{{Text: "submit"}})
	if strings.Contains(snippet, "handle submit") || strings.Contains(snippet, SessionSearchExpansionMarker) {
		t.Fatalf("snippet shows the index's expansion: %q", snippet)
	}
	if got := marked(snippet); len(got) != 1 || got[0] != "handleSubmit" {
		t.Fatalf("marked = %v in %q", got, snippet)
	}
}

func TestSessionSearchSnippetCollapsesWhitespaceAndStartsAtTheText(t *testing.T) {
	snippet := SessionSearchSnippet("\n\n  --- FAIL: TestLogin\n\t expected   retry", []SessionSearchHighlightTerm{{Text: "retry"}})
	if snippet != "--- FAIL: TestLogin expected "+SessionSearchSnippetStart+"retry"+SessionSearchSnippetEnd {
		t.Fatalf("snippet = %q", snippet)
	}
}

func TestSessionSearchSnippetWithoutMatchesShowsTheStart(t *testing.T) {
	snippet := SessionSearchSnippet("one two three", []SessionSearchHighlightTerm{{Text: "absent"}})
	if snippet != "one two three" {
		t.Fatalf("snippet = %q", snippet)
	}
	if SessionSearchSnippet("   ", nil) != "" {
		t.Fatal("blank text should give an empty snippet")
	}
}

func TestParseSessionSearchQueryCarriesHighlightTerms(t *testing.T) {
	parsed := ParseSessionSearchQuery(`how did we fix "the Flaky login" Tes`)
	var got []string
	for _, term := range parsed.HighlightTerms {
		label := term.Text
		if term.Prefix {
			label += "*"
		}
		got = append(got, label)
	}
	// The phrase marks each of its tokens (its stopwords too: a phrase means
	// them); plain words lose their stopwords; the last word is a prefix.
	if strings.Join(got, ",") != "the,flaky,login,fix,tes*" {
		t.Fatalf("highlight terms = %v", got)
	}
}
