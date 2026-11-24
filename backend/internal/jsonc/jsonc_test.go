package jsonc

import (
	"encoding/json"
	"testing"
)

func TestStandardizeReadsHarnessJSONC(t *testing.T) {
	for _, input := range []string{
		`{"theme":"dark"}`,
		`{"theme":"dark",} // note`,
		"// settings\r{\"theme\":\"dark\"}",
		"// settings\r\n{\"theme\":\"dark\"}\n",
		"/* a */ {\"theme\": /* b */ \"dark\"}\r",
	} {
		plain, err := Standardize([]byte(input))
		if err != nil {
			t.Fatalf("Standardize(%q): %v", input, err)
		}
		if len(plain) != len(input) {
			t.Fatalf("Standardize(%q) changed the length: %d, want %d", input, len(plain), len(input))
		}
		var got struct{ Theme string }
		if err := json.Unmarshal(plain, &got); err != nil || got.Theme != "dark" {
			t.Fatalf("Standardize(%q) = %q, %v; want theme dark", input, plain, err)
		}
	}
	if _, err := Standardize([]byte(`{"theme":}`)); err == nil {
		t.Fatal("Standardize accepted invalid JSON")
	}
}

func TestStandardizeLeavesItsInputAlone(t *testing.T) {
	input := []byte("// a\r{\"x\":1,}")
	want := string(input)
	if _, err := Standardize(input); err != nil {
		t.Fatal(err)
	}
	if string(input) != want {
		t.Fatalf("input changed to %q", input)
	}
}

func TestBlank(t *testing.T) {
	for input, want := range map[string]bool{
		"":                       true,
		"  \n\t":                 true,
		"// only a comment":      true,
		"/* a */ // b\r\n":       true,
		"// settings\r{\"x\":1}": false,
		"{}":                     false,
		"null":                   false,
		"{\"x\":1} // trailing":  false,
		"not json":               false,
	} {
		if got := Blank([]byte(input)); got != want {
			t.Errorf("Blank(%q) = %v, want %v", input, got, want)
		}
	}
}
