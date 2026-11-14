// Package approval pauses consequential agent actions until the user allows
// or denies them, and records every gated action in an audit log.
//
// Classify decides whether a browser action needs approval from the element
// and page it targets. Gate holds a request open until the user answers it in
// the app or it times out (a timeout is a deny). Other features, such as the
// credential vault's "first use of a saved login on this site", call
// Gate.Request directly with their own Kind and Category.
package approval

import (
	"fmt"
	"net/url"
	"strings"
)

// Mode is how much a session asks before acting.
type Mode string

const (
	// ModeOff never asks.
	ModeOff Mode = "off"
	// ModeRisky asks for payments, messages, posts, deletes, security and
	// account changes. It is the default.
	ModeRisky Mode = "risky"
	// ModeEverySubmit also asks for every form submission.
	ModeEverySubmit Mode = "every-submit"
)

// ValidMode reports whether m is a known mode.
func ValidMode(m Mode) bool {
	return m == ModeOff || m == ModeRisky || m == ModeEverySubmit
}

// Categories of consequential action. "Always allow on this site" is
// remembered per site and category, so allowing messages on a mail site does
// not allow payments there.
const (
	CategoryPayment    = "payment"
	CategoryMessage    = "message"
	CategoryPublish    = "publish"
	CategoryDelete     = "delete"
	CategorySecurity   = "security"
	CategoryAccount    = "account"
	CategorySubmit     = "submit"
	CategoryCredential = "credential"
)

var categoryNouns = map[string]string{
	CategoryPayment:  "a payment or purchase",
	CategoryMessage:  "sending a message",
	CategoryPublish:  "posting or publishing",
	CategoryDelete:   "deleting or removing something",
	CategorySecurity: "a security setting change",
	CategoryAccount:  "an account change",
}

// Target describes what a browser action would act on. The runtime's
// describe step fills it from the live page; fields it cannot read stay empty.
type Target struct {
	Action      string `json:"action"`
	Key         string `json:"key,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Role        string `json:"role,omitempty"`
	Name        string `json:"name,omitempty"`
	Tag         string `json:"tag,omitempty"`
	InputType   string `json:"inputType,omitempty"`
	Href        string `json:"href,omitempty"`
	// IsSubmit means the action submits a form (a submit control, or Enter in
	// a form field).
	IsSubmit        bool   `json:"isSubmit,omitempty"`
	FormAction      string `json:"formAction,omitempty"`
	FormMethod      string `json:"formMethod,omitempty"`
	FormHasPayment  bool   `json:"formHasPayment,omitempty"`
	FormHasPassword bool   `json:"formHasPassword,omitempty"`
	// SubmitName is the label of the form's submit control, used when Enter
	// submits a form from a text field.
	SubmitName string `json:"submitName,omitempty"`
	PageURL    string `json:"pageUrl,omitempty"`
	PageTitle  string `json:"pageTitle,omitempty"`
}

// Verdict is the explainable result of Classify.
type Verdict struct {
	NeedsApproval bool   `json:"needsApproval"`
	Category      string `json:"category,omitempty"`
	// Summary says what will happen, e.g. `Click "Place order" on shop.example`.
	Summary string `json:"summary,omitempty"`
	// Reason says why it was flagged.
	Reason string `json:"reason,omitempty"`
}

// GatedActions are the browser actions Classify can flag. Everything else
// (reading, typing, scrolling, navigation by URL) never asks.
var GatedActions = map[string]struct{}{
	"click": {}, "dblclick": {}, "press": {}, "act": {}, "check": {}, "uncheck": {}, "select": {},
}

type phraseSet struct {
	category string
	phrases  []string
}

// Ordered: the first matching category wins ("send money" is a payment, not
// a message; "delete account" is a delete).
var phraseSets = []phraseSet{
	{CategorySecurity, []string{
		"change password", "reset password", "update password", "two factor", "2fa", "mfa",
		"new token", "generate token", "regenerate", "revoke", "api key", "ssh key", "security key",
		"recovery codes", "sign out everywhere", "sign out of all",
	}},
	{CategoryPayment, []string{
		"pay", "pay now", "place order", "place your order", "submit order", "confirm order", "checkout",
		"check out", "buy", "buy now", "purchase", "complete purchase", "subscribe", "donate",
		"transfer", "send money", "confirm payment", "make payment", "book now", "rent now",
	}},
	{CategoryDelete, []string{
		"delete", "remove", "destroy", "erase", "empty trash", "permanently", "cancel subscription",
		"cancel order", "deactivate", "close account", "wipe", "discard",
	}},
	{CategoryMessage, []string{
		"send", "send message", "send email", "reply", "reply all", "forward", "send invite", "invite",
	}},
	{CategoryPublish, []string{
		"post", "publish", "tweet", "retweet", "repost", "comment", "submit comment", "go live",
		"merge", "merge pull request", "deploy", "release",
	}},
}

var accountPhrases = []string{
	"save changes", "update email", "change email", "update profile", "update account",
	"change username", "update settings",
}

// Never consequential on their own, even inside a phrase above.
var benignPhrases = []string{
	"add to cart", "add to bag", "view order", "how to", "learn more", "read more", "sign in",
	"log in", "login", "search",
}

// Labels that open an editor or composer rather than act: "Create post",
// "New message", "Compose".
var composerPrefixes = []string{"create", "new", "write", "compose", "start", "draft"}

// Text-field names whose Enter usually sends (chat boxes rarely use forms).
var messageFieldPhrases = []string{"message", "reply", "write a comment", "add a comment", "what s happening", "tweet", "chat"}

// Roles whose name is a label for a value, not an action.
var valueRoles = map[string]struct{}{
	"tab": {}, "checkbox": {}, "radio": {}, "combobox": {}, "listbox": {}, "option": {},
	"textbox": {}, "searchbox": {}, "slider": {}, "spinbutton": {}, "heading": {}, "switch": {},
}

// Classify decides whether target needs approval under mode.
func Classify(target Target, mode Mode) Verdict {
	action := strings.ToLower(strings.TrimSpace(target.Action))
	if mode == ModeOff {
		return Verdict{}
	}
	if _, ok := GatedActions[action]; !ok {
		return Verdict{}
	}
	role := strings.ToLower(strings.TrimSpace(target.Role))
	name := normalize(target.Name)
	if action == "act" && name == "" {
		name, role = instructionTarget(target.Instruction)
	}
	verdict := classifyRisk(action, role, name, target)
	if !verdict.NeedsApproval && mode == ModeEverySubmit && target.IsSubmit {
		verdict = Verdict{NeedsApproval: true, Category: CategorySubmit, Reason: "This submits a form, and this session asks before every form submission."}
	}
	if verdict.NeedsApproval {
		verdict.Summary = summarize(action, target)
	}
	return verdict
}

func classifyRisk(action, role, name string, target Target) Verdict {
	if action == "press" {
		if !strings.EqualFold(strings.TrimSpace(target.Key), "enter") {
			return Verdict{}
		}
		if role == "button" || role == "link" || role == "menuitem" {
			// Enter on a focused control activates it, exactly like a click.
			return classifyRisk("click", role, name, target)
		}
		if target.IsSubmit {
			return classifySubmit(normalize(target.SubmitName), target, true)
		}
		if role == "textbox" && containsAny(name, messageFieldPhrases) {
			return Verdict{NeedsApproval: true, Category: CategoryMessage,
				Reason: fmt.Sprintf("Pressing Enter in the %q box usually sends what is typed there.", target.Name)}
		}
		return Verdict{}
	}
	if action == "check" || action == "uncheck" || action == "select" {
		// Toggling a value is not consequential until a form is submitted.
		return Verdict{}
	}
	if _, ok := valueRoles[role]; ok {
		return Verdict{}
	}
	if name != "" && name != "cancel" && !containsAny(name, benignPhrases) && !opensComposer(name) {
		linkish := role == "link"
		for _, set := range phraseSets {
			// A link labelled with one word is navigation ("Checkout",
			// "POST") unless that word destroys or changes security.
			if linkish && set.category != CategoryDelete && set.category != CategorySecurity && !strings.Contains(name, " ") {
				continue
			}
			if phrase := matchPhrase(name, set.phrases, linkish); phrase != "" {
				return Verdict{NeedsApproval: true, Category: set.category, Reason: labelReason(target, role, set.category)}
			}
		}
	}
	if target.IsSubmit {
		return classifySubmit(name, target, false)
	}
	return Verdict{}
}

// classifySubmit judges a form submission by its form and submit label.
func classifySubmit(submitName string, target Target, pressed bool) Verdict {
	if containsAny(submitName, []string{"search", "filter", "sign in", "log in", "login"}) {
		return Verdict{}
	}
	if target.FormHasPayment {
		return Verdict{NeedsApproval: true, Category: CategoryPayment,
			Reason: "It submits a form with payment card fields."}
	}
	if submitName != "" && !containsAny(submitName, benignPhrases) {
		for _, set := range phraseSets {
			if phrase := matchPhrase(submitName, set.phrases, false); phrase != "" {
				label := target.SubmitName
				if label == "" {
					label = target.Name
				}
				return Verdict{NeedsApproval: true, Category: set.category,
					Reason: fmt.Sprintf("It submits a form whose button %q reads like %s.", label, categoryNouns[set.category])}
			}
		}
	}
	if pressed {
		return Verdict{}
	}
	post := strings.EqualFold(target.FormMethod, "post")
	if post && (target.FormHasPassword || containsAny(submitName, accountPhrases)) {
		reason := "It submits a form that changes account details."
		if target.FormHasPassword {
			reason = "It submits a form with a password field that changes account details."
		}
		return Verdict{NeedsApproval: true, Category: CategoryAccount, Reason: reason}
	}
	return Verdict{}
}

func labelReason(target Target, role, category string) string {
	kind := "button"
	switch role {
	case "link":
		kind = "link"
	case "menuitem":
		kind = "menu item"
	}
	reason := fmt.Sprintf("The %s %q reads like %s.", kind, displayName(target), categoryNouns[category])
	if target.FormHasPayment {
		reason += " Its form has payment card fields."
	}
	return reason
}

func displayName(target Target) string {
	if strings.TrimSpace(target.Name) != "" {
		return strings.TrimSpace(target.Name)
	}
	return strings.TrimSpace(target.Instruction)
}

// matchPhrase returns the phrase found in name. Links only match when the
// whole label is the phrase: "Delete" is an action, "Pay bills" is navigation.
// Long labels (sentences, headlines) only match at their start.
func matchPhrase(name string, phrases []string, whole bool) string {
	words := strings.Fields(name)
	for _, phrase := range phrases {
		switch {
		case whole:
			if name == phrase {
				return phrase
			}
		case len(words) > 5:
			if strings.HasPrefix(name+" ", phrase+" ") {
				return phrase
			}
		default:
			if strings.Contains(" "+name+" ", " "+phrase+" ") {
				return phrase
			}
		}
	}
	return ""
}

func opensComposer(name string) bool {
	first, _, _ := strings.Cut(name, " ")
	for _, prefix := range composerPrefixes {
		if first == prefix {
			return true
		}
	}
	return false
}

func containsAny(name string, phrases []string) bool {
	for _, phrase := range phrases {
		if name != "" && strings.Contains(" "+name+" ", " "+phrase+" ") {
			return true
		}
	}
	return false
}

// instructionTarget extracts the element words from an act instruction:
// "click the place order button" -> ("place order", "button").
func instructionTarget(instruction string) (string, string) {
	words := strings.Fields(normalize(instruction))
	if len(words) > 0 {
		switch words[0] {
		case "click", "tap", "press", "hit", "open", "select", "choose", "dblclick":
			words = words[1:]
		}
	}
	filtered := words[:0]
	for _, word := range words {
		if word != "the" && word != "a" && word != "an" && word != "on" {
			filtered = append(filtered, word)
		}
	}
	role := ""
	if n := len(filtered); n > 0 {
		switch filtered[n-1] {
		case "button", "link", "tab", "checkbox", "menuitem":
			role = filtered[n-1]
			filtered = filtered[:n-1]
		}
	}
	return strings.Join(filtered, " "), role
}

// normalize lowercases and turns punctuation into single spaces, so
// "Pay $42.10" becomes "pay 42 10" and "two-factor" becomes "two factor".
func normalize(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func summarize(action string, target Target) string {
	label := strings.TrimSpace(target.Name)
	var text string
	switch action {
	case "dblclick":
		text = fmt.Sprintf("Double-click %q", label)
	case "press":
		text = fmt.Sprintf("Press Enter in %q", label)
		if label == "" {
			text = "Press Enter"
		}
	case "act":
		if label == "" {
			text = fmt.Sprintf("Act: %q", strings.TrimSpace(target.Instruction))
		} else {
			text = fmt.Sprintf("Click %q", label)
		}
	default:
		text = fmt.Sprintf("Click %q", label)
	}
	if host := Site(target.PageURL); host != "" {
		text += " on " + strings.TrimPrefix(host, "www.")
	}
	return text
}

// Site returns the host a page URL belongs to, the key for per-site rules.
func Site(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
