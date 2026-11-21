package citation

import (
	"strings"
	"testing"
)

func TestMarkdownRendersSharedSourcesAndUnavailableDestinations(t *testing.T) {
	raw := "First <one>. Second <two>. Third <one>."
	refs := []Reference{
		{Start: 6, End: 11, Sources: []Source{{ID: "one", URL: "https://example.com/a"}}},
		{Start: 20, End: 25, Sources: []Source{{ID: "two", URL: "javascript:alert(1)"}}},
		{Start: 33, End: 38, Sources: []Source{{ID: "one", URL: "https://example.com/a"}}},
	}
	got := Markdown(raw, refs)
	want := "First [1](<https://example.com/a>). Second [Source unavailable]. Third [1](<https://example.com/a>)."
	if got != want {
		t.Fatalf("Markdown() = %q, want %q", got, want)
	}
	if strings.Contains(got, "javascript:") {
		t.Fatal("unsafe URL leaked into rendered Markdown")
	}
}

func TestMarkdownCanInsertSeveralSourcesAfterOneSpan(t *testing.T) {
	got := Markdown("Claim.", []Reference{{Start: 6, End: 6, Sources: []Source{
		{ID: "a", URL: "https://example.com/a"},
		{ID: "b", URL: "https://example.com/b"},
	}}})
	if got != "Claim.[1](<https://example.com/a>), [2](<https://example.com/b>)" {
		t.Fatalf("Markdown() = %q", got)
	}
}
