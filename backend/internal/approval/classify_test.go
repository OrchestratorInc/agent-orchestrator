package approval

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestOrdinaryPagesFalsePositiveRate classifies every button, link and menu
// item captured from ordinary pages (news, docs, code hosting, government) as
// a click. The PR lists the measured rate.
func TestOrdinaryPagesFalsePositiveRate(t *testing.T) {
	raw, err := os.ReadFile("testdata/ordinary-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Pages []struct {
			Page     string      `json:"page"`
			Elements [][2]string `json:"elements"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	total, flagged := 0, 0
	for _, page := range fixture.Pages {
		for _, el := range page.Elements {
			total++
			if v := Classify(Target{Action: "click", Role: el[0], Name: el[1], PageURL: "https://example.test/"}, ModeRisky); v.NeedsApproval {
				flagged++
				t.Logf("%s: %s %q -> %s", page.Page, el[0], el[1], v.Category)
			}
		}
	}
	t.Logf("ordinary pages: %d/%d flagged (%.2f%%)", flagged, total, 100*float64(flagged)/float64(total))
	// One known ask: MDN's newsletter "Subscribe!" button.
	if flagged > 1 {
		t.Fatalf("false positives on ordinary pages: %d/%d", flagged, total)
	}
}

func TestClassifyEnterFallsBackToMessageFieldWhenSubmitIsUnrecognized(t *testing.T) {
	got := Classify(Target{Action: "press", Key: "Enter", Role: "textbox", Name: "Message", IsSubmit: true, FormMethod: "post", PageURL: "https://chat.example/"}, ModeRisky)
	if !got.NeedsApproval || got.Category != CategoryMessage {
		t.Fatalf("verdict = %+v", got)
	}
}

func TestClassifyEnterMatchesClickOnAccountForm(t *testing.T) {
	form := Target{Role: "button", Name: "Save changes", IsSubmit: true, FormMethod: "post", FormHasPassword: true, SubmitName: "Save changes", PageURL: "https://app.example/profile"}
	click := form
	click.Action = "click"
	enter := form
	enter.Action, enter.Key, enter.Role, enter.Name = "press", "Enter", "textbox", "Display name"
	c, e := Classify(click, ModeRisky), Classify(enter, ModeRisky)
	if !c.NeedsApproval || !e.NeedsApproval || c.Category != CategoryAccount || e.Category != CategoryAccount {
		t.Fatalf("click = %+v, enter = %+v", c, e)
	}
}

func TestClassifyUnicodeSpacesMatchLikeASCII(t *testing.T) {
	for _, name := range []string{"Pay 42.10", "Place order", "Delete　repository"} {
		if got := Classify(Target{Action: "click", Role: "button", Name: name}, ModeRisky); !got.NeedsApproval {
			t.Errorf("%q not flagged", name)
		}
	}
	// Non-Latin labels keep their letters.
	if normalize("Оплатить заказ") != "оплатить заказ" {
		t.Fatalf("normalize = %q", normalize("Оплатить заказ"))
	}
}

func TestIsEnterKey(t *testing.T) {
	for key, want := range map[string]bool{
		"Enter": true, "enter": true, "Return": true, "Control+Enter": true, "Shift+Enter": true, "Enter+": true,
		" Meta + Return ": true, "Tab": false, "Enter+a": false, "": false,
	} {
		if IsEnterKey(key) != want {
			t.Errorf("IsEnterKey(%q) != %v", key, want)
		}
	}
	if got := Classify(Target{Action: "press", Key: "Control+Enter", Role: "button", Name: "Send"}, ModeRisky); !got.NeedsApproval {
		t.Fatal("chorded Enter not gated")
	}
}

func TestPaymentFormEvidenceBeatsTheButtonLabel(t *testing.T) {
	for _, name := range []string{"Send", "Search", "Continue", "Log in"} {
		form := Target{Role: "button", Name: name, IsSubmit: true, FormMethod: "post", FormHasPayment: true, SubmitName: name, PageURL: "https://shop.example/"}
		click := form
		click.Action = "click"
		enter := form
		enter.Action, enter.Key, enter.Role, enter.Name = "press", "Enter", "textbox", "Card number"
		for _, target := range []Target{click, enter} {
			if got := Classify(target, ModeRisky); !got.NeedsApproval || got.Category != CategoryPayment {
				t.Errorf("%s %q in a payment form = %+v", target.Action, name, got)
			}
		}
	}
	// An ordinary search form stays exempt.
	if got := Classify(Target{Action: "click", Role: "button", Name: "Search", IsSubmit: true, FormMethod: "get"}, ModeRisky); got.NeedsApproval {
		t.Fatalf("search form flagged: %+v", got)
	}
}

func TestActivationKeysActivateFocusedControls(t *testing.T) {
	for key, want := range map[string]bool{
		"Enter": true, "NumpadEnter": true, "Space": true, " ": true, "Spacebar": true, "Shift+Space": true,
		"Return": true, "Tab": false, "a": false, "": false,
	} {
		if IsActivationKey(key) != want {
			t.Errorf("IsActivationKey(%q) != %v", key, want)
		}
	}
	for _, key := range []string{"Space", "NumpadEnter", " "} {
		for _, name := range []string{"Place order", "Delete repository"} {
			if got := Classify(Target{Action: "press", Key: key, Role: "button", Name: name}, ModeRisky); !got.NeedsApproval {
				t.Errorf("%q on %q not gated", key, name)
			}
		}
		submit := Classify(Target{Action: "press", Key: key, Role: "button", Name: "Continue", IsSubmit: true, FormMethod: "post"}, ModeEverySubmit)
		if !submit.NeedsApproval {
			t.Errorf("%q on a submit button not gated in every-submit mode", key)
		}
	}
	// Space types a character in a text field; it does not submit.
	if got := Classify(Target{Action: "press", Key: "Space", Role: "textbox", Name: "Message", IsSubmit: true, FormMethod: "post"}, ModeEverySubmit); got.NeedsApproval {
		t.Fatalf("space in a text field gated: %+v", got)
	}
	if got := Classify(Target{Action: "press", Key: "Space", Role: "button", Name: "Place order"}, ModeRisky); got.Summary != `Press Space on "Place order"` {
		t.Fatalf("summary = %q", got.Summary)
	}
}

func TestSiteIsTheOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://shop.example/checkout": "https://shop.example",
		"https://Shop.Example:443/x":    "https://shop.example",
		"http://shop.example":           "http://shop.example",
		"HTTP://shop.example:80/":       "http://shop.example",
		"http://localhost:4173/pay":     "http://localhost:4173",
		"http://localhost:5173/pay":     "http://localhost:5173",
		"https://[::1]:8443/":           "https://[::1]:8443",
		"about:blank":                   "",
		"file:///Users/me/page.html":    "",
		"":                              "",
	} {
		if got := Site(raw); got != want {
			t.Errorf("Site(%q) = %q, want %q", raw, got, want)
		}
	}
	// Prompts still name the host alone.
	if got := Classify(Target{Action: "click", Role: "button", Name: "Pay now", PageURL: "http://localhost:4173/"}, ModeRisky); got.Summary != `Click "Pay now" on localhost` {
		t.Fatalf("summary = %q", got.Summary)
	}
}

func TestNewPasswordFormAsksEvenBehindASignInLabel(t *testing.T) {
	setsPassword := Target{Role: "button", Name: "Sign in", IsSubmit: true, FormMethod: "post",
		FormHasPassword: true, FormHasNewPassword: true, SubmitName: "Sign in", PageURL: "https://app.example/"}
	click := setsPassword
	click.Action = "click"
	enter := setsPassword
	enter.Action, enter.Key, enter.Role, enter.Name = "press", "Enter", "textbox", "Password"
	for _, target := range []Target{click, enter} {
		if got := Classify(target, ModeRisky); !got.NeedsApproval || got.Category != CategoryAccount {
			t.Errorf("%s: verdict = %+v", target.Action, got)
		}
	}
	// An ordinary sign-in form (a password, no new one) stays quiet.
	signIn := click
	signIn.FormHasNewPassword = false
	if got := Classify(signIn, ModeRisky); got.NeedsApproval {
		t.Fatalf("plain sign-in asked: %+v", got)
	}
}

func TestTruncateCountsCharactersNotBytes(t *testing.T) {
	shown, cut := Truncate(strings.Repeat("é", 130), 120)
	if !utf8.ValidString(shown) || cut != 10 || strings.Count(shown, "é") != 120 {
		t.Fatalf("shown %q cut %d", shown, cut)
	}
	if shown, cut := Truncate("short", 120); shown != "short" || cut != 0 {
		t.Fatalf("shown %q cut %d", shown, cut)
	}
	if got := ClassifyDialog("confirm", strings.Repeat("削除", 100), ModeRisky); !utf8.ValidString(got.Summary) {
		t.Fatalf("dialog summary split a character: %q", got.Summary)
	}
}
