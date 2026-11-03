// Run from backend/: go run ../scripts/generate-chat-find-fold.go
// Keeps renderer case folding aligned with the daemon's pinned Unicode tables.
package main

import (
	"encoding/json"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
)

func main() {
	values := map[string]string{}
	folder := cases.Fold()
	for r := rune(0); r <= unicode.MaxRune; r++ {
		value := string(r)
		if folded := folder.String(value); folded != strings.ToLower(value) {
			values[value] = folded
		}
	}
	file, err := os.Create("../frontend/src/renderer/lib/chat-find-fold.json")
	if err != nil {
		panic(err)
	}
	defer func() { _ = file.Close() }()
	if err := json.NewEncoder(file).Encode(values); err != nil {
		panic(err)
	}
}
