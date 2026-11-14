package browser

import (
	"context"
	"encoding/json"
	"fmt"
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
	case "press", "key":
		if len(rest) > 0 && approval.IsEnterKey(rest[0]) {
			return remoteGate{kind: remoteDescribe, action: "press", key: rest[0]}
		}
		return remoteGate{}
	case "keyboard":
		for _, word := range rest {
			if approval.IsEnterKey(word) {
				return remoteGate{kind: remoteDescribe, action: "press", key: word}
			}
		}
		return remoteGate{}
	case "find":
		return classifyFind(rest)
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
	if action != "click" && action != "dblclick" {
		return remoteGate{}
	}
	gate := remoteGate{kind: remoteDescribe, action: "click"}
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
	pageURL, pageTitle := s.remotePage(ctx)
	command := strings.Join(args, " ")
	if len(command) > 160 {
		command = command[:160] + "…"
	}
	var verdict approval.Verdict
	if gate.kind == remoteDescribe {
		target, identified := s.remoteTarget(ctx, gate)
		target.PageURL, target.PageTitle = pageURL, pageTitle
		if identified {
			verdict = approval.Classify(target, mode)
		} else {
			verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
				Summary: onSite(fmt.Sprintf("Run %q", command), pageURL),
				Reason:  "Orchestrator could not identify the element this acts on, so it asks first."}
		}
	} else {
		reason := "This runs JavaScript in the page, which can do anything the page can, including pay or send."
		if gate.action != "eval" && gate.action != "js" {
			reason = "Orchestrator cannot tell in advance what this command does on the page, so it asks first."
		}
		verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
			Summary: onSite(fmt.Sprintf("Run %q", command), pageURL), Reason: reason}
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
	if outcome.Allowed() {
		return nil
	}
	return approvalRefusal(outcome.Decision, verdict.Summary, s.approvalTimeout)
}

func onSite(text, pageURL string) string {
	if host := approval.Site(pageURL); host != "" {
		return text + " on " + strings.TrimPrefix(host, "www.")
	}
	return text
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

// remoteTarget describes what a click or Enter would act on. identified is
// false when nothing about the element could be read.
func (s *Service) remoteTarget(ctx context.Context, gate remoteGate) (approval.Target, bool) {
	target := approval.Target{Action: gate.action, Key: gate.key, Role: gate.role, Name: gate.name}
	if gate.name != "" {
		if target.Role == "" {
			target.Role = "button"
		}
		return target, true
	}
	if ref, ok := strings.CutPrefix(gate.selector, "@"); ok {
		if data, ok := s.remoteData(ctx, "snapshot", "--interactive", "--json"); ok {
			if refs, ok := data["refs"].(map[string]interface{}); ok {
				if entry, ok := refs[ref].(map[string]interface{}); ok {
					target.Role, _ = entry["role"].(string)
					target.Name, _ = entry["name"].(string)
					return target, target.Role != "" || target.Name != ""
				}
			}
		}
		return target, false
	}
	// Enter acts on the focused element; a CSS selector names one directly.
	request := map[string]string{"mode": "focused"}
	if gate.action != "press" {
		request = map[string]string{"mode": "selector", "selector": gate.selector}
	}
	raw, _ := json.Marshal(request)
	data, ok := s.remoteData(ctx, "eval", fmt.Sprintf(remoteDescribeScript, raw), "--json")
	if !ok {
		return target, false
	}
	facts, ok := data["result"].(map[string]interface{})
	if !ok {
		return target, false
	}
	encoded, _ := json.Marshal(facts)
	var described approval.Target
	if json.Unmarshal(encoded, &described) != nil {
		return target, false
	}
	described.Action, described.Key = gate.action, gate.key
	return described, described.Role != "" || described.Name != "" || described.IsSubmit
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
const el = req.mode === "focused" ? document.activeElement : document.querySelector(req.selector);
if (!el || el === document.body) return null;
const clean = (v) => (v || "").replace(/\s+/g, " ").trim().slice(0, 120);
const tag = el.tagName.toLowerCase();
const type = (el.type || "").toLowerCase();
let role = el.getAttribute("role") || "";
if (!role) role = tag === "a" ? "link" : tag === "button" || ["submit", "button", "image"].includes(type) ? "button" : tag === "textarea" || tag === "input" ? "textbox" : "";
let name = clean(el.getAttribute("aria-label")) || (tag === "input" && ["submit", "button"].includes(type) ? clean(el.value) : "");
if (!name && el.id) { const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]'); if (l) name = clean(l.textContent); }
if (!name) name = clean(el.innerText || el.textContent || el.placeholder);
const form = el.form || el.closest("form");
const out = { role, name, tag, inputType: type || undefined, isSubmit: false, formHasPayment: false, formHasPassword: false };
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
