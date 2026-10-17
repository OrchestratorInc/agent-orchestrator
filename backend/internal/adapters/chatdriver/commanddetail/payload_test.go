package commanddetail

import "testing"

func TestHead(t *testing.T) {
	for _, tt := range []struct {
		text  string
		limit int
		want  string
	}{
		{"abc", 3, "abc"},
		{"abcdef", 2, "ab"},
		{"aéb", 2, "a"},
		{"aéb", 3, "aé"},
	} {
		if got := Head(tt.text, tt.limit); got != tt.want {
			t.Fatalf("Head(%q, %d) = %q, want %q", tt.text, tt.limit, got, tt.want)
		}
	}
}

func TestTailChars(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
		cut   bool
	}{
		{name: "empty", text: "", limit: 3, want: ""},
		{name: "fits", text: "abc", limit: 3, want: "abc"},
		{name: "keeps the end", text: "abcdef", limit: 2, want: "ef", cut: true},
		{name: "counts characters, not bytes", text: "aéé", limit: 2, want: "éé", cut: true},
		{name: "more bytes than the limit but no more characters", text: "éé", limit: 2, want: "éé"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := TailChars(tt.text, tt.limit)
			if got != tt.want || cut != tt.cut {
				t.Fatalf("TailChars(%q, %d) = %q, %v; want %q, %v", tt.text, tt.limit, got, cut, tt.want, tt.cut)
			}
		})
	}
}
