package approval

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type corpusEntry struct {
	Category string `json:"category"`
	Target   Target `json:"target"`
}

func loadCorpus(t *testing.T) (risky, benign []corpusEntry) {
	t.Helper()
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Risky  []corpusEntry `json:"risky"`
		Benign []corpusEntry `json:"benign"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	return corpus.Risky, corpus.Benign
}

func TestClassifyRiskyCorpus(t *testing.T) {
	risky, _ := loadCorpus(t)
	for _, entry := range risky {
		got := Classify(entry.Target, ModeRisky)
		if !got.NeedsApproval {
			t.Errorf("%s %q: not flagged", entry.Target.Action, entry.Target.Name+entry.Target.Instruction)
			continue
		}
		if got.Category != entry.Category {
			t.Errorf("%s %q: category %q, want %q", entry.Target.Action, entry.Target.Name+entry.Target.Instruction, got.Category, entry.Category)
		}
		if got.Reason == "" || got.Summary == "" {
			t.Errorf("%q: missing reason or summary: %+v", entry.Target.Name, got)
		}
	}
}

func TestClassifyBenignCorpus(t *testing.T) {
	_, benign := loadCorpus(t)
	for _, entry := range benign {
		if got := Classify(entry.Target, ModeRisky); got.NeedsApproval {
			t.Errorf("%s %q flagged as %s: %s", entry.Target.Action, entry.Target.Name+entry.Target.Instruction, got.Category, got.Reason)
		}
	}
}

func TestClassifyModeOffNeverAsks(t *testing.T) {
	risky, _ := loadCorpus(t)
	for _, entry := range risky {
		if got := Classify(entry.Target, ModeOff); got.NeedsApproval {
			t.Fatalf("mode off flagged %q", entry.Target.Name)
		}
	}
}

func TestClassifyEverySubmitAsksForPlainSubmit(t *testing.T) {
	target := Target{Action: "click", Role: "button", Name: "Next", IsSubmit: true, FormMethod: "post", PageURL: "https://app.example/wizard"}
	if got := Classify(target, ModeRisky); got.NeedsApproval {
		t.Fatalf("risky mode flagged a plain submit: %+v", got)
	}
	got := Classify(target, ModeEverySubmit)
	if !got.NeedsApproval || got.Category != CategorySubmit {
		t.Fatalf("every-submit mode did not ask: %+v", got)
	}
	// A search form still submits a form, so every-submit asks for it too.
	search := Target{Action: "press", Key: "Enter", Role: "searchbox", IsSubmit: true, FormMethod: "get"}
	if got := Classify(search, ModeEverySubmit); !got.NeedsApproval {
		t.Fatalf("every-submit mode skipped a GET submit")
	}
}

func TestClassifyExplainsWhy(t *testing.T) {
	got := Classify(Target{
		Action: "click", Role: "button", Name: "Continue", IsSubmit: true, FormMethod: "post",
		FormHasPayment: true, PageURL: "https://shop.example/checkout",
	}, ModeRisky)
	if !strings.Contains(got.Reason, "card") {
		t.Fatalf("reason should mention the payment fields: %q", got.Reason)
	}
	if got.Summary != `Click "Continue" on shop.example` {
		t.Fatalf("summary = %q", got.Summary)
	}
}

func TestClassifyIgnoresNonActingActions(t *testing.T) {
	for _, action := range []string{"snapshot", "hover", "fill", "type", "scroll", "get", "screenshot", "open"} {
		if got := Classify(Target{Action: action, Role: "button", Name: "Delete"}, ModeEverySubmit); got.NeedsApproval {
			t.Fatalf("%s flagged", action)
		}
	}
}

// TestClassifierFalsePositiveRate reports the benign-corpus false-positive
// rate; the PR lists it. The bar is zero on the fixture corpus.
func TestClassifierFalsePositiveRate(t *testing.T) {
	_, benign := loadCorpus(t)
	flagged := 0
	for _, entry := range benign {
		if Classify(entry.Target, ModeRisky).NeedsApproval {
			flagged++
		}
	}
	t.Logf("benign fixture corpus: %d/%d flagged", flagged, len(benign))
	if flagged != 0 {
		t.Fatalf("false positives: %d", flagged)
	}
}
