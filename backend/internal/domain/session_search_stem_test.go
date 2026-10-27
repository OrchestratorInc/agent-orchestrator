package domain

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

func TestPorterStemKnownWords(t *testing.T) {
	// Pairs taken from Porter's published vocabulary/output files, chosen to
	// cover every step of the algorithm and the words session search cares about.
	cases := map[string]string{
		"caresses": "caress", "ponies": "poni", "ties": "ti", "caress": "caress", "cats": "cat",
		//nolint:misspell // Porter stems are not words.
		"feed": "feed", "agreed": "agre", "plastered": "plaster", "bled": "bled", "motoring": "motor",
		"sing": "sing", "conflated": "conflat", "troubled": "troubl", "sized": "size", "hopping": "hop",
		"tanned": "tan", "falling": "fall", "hissing": "hiss", "fizzed": "fizz", "failing": "fail",
		"filing": "file", "happy": "happi", "sky": "sky", "relational": "relat", "conditional": "condit",
		"rational": "ration", "valenci": "valenc", "digitizer": "digit", "conformabli": "conform",
		"radicalli": "radic", "differentli": "differ", "vileli": "vile", "analogousli": "analog",
		"vietnamization": "vietnam", "predication": "predic", "operator": "oper", "feudalism": "feudal",
		"decisiveness": "decis", "hopefulness": "hope", "callousness": "callous", "formaliti": "formal",
		"sensitiviti": "sensit", "sensibiliti": "sensibl", "triplicate": "triplic", "formative": "form",
		"formalize": "formal", "electriciti": "electr", "electrical": "electr", "hopeful": "hope",
		"goodness": "good", "revival": "reviv", "allowance": "allow", "inference": "infer",
		"airliner": "airlin", "gyroscopic": "gyroscop", "adjustable": "adjust", "defensible": "defens",
		"irritant": "irrit", "replacement": "replac", "adjustment": "adjust", "dependent": "depend",
		"adoption": "adopt", "homologou": "homolog", "communism": "commun", "activate": "activ",
		"angulariti": "angular", "homologous": "homolog", "effective": "effect", "bowdlerize": "bowdler",
		//nolint:misspell // Porter stems are not words.
		"probate": "probat", "rate": "rate", "cease": "ceas", "controll": "control", "roll": "roll",
		"signatures": "signatur", "signature": "signatur", "verified": "verifi", "verification": "verif",
		"migrations": "migrat", "migration": "migrat", "failed": "fail", "tests": "test",
		// Not all a-z: returned unchanged.
		"http2": "http2", "SQLITE": "SQLITE", "go": "go",
	}
	for word, want := range cases {
		if got := PorterStem(word); got != want {
			t.Errorf("PorterStem(%q) = %q, want %q", word, got, want)
		}
	}
}

// TestPorterStemReferenceVocabulary checks every word of Porter's official test
// vocabulary against his official output. Set AO_PORTER_VOCAB_DIR to a folder
// holding voc.txt and output.txt from https://tartarus.org/martin/PorterStemmer/.
func TestPorterStemReferenceVocabulary(t *testing.T) {
	dir := os.Getenv("AO_PORTER_VOCAB_DIR")
	if dir == "" {
		t.Skip("set AO_PORTER_VOCAB_DIR to a folder with Porter's voc.txt and output.txt")
	}
	words, err := os.Open(filepath.Join(dir, "voc.txt"))
	if err != nil {
		t.Fatalf("open voc.txt: %v", err)
	}
	defer func() { _ = words.Close() }()
	stems, err := os.Open(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("open output.txt: %v", err)
	}
	defer func() { _ = stems.Close() }()
	wordLines, stemLines := bufio.NewScanner(words), bufio.NewScanner(stems)
	total, wrong := 0, 0
	for wordLines.Scan() {
		if !stemLines.Scan() {
			t.Fatal("output.txt is shorter than voc.txt")
		}
		total++
		if got := PorterStem(wordLines.Text()); got != stemLines.Text() {
			wrong++
			if wrong <= 20 {
				t.Errorf("PorterStem(%q) = %q, want %q", wordLines.Text(), got, stemLines.Text())
			}
		}
	}
	t.Logf("%d words, %d mismatches", total, wrong)
	if wrong > 0 {
		t.Fatalf("%d of %d reference words stem differently", wrong, total)
	}
}
