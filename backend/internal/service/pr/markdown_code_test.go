package pr

import (
	"reflect"
	"testing"
)

// The app's markdown-code.test.ts runs the same cases against its copy.
func TestMarkdownCodeRanges(t *testing.T) {
	cases := []struct {
		name string
		text string
		code []string
	}{
		{"inline", "a `x` b", []string{"`x`"}},
		{"double backticks", "a ``x ` y`` b", []string{"``x ` y``"}},
		{"double backticks around a link", "``![a](attachment:0)``", []string{"``![a](attachment:0)``"}},
		{"a run with no partner is text", "unmatched `` here `x`", []string{"`x`"}},
		{"an escaped backtick is text", "\\`x` y", nil},
		{"a blank line ends the paragraph", "`a\n\nb`", nil},
		{"fenced block", "```md\n![a](attachment:0)\n```\nafter", []string{"```md\n![a](attachment:0)\n```\n"}},
		{"tilde fence closed by a longer one", "~~~\ncode\n~~~~\n", []string{"~~~\ncode\n~~~~\n"}},
		{"unclosed fence runs to the end", "```\ncode", []string{"```\ncode"}},
		{"indented fence", "- item\n  ```\n  x\n  ```\n", []string{"  ```\n  x\n  ```\n"}},
		{"a backtick in the info string is not a fence", "``` a`b\nx", nil},
		{"no code", "plain ![a](attachment:0)", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, r := range markdownCodeRanges(tc.text) {
				got = append(got, tc.text[r[0]:r[1]])
			}
			if !reflect.DeepEqual(got, tc.code) {
				t.Fatalf("markdownCodeRanges(%q) = %q, want %q", tc.text, got, tc.code)
			}
		})
	}
}
