package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// RemoteResponse is a self-hosted agent-browser service's reply to one
// command: its output, an optional error, and files it wrote.
type RemoteResponse struct {
	Output string            `json:"output"`
	Error  string            `json:"error,omitempty"`
	Files  map[string]string `json:"files,omitempty"`
}

// RemoteBrowser forwards raw agent-browser commands to the host's own
// browser service (AO_AGENT_BROWSER_URL), holding its token.
type RemoteBrowser interface {
	Forward(ctx context.Context, args []string, files map[string]string) (RemoteResponse, error)
}

// UseRemoteBrowser routes sessions' remote browser commands through this
// service, so they pass the approval gate before reaching the browser.
func (s *Service) UseRemoteBrowser(remote RemoteBrowser) {
	s.remote = remote
}

// ExecuteRemote checks one agent-browser command against the approval gate,
// then forwards it to the host's browser service. It fails closed: a command
// that may need approval is refused when no gate can ask.
func (s *Service) ExecuteRemote(ctx context.Context, sessionID domain.SessionID, capability string, args []string, files map[string]string) (RemoteResponse, error) {
	if err := s.authorize(ctx, sessionID, capability); err != nil {
		return RemoteResponse{}, err
	}
	if s.remote == nil {
		return RemoteResponse{}, apierr.Unavailable("BROWSER_REMOTE_UNAVAILABLE", "This host has no agent browser service")
	}
	if err := s.remotePreflight(ctx, sessionID, args); err != nil {
		return RemoteResponse{}, err
	}
	out, err := s.remote.Forward(ctx, args, files)
	if err != nil {
		return RemoteResponse{}, apierr.Unavailable("BROWSER_REMOTE_UNREACHABLE", err.Error())
	}
	return out, nil
}

type remoteGateKind int

const (
	remoteNone remoteGateKind = iota
	// remoteDescribe: read the target, then classify it.
	remoteDescribe
	// remoteAsk: the effect cannot be read in advance; always ask.
	remoteAsk
)

type remoteGate struct {
	kind     remoteGateKind
	action   string
	selector string
	key      string
	role     string
	name     string
	// typed: the command types a line break, which presses Enter.
	typed bool
}

// Commands that read, navigate by URL, or change a field's value without
// submitting. Everything not listed here and not classified below asks.
var remoteBenign = map[string]struct{}{
	"open": {}, "goto": {}, "navigate": {}, "back": {}, "forward": {}, "reload": {}, "snapshot": {},
	"screenshot": {}, "pdf": {}, "get": {}, "is": {}, "wait": {}, "scroll": {}, "scrollintoview": {},
	"hover": {}, "focus": {}, "fill": {}, "type": {}, "select": {}, "check": {}, "uncheck": {},
	"highlight": {}, "tab": {}, "tabs": {}, "window": {}, "frame": {}, "dialog": {}, "console": {},
	"errors": {}, "network": {}, "set": {}, "diff": {}, "upload": {}, "download": {}, "close": {},
	"help": {}, "version": {}, "drag": {}, "record": {}, "trace": {}, "cookies": {}, "storage": {},
}

// classifyRemoteArgs reads an agent-browser command line the way the service
// will: leading flags (and their values) are skipped, the first word is the
// command.
func classifyRemoteArgs(args []string) remoteGate {
	words := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(words) == 0 && strings.HasPrefix(arg, "-") {
			// A leading "--flag value" pair: skip the value too.
			if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && arg != "--help" && arg != "-h" {
				i++
			}
			continue
		}
		words = append(words, arg)
	}
	if len(words) == 0 {
		return remoteGate{}
	}
	command := strings.ToLower(words[0])
	rest := words[1:]
	switch command {
	case "click", "dblclick":
		if len(rest) == 0 {
			return remoteGate{}
		}
		return remoteGate{kind: remoteDescribe, action: command, selector: rest[0]}
	case "type":
		// Typing key by key turns a line break into a real Enter.
		if len(rest) > 1 && slices.ContainsFunc(rest[1:], approval.HasLineBreak) {
			return remoteGate{kind: remoteDescribe, action: "type", key: "Enter", selector: rest[0], typed: true}
		}
		return remoteGate{}
	case "press", "key":
		if len(rest) > 0 && approval.IsActivationKey(rest[0]) {
			return remoteGate{kind: remoteDescribe, action: "press", key: rest[0]}
		}
		return remoteGate{}
	case "keyboard":
		for _, word := range rest {
			if approval.IsActivationKey(word) {
				return remoteGate{kind: remoteDescribe, action: "press", key: word}
			}
			if approval.HasLineBreak(word) {
				return remoteGate{kind: remoteDescribe, action: "press", key: "Enter", typed: true}
			}
		}
		return remoteGate{}
	case "find":
		return classifyFind(rest)
	case "dialog":
		// Accepting a confirm or prompt lets the page carry on with what it
		// asked about; reading or dismissing it does not.
		if len(rest) > 0 && strings.EqualFold(rest[0], "accept") {
			return remoteGate{kind: remoteAsk, action: "dialog"}
		}
		return remoteGate{}
	case "eval", "js", "mouse", "batch", "run":
		return remoteGate{kind: remoteAsk, action: command}
	}
	if _, ok := remoteBenign[command]; ok {
		return remoteGate{}
	}
	return remoteGate{kind: remoteAsk, action: command}
}

// classifyFind handles `find <locator> <value> <action> [--name N]`.
func classifyFind(rest []string) remoteGate {
	var positional []string
	name := ""
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--name" && i+1 < len(rest) {
			name = rest[i+1]
			i++
			continue
		}
		if strings.HasPrefix(rest[i], "-") {
			continue
		}
		positional = append(positional, rest[i])
	}
	if len(positional) < 3 {
		return remoteGate{}
	}
	action := strings.ToLower(positional[2])
	gate := remoteGate{kind: remoteDescribe, action: "click"}
	switch {
	case action == "click" || action == "dblclick":
	case action == "type" && slices.ContainsFunc(positional[3:], approval.HasLineBreak):
		gate.action, gate.key, gate.typed = "type", "Enter", true
	default:
		return remoteGate{}
	}
	if strings.EqualFold(positional[0], "role") {
		gate.role = strings.ToLower(positional[1])
		gate.name = name
	} else {
		gate.name = positional[1]
	}
	return gate
}

func (s *Service) remotePreflight(ctx context.Context, sessionID domain.SessionID, args []string) error {
	gate := classifyRemoteArgs(args)
	if gate.kind == remoteNone {
		return nil
	}
	if s.approvals == nil {
		return apierr.Unavailable("BROWSER_APPROVAL_UNAVAILABLE", "This command may need the user's approval, and approvals are unavailable on this host, so it was not run")
	}
	mode := s.approvals.SessionMode(string(sessionID))
	if mode == approval.ModeOff {
		return nil
	}
	// Discovery gets its own short deadline: a stalled page must not use up
	// the request's budget before the user is even asked.
	limit := s.describeTimeout
	if limit <= 0 {
		limit = DefaultDescribeTimeout
	}
	discoverCtx, cancelDiscover := context.WithTimeout(ctx, limit)
	defer cancelDiscover()
	pageURL, pageTitle := s.remotePage(discoverCtx)
	summary := onSite(remoteSummary(gate, args), pageURL)
	var verdict approval.Verdict
	var approved approval.Target
	var approvedIdentified bool
	var approvedDocument float64
	if gate.kind == remoteDescribe {
		target, identified, partial, document := s.remoteTarget(discoverCtx, gate)
		// The address is read again after the target: if the page moved on in
		// between, the prompt would pair one page's address with another's
		// control. A re-read that fails (discovery ran out of time) proves no
		// navigation, so the facts are dropped and the user is asked about an
		// unknown target instead.
		afterURL, readOK := s.remoteURL(discoverCtx)
		switch {
		case !readOK:
			target, identified, partial, document = approval.Target{Action: gate.action, Key: gate.key}, false, false, 0
		case afterURL != pageURL:
			return remoteTargetChanged()
		}
		target.PageURL = pageURL
		approved, approvedIdentified, approvedDocument = target, identified, document
		target.PageURL, target.PageTitle = pageURL, pageTitle
		if identified {
			verdict = approval.Classify(target, mode)
		}
		if identified && partial && !verdict.NeedsApproval {
			verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
				Summary: summary,
				Reason:  "Orchestrator could not read the form this element belongs to, so it asks first."}
		} else if !identified {
			verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
				Summary: summary,
				Reason:  "Orchestrator could not identify the element this acts on, so it asks first."}
		}
	} else {
		reason := "This runs JavaScript in the page, which can do anything the page can, including pay or send."
		switch {
		case gate.action == "dialog":
			reason = "Accepting the page's dialog lets the page carry on with what it asked to confirm."
		case gate.action != "eval" && gate.action != "js":
			reason = "Orchestrator cannot tell in advance what this command does on the page, so it asks first."
		}
		verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
			Summary: summary, Reason: reason}
	}
	if !verdict.NeedsApproval {
		return nil
	}
	outcome, err := s.approvals.Request(ctx, approval.Request{
		Kind: approval.KindBrowserAction, SessionID: string(sessionID), Site: approval.Site(pageURL), URL: pageURL,
		PageTitle: pageTitle, Action: verdict.Summary, Category: verdict.Category, Reason: verdict.Reason,
		Timeout: s.approvalTimeout,
	})
	if err != nil {
		return err
	}
	if !outcome.Allowed() {
		return approvalRefusal(outcome.Decision, verdict.Summary, s.approvalTimeout)
	}
	// The service runs refs and selectors when the command arrives, not when
	// the user approved it: read the page again and refuse if it moved on.
	recheckCtx, cancelRecheck := context.WithTimeout(ctx, limit)
	defer cancelRecheck()
	// The target is read first and the address after it, so a navigation
	// between the two reads cannot pass both checks. What remains is the gap
	// between this read and the service running the command, which only the
	// service can close (it takes no expected-page guard today).
	// A target the user approved without seeing (it could not be read) has
	// nothing to compare but the page itself.
	if gate.kind == remoteDescribe && approvedIdentified {
		now, identified, _, document := s.remoteTarget(recheckCtx, gate)
		var readOK bool
		now.PageURL, readOK = s.remoteURL(recheckCtx)
		// After approval an unread address is not trusted: refuse.
		if !readOK || identified != approvedIdentified || document != approvedDocument || !sameRemoteTarget(now, approved) {
			return remoteTargetChanged()
		}
		return nil
	}
	if nowURL, readOK := s.remoteURL(recheckCtx); !readOK || nowURL != pageURL {
		return remoteTargetChanged()
	}
	return nil
}

// sameRemoteTarget compares what the user was shown and judged by.
func sameRemoteTarget(a, b approval.Target) bool {
	return a.PageURL == b.PageURL && a.Role == b.Role && a.Name == b.Name && a.IsSubmit == b.IsSubmit &&
		a.FormAction == b.FormAction && a.FormMethod == b.FormMethod && a.FormHasPayment == b.FormHasPayment &&
		a.FormHasPassword == b.FormHasPassword && a.FormHasNewPassword == b.FormHasNewPassword && a.SubmitName == b.SubmitName
}

func remoteTargetChanged() error {
	return apierr.Conflict("BROWSER_TARGET_CHANGED",
		"The page changed while the action waited for approval, so it no longer acts on what the user approved. Take a new snapshot and try again.", nil)
}

// remoteSummary says what a gated command does from its verb and target
// only. It goes to the prompt, the audit log and push notifications, so it
// never carries typed text or other argument values; eval and js keep their
// script, which is the action being approved.
func remoteSummary(gate remoteGate, args []string) string {
	if gate.kind == remoteAsk && gate.action == "dialog" {
		return "Accept the page's dialog"
	}
	if gate.kind == remoteAsk {
		if gate.action == "eval" || gate.action == "js" {
			script := strings.Join(remoteCommandArgs(args, gate.action), " ")
			if len(script) > 160 {
				script = script[:160] + "…"
			}
			return fmt.Sprintf("Run %q", strings.TrimSpace(gate.action+" "+script))
		}
		return fmt.Sprintf("Run %q", gate.action)
	}
	target := "the focused element"
	switch {
	case gate.name != "":
		target = fmt.Sprintf("%q", gate.name)
	case gate.selector != "":
		target = gate.selector
		if len(target) > 80 {
			target = target[:80] + "…"
		}
	}
	switch {
	case gate.typed:
		return "Type a line break into " + target
	case gate.action == "press":
		return fmt.Sprintf("Press %s on %s", strings.TrimSpace(gate.key), target)
	case gate.action == "dblclick":
		return "Double-click " + target
	}
	return "Click " + target
}

// remoteCommandArgs returns what follows the command verb in args.
func remoteCommandArgs(args []string, verb string) []string {
	for i, word := range args {
		if strings.EqualFold(word, verb) {
			return args[i+1:]
		}
	}
	return nil
}

func onSite(text, pageURL string) string {
	if host := approval.Host(pageURL); host != "" {
		return text + " on " + host
	}
	return text
}

// remoteURL reads the active page's URL; ok is false when the read failed.
func (s *Service) remoteURL(ctx context.Context) (string, bool) {
	data, ok := s.remoteData(ctx, "get", "url", "--json")
	if !ok {
		return "", false
	}
	url, _ := data["url"].(string)
	return url, true
}

// remotePage reads the active page's URL and title; empty when unavailable.
func (s *Service) remotePage(ctx context.Context) (string, string) {
	var url, title string
	if data, ok := s.remoteData(ctx, "get", "url", "--json"); ok {
		url, _ = data["url"].(string)
	}
	if data, ok := s.remoteData(ctx, "get", "title", "--json"); ok {
		title, _ = data["title"].(string)
	}
	return url, title
}

// remoteTarget describes what a click, Enter or Space would act on.
// identified is false when nothing about the element could be read; partial
// is true when the element was named (by a snapshot ref or a find locator)
// but the page could not resolve it to one element, so its form is unknown.
// document identifies the page document the facts were read from (its
// performance.timeOrigin, new on every navigation or reload); 0 when unread.
func (s *Service) remoteTarget(ctx context.Context, gate remoteGate) (target approval.Target, identified, partial bool, document float64) {
	target = approval.Target{Action: gate.action, Key: gate.key, Role: gate.role, Name: gate.name}
	var request map[string]string
	switch ref, isRef := snapshotRef(gate.selector); {
	case gate.name != "":
		// find: text locators match any control; role locators one role.
		request = map[string]string{"mode": "named", "role": gate.role, "name": gate.name}
	case gate.action == "press":
		// Enter and Space act on the focused element.
		request = map[string]string{"mode": "focused"}
	case isRef:
		// The snapshot gives only role and name; the element itself says
		// what form it would submit.
		data, ok := s.remoteData(ctx, "snapshot", "--interactive", "--json")
		if !ok {
			return target, false, false, 0
		}
		refs, _ := data["refs"].(map[string]interface{})
		entry, _ := refs[ref].(map[string]interface{})
		target.Role, _ = entry["role"].(string)
		target.Name, _ = entry["name"].(string)
		if target.Role == "" && target.Name == "" {
			return target, false, false, 0
		}
		request = map[string]string{"mode": "named", "role": target.Role, "name": target.Name}
	default:
		request = map[string]string{"mode": "selector", "selector": gate.selector}
	}
	named := request["mode"] == "named"
	if named && target.Role == "" && gate.action != "type" {
		target.Role = "button"
	}
	described, ok := s.remoteDescribe(ctx, request)
	if !ok || described.Partial {
		// A named element without its form facts may still submit one.
		return typedEnter(gate, target), named, named, described.DocumentOrigin
	}
	described.Action, described.Key = gate.action, gate.key
	if named && described.Name == "" {
		described.Role, described.Name = target.Role, target.Name
	}
	return typedEnter(gate, described.Target), described.Role != "" || described.Name != "" || described.IsSubmit, false, described.DocumentOrigin
}

// typedEnter classifies a typed line break as the Enter press it becomes.
func typedEnter(gate remoteGate, target approval.Target) approval.Target {
	if !gate.typed {
		return target
	}
	return approval.TypedEnter(target)
}

type remoteFacts struct {
	approval.Target
	Partial        bool    `json:"partial"`
	DocumentOrigin float64 `json:"documentOrigin"`
}

// remoteDescribe runs remoteDescribeScript for request.
func (s *Service) remoteDescribe(ctx context.Context, request map[string]string) (remoteFacts, bool) {
	raw, _ := json.Marshal(request)
	data, ok := s.remoteData(ctx, "eval", fmt.Sprintf(remoteDescribeScript, raw), "--json")
	if !ok {
		return remoteFacts{}, false
	}
	facts, ok := data["result"].(map[string]interface{})
	if !ok {
		return remoteFacts{}, false
	}
	encoded, _ := json.Marshal(facts)
	var described remoteFacts
	if json.Unmarshal(encoded, &described) != nil {
		return remoteFacts{}, false
	}
	return described, true
}

// snapshotRef reads a snapshot ref as agents write it: "e2" as the snapshot
// prints it, or "@e2". Anything else is a CSS selector.
func snapshotRef(selector string) (string, bool) {
	ref := strings.TrimPrefix(selector, "@")
	if len(ref) < 2 || ref[0] != 'e' {
		return "", false
	}
	for _, r := range ref[1:] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return ref, true
}

// remoteData runs a describe command on the service and returns its JSON
// "data" object.
func (s *Service) remoteData(ctx context.Context, args ...string) (map[string]interface{}, bool) {
	resp, err := s.remote.Forward(ctx, args, nil)
	if err != nil || resp.Error != "" {
		return nil, false
	}
	var envelope struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(resp.Output)), &envelope) != nil || !envelope.Success {
		return nil, false
	}
	return envelope.Data, true
}

// remoteDescribeScript reads the focused element, or the one a selector
// names, the way the desktop runtimes' describe does: role, label, and the
// form it would submit. %s is a JSON request.
const remoteDescribeScript = `(() => {
const req = %s;
const clean = (v) => (v || "").replace(/\s+/g, " ").trim().slice(0, 120);
const roleOf = (el) => {
  const tag = el.tagName.toLowerCase(), type = (el.type || "").toLowerCase();
  return el.getAttribute("role") || (tag === "a" ? "link" : tag === "button" || ["submit", "button", "image"].includes(type) ? "button" : tag === "textarea" || tag === "input" ? "textbox" : "");
};
const nameOf = (el) => {
  const tag = el.tagName.toLowerCase(), type = (el.type || "").toLowerCase();
  let name = clean(el.getAttribute("aria-label")) || (tag === "input" && ["submit", "button"].includes(type) ? clean(el.value) : "");
  if (!name && el.id) { const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]'); if (l) name = clean(l.textContent); }
  // An editable field's content is what the agent typed: name it only by its
  // label or hint, never by its text, so typed values stay out of the prompt.
  const editable = tag === "textarea" || el.isContentEditable || (tag === "input" && !["submit", "button", "image", "reset", "checkbox", "radio"].includes(type));
  if (editable) {
    const wrap = name ? null : el.closest("label");
    if (wrap) name = clean([...wrap.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent).join(" "));
    return name || clean(el.placeholder || el.getAttribute("aria-placeholder") || el.getAttribute("data-placeholder") || el.title);
  }
  return name || clean(el.innerText || el.textContent || el.placeholder);
};
let el;
if (req.mode === "named") {
  // A snapshot ref or find locator names an element by role and label; it
  // counts only when exactly one control on the page matches.
  const want = clean(req.name).toLowerCase();
  const hits = [...document.querySelectorAll("a,button,input,select,textarea,summary,[role],[onclick],[tabindex]")]
    .filter((c) => (!req.role || roleOf(c) === req.role) && nameOf(c).toLowerCase() === want);
  if (hits.length !== 1) return { partial: true, documentOrigin: performance.timeOrigin };
  el = hits[0];
} else {
  el = req.mode === "focused" ? document.activeElement : document.querySelector(req.selector);
}
if (!el || el === document.body) return null;
const tag = el.tagName.toLowerCase();
const type = (el.type || "").toLowerCase();
const role = roleOf(el);
const name = nameOf(el);
const form = el.form || el.closest("form");
const out = { documentOrigin: performance.timeOrigin, role, name, tag, inputType: type || undefined, isSubmit: false, formHasPayment: false, formHasPassword: false };
if (form) {
  const pay = (f) => /cc-(number|csc|exp)/.test(f.getAttribute("autocomplete") || "") || /card.?num|cc.?num|\bcvc\b|\bcvv\b|security.?code/i.test((f.name || "") + " " + (f.id || "") + " " + (f.placeholder || ""));
  const submit = form.querySelector("button:not([type]),button[type=submit],input[type=submit],input[type=image]");
  out.formMethod = (form.getAttribute("method") || "get").toLowerCase();
  out.formAction = clean(form.action);
  out.formHasPayment = [...form.querySelectorAll("input,select")].some(pay);
  const passwords = [...form.querySelectorAll("input[type=password]")];
  out.formHasPassword = passwords.length > 0;
  out.formHasNewPassword = passwords.length > 1 || passwords.some((f) => /\bnew-password\b/i.test(f.getAttribute("autocomplete") || ""));
  out.submitName = submit ? clean(submit.getAttribute("aria-label") || submit.innerText || submit.value) : undefined;
  const textField = tag === "input" && !["checkbox", "radio", "submit", "button", "reset", "image", "file"].includes(type);
  out.isSubmit = (tag === "button" && (type || "submit") === "submit") || ["submit", "image"].includes(type) || (req.mode === "focused" && textField);
}
return out;
})()`
