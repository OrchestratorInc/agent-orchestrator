package domain

import "testing"

func TestStemRootWidensInflectedForms(t *testing.T) {
	cases := []struct {
		word string
		root string
		ok   bool
	}{
		{"rebasing", "rebas", true},
		{"failing", "fail", true},
		{"verified", "verif", true}, // the stemmer's "i" from "y" is dropped, so "verify" matches too
		{"signatures", "signatur", true},
		{"fail", "", false}, // already its own stem: nothing to widen
		{"runs", "", false}, // a three-letter root ("run") would also match "runtime"
	}
	for _, tc := range cases {
		root, ok := stemRoot(tc.word)
		if root != tc.root || ok != tc.ok {
			t.Errorf("stemRoot(%q) = %q, %v; want %q, %v", tc.word, root, ok, tc.root, tc.ok)
		}
	}
}
