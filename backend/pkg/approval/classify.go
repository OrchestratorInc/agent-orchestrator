// Package approval holds the rules that decide which agent browser actions
// need the user's approval: Classify, its modes, categories and request kinds.
//
// It is public so every gate applies the exact same rules: the local
// daemon's gate (backend/internal/approval) and AO Cloud's gate for cloud
// agent browsers both import it. It has no state and no I/O.
package approval

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Request kinds. KindSavedLogin is the credential vault's first use of a
// saved login on a site; it is gated with CategoryCredential.
const (
	KindBrowserAction = "browser-action"
	KindSavedLogin    = "credential-use"
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
	// CategoryScript covers commands whose effect on the page cannot be read
	// in advance: page scripts, coordinate clicks, unrecognized commands, and
	// targets the runtime could not describe.
	CategoryScript = "script"
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
	// FormHasNewPassword means the form sets a password (an
	// autocomplete="new-password" field, or a password and its confirmation),
	// as a change or reset does and a sign-in does not.
	FormHasNewPassword bool `json:"formHasNewPassword,omitempty"`
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
	if !verdict.NeedsApproval && mode == ModeEverySubmit && target.IsSubmit && submitsByPress(action, role, target.Key) {
		verdict = Verdict{NeedsApproval: true, Category: CategorySubmit, Reason: "This submits a form, and this session asks before every form submission."}
	}
	if verdict.NeedsApproval {
		verdict.Summary = summarize(action, target)
	}
	return verdict
}

// submitsByPress is false only for a press that cannot submit: a key other
// than Enter or Space, or Space outside a control (where it types).
func submitsByPress(action, role, key string) bool {
	if action != "press" {
		return true
	}
	if IsEnterKey(key) {
		return true
	}
	return IsActivationKey(key) && isActivatable(role)
}

func isActivatable(role string) bool {
	return role == "button" || role == "link" || role == "menuitem"
}

func classifyRisk(action, role, name string, target Target) Verdict {
	if action == "press" {
		if !IsActivationKey(target.Key) {
			return Verdict{}
		}
		if isActivatable(role) {
			// Enter or Space on a focused control activates it, exactly
			// like a click.
			return classifyRisk("click", role, name, target)
		}
		if !IsEnterKey(target.Key) {
			// Space elsewhere types or scrolls; only Enter submits a form.
			return Verdict{}
		}
		if target.IsSubmit {
			// Enter submits the form as its default button would.
			if verdict := classifySubmit(normalize(target.SubmitName), target); verdict.NeedsApproval {
				return verdict
			}
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
	// Card fields decide before any label: a payment form's "Send" or
	// "Search" button still pays, and must not fall under a message rule.
	if target.IsSubmit && target.FormHasPayment {
		return paymentFormVerdict()
	}
	if name != "" && name != "cancel" && !leadsWith(name, benignPhrases) && !opensComposer(name) {
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
		return classifySubmit(name, target)
	}
	return Verdict{}
}

// classifySubmit judges a form submission by its form and submit label.
func classifySubmit(submitName string, target Target) Verdict {
	// Card fields first: the label exemptions below are for ordinary search
	// and sign-in forms, which collect no card.
	if target.FormHasPayment {
		return paymentFormVerdict()
	}
	post := strings.EqualFold(target.FormMethod, "post")
	// Setting a password changes the account whatever the button says: a
	// "Sign in" label must not exempt it.
	if post && target.FormHasNewPassword {
		return Verdict{NeedsApproval: true, Category: CategoryAccount, Reason: "It submits a form that sets a new password."}
	}
	if leadsWith(submitName, []string{"search", "filter", "sign in", "log in", "login"}) {
		return Verdict{}
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
	// A password field alone is a sign-in, not an account change: only a form
	// that sets a new password (above), or whose button says it saves account
	// details, changes the account.
	if post && containsAny(submitName, accountPhrases) {
		return Verdict{NeedsApproval: true, Category: CategoryAccount, Reason: "It submits a form that changes account details."}
	}
	return Verdict{}
}

func paymentFormVerdict() Verdict {
	return Verdict{NeedsApproval: true, Category: CategoryPayment, Reason: "It submits a form with payment card fields."}
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
			// A long label matches at its start, so headlines and sentences
			// that merely mention a word stay out; a confirm button's leading
			// "Yes," or "I understand," does not count as its start.
			if strings.HasPrefix(stripFiller(name)+" ", phrase+" ") {
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

// leadsWith reports a label that is one of phrases or starts with one: a
// benign phrase ("search", "sign in") excuses "Search orders" but not
// "Delete search history".
func leadsWith(name string, phrases []string) bool {
	for _, phrase := range phrases {
		if name == phrase || strings.HasPrefix(name+" ", phrase+" ") {
			return true
		}
	}
	return false
}

// confirmFillers lead a confirm dialog's button without being its action.
var confirmFillers = []string{"yes", "ok", "okay", "sure", "confirm and", "confirm", "i understand", "i agree"}

// stripFiller drops a leading confirm filler from a normalized label.
func stripFiller(name string) string {
	for _, filler := range confirmFillers {
		if rest, ok := strings.CutPrefix(name, filler+" "); ok {
			return rest
		}
	}
	return name
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

// IsEnterKey reports whether a press dispatches Enter: "Enter", its alias
// "Return", or a chord ending in either ("Control+Enter"), resolved the way
// the runtimes resolve keys (the last non-empty "+" part).
func IsEnterKey(key string) bool {
	last := ""
	for _, part := range strings.Split(key, "+") {
		if part = strings.TrimSpace(part); part != "" {
			last = part
		}
	}
	last = strings.ToLower(last)
	return last == "enter" || last == "return" || last == "numpadenter"
}

// IsActivationKey reports whether a press can activate the focused control:
// Enter (see IsEnterKey) or Space, under any of the names runtimes accept for
// it. A chord ending in "+ " is Space too, so the raw key is checked first.
func IsActivationKey(key string) bool {
	if key == " " || strings.HasSuffix(key, "+ ") {
		return true
	}
	if IsEnterKey(key) {
		return true
	}
	last := ""
	for _, part := range strings.Split(key, "+") {
		if part = strings.TrimSpace(part); part != "" {
			last = part
		}
	}
	last = strings.ToLower(last)
	return last == "space" || last == "spacebar"
}

// ClassifyDialog judges accepting a page dialog. Accepting a confirm, prompt
// or leave-page dialog lets the page carry on with what it asked about, so it
// always asks; the dialog's wording names the category when it reads like one
// ("Delete account?"). An alert only closes. kind is the dialog type, empty
// when it could not be read; a prompt's answer is never part of the summary.
func ClassifyDialog(kind, message string, mode Mode) Verdict {
	if mode == ModeOff || strings.EqualFold(kind, "alert") {
		return Verdict{}
	}
	verdict := Verdict{NeedsApproval: true, Category: CategoryScript,
		Reason: "Accepting the page's dialog lets the page carry on with what it asked to confirm."}
	if name := normalize(message); name != "" {
		for _, set := range phraseSets {
			if matchPhrase(name, set.phrases, false) != "" {
				verdict.Category = set.category
				verdict.Reason = fmt.Sprintf("Accepting it lets the page carry on, and the dialog reads like %s.", categoryNouns[set.category])
				break
			}
		}
	}
	verdict.Summary = "Accept the page's dialog"
	if kind != "" {
		verdict.Summary = fmt.Sprintf("Accept the page's %s dialog", strings.ToLower(kind))
	}
	if text := strings.TrimSpace(message); text != "" {
		text, _ = Truncate(text, 120)
		verdict.Summary += fmt.Sprintf(" %q", text)
	}
	return verdict
}

// Truncate cuts s to at most max characters (not bytes, so a multi-byte
// character is never split) and reports how many characters it left out.
func Truncate(s string, max int) (string, int) {
	runes := []rune(s)
	if len(runes) <= max {
		return s, 0
	}
	return string(runes[:max]) + "…", len(runes) - max
}

// HasLineBreak reports whether typed text contains a line break. Runtimes
// that type key by key turn it into a real Enter keypress.
func HasLineBreak(text string) bool {
	return strings.ContainsAny(text, "\r\n")
}

// TypedEnter turns typing a line break into target into the Enter press it
// is. Enter in a single-line field submits its form; describe reports that
// only for the focused element, so it is derived here from the form facts.
func TypedEnter(target Target) Target {
	target.Action, target.Key = "press", "Enter"
	if !target.IsSubmit && target.FormMethod != "" && isTextField(target) {
		target.IsSubmit = true
	}
	return target
}

// isTextField is a single-line field, where Enter submits. An unread tag
// counts when the role says text entry, so missing facts ask, not pass.
func isTextField(target Target) bool {
	switch strings.ToLower(target.Tag) {
	case "input":
		switch strings.ToLower(target.InputType) {
		case "checkbox", "radio", "submit", "button", "reset", "image", "file":
			return false
		}
		return true
	case "":
		switch target.Role {
		case "textbox", "searchbox", "combobox":
			return true
		}
	}
	return false
}

// keyLabel names a pressed key for a prompt.
func keyLabel(key string) string {
	if IsEnterKey(key) {
		return "Enter"
	}
	return "Space"
}

// normalize lowercases and turns punctuation and any Unicode space into
// single spaces, so "Pay $42.10" becomes "pay 42 10", "Pay\u00a042" becomes
// "pay 42", and "two-factor" becomes "two factor". Letters and digits of
// every script are kept.
func normalize(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
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
		key := keyLabel(target.Key)
		switch {
		case label == "":
			text = "Press " + key
		case key == "Space":
			text = fmt.Sprintf("Press Space on %q", label)
		default:
			text = fmt.Sprintf("Press Enter in %q", label)
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
	if host := Host(target.PageURL); host != "" {
		text += " on " + host
	}
	return text
}

// Site returns the origin a page URL belongs to (scheme, host, and any
// non-default port), the key for per-site rules: the scope browsers use for
// permissions, so an allow on http://localhost:4173 does not cover another
// dev server, and one on https://shop.example does not cover plain http.
func Site(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && port != defaultPort[scheme] {
		host += ":" + port
	}
	return scheme + "://" + host
}

var defaultPort = map[string]string{"http": "80", "https": "443"}

// Host names a page's host for prompts ("shop.example"); rules key on Site.
func Host(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
