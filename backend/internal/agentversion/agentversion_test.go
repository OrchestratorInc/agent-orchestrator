package agentversion

import "testing"

func TestFind(t *testing.T) {
	for input, want := range map[string]string{
		"kimchi 0.0.7":            "0.0.7",
		"kimchi version 1.2.3":    "1.2.3",
		"omp/15.0.0":              "15.0.0",
		"codex-cli 0.130.0\n":     "0.130.0",
		"  0.1.0-rc1 ":            "0.1.0",
		"Qwen Code 0.16.0 (beta)": "0.16.0",
	} {
		got, ok := Find(input)
		if !ok || got.String() != want {
			t.Errorf("Find(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "kimchi", "no version here", "0.0", "v1"} {
		if got, ok := Find(input); ok {
			t.Errorf("Find(%q) = %q, want no version", input, got)
		}
	}
}

func TestParse(t *testing.T) {
	for input, want := range map[string]string{
		"1.2.3":            "1.2.3",
		"v1.2.3":           "1.2.3",
		" 1.2.3 ":          "1.2.3",
		"1.2.3-beta.1":     "1.2.3",
		"1.2.3+build.5":    "1.2.3",
		"1.5.4 (a1b2c3)":   "1.5.4",
		"0.1.0-rc1+abcdef": "0.1.0",
	} {
		got, ok := Parse(input)
		if !ok || got.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "1.2", "1.2.3.4", "version 1.2.3", "x1.2.3", "1.2.x"} {
		if got, ok := Parse(input); ok {
			t.Errorf("Parse(%q) = %q, want no version", input, got)
		}
	}
}

func TestLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.0.6", "0.0.7", true},
		{"0.0.7", "0.0.7", false},
		{"0.1.0", "0.0.7", false},
		{"0.0.7", "0.1.0", true},
		{"1.0.0", "0.99.99", false},
		{"0.9.0", "0.10.0", true},
		// Suffixes are ignored, so a pre-release meets its own minimum.
		{"1.5.4-rc1", "1.5.4", false},
	} {
		if got := MustParse(tc.a).Less(MustParse(tc.b)); got != tc.want {
			t.Errorf("%s < %s = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMustParsePanicsOnABadConstant(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse did not panic")
		}
	}()
	MustParse("1.2")
}
