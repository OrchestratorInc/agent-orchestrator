// Package browser owns authorization and dispatch for session-scoped browser
// commands. HTTP controllers remain transport-only adapters.
package browser

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var actions = map[string]struct{}{
	"open": {}, "snapshot": {}, "act": {}, "click": {}, "dblclick": {}, "focus": {}, "fill": {}, "type": {}, "press": {},
	"hover": {}, "highlight": {}, "unhighlight": {}, "scrollintoview": {}, "drag": {}, "tabs": {}, "tab-new": {},
	"tab-select": {}, "tab-close": {}, "scroll": {}, "select": {}, "check": {},
	"uncheck": {}, "get": {}, "wait": {}, "screenshot": {}, "network-start": {},
	"network-status": {}, "network-list": {}, "network-stop": {}, "network-clear": {},
	"console": {}, "errors": {}, "frame": {}, "dialog": {},
	"devtools-open": {}, "devtools-close": {}, "target": {},
}

type sessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

type runtime interface {
	Status() browserruntime.Status
	Execute(ctx context.Context, sessionID domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error)
}

// Service validates worker ownership and lifecycle state before dispatching to
// the session's browser runtime: the Electron panel by default, or the user's
// Chrome through the AO extension when the session chose it.
type Service struct {
	sessions  sessionReader
	runtime   runtime
	authority *Authority

	chrome  runtime
	targets *TargetStore
	log     *slog.Logger
}

// New creates a browser service.
func New(sessions sessionReader, runtime runtime, authority *Authority) *Service {
	return &Service{sessions: sessions, runtime: runtime, authority: authority}
}

// UseChrome enables the Chrome extension runtime. Sessions opt in with the
// "target" action; their choice persists in targets.
func (s *Service) UseChrome(chrome runtime, targets *TargetStore, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	s.chrome = chrome
	s.targets = targets
	s.log = log
}

// Status returns transport state after validating the session owner.
func (s *Service) Status(ctx context.Context, sessionID domain.SessionID, capability string) (browserruntime.Status, error) {
	if err := s.authorize(ctx, sessionID, capability); err != nil {
		return browserruntime.Status{}, err
	}
	target := s.targetFor(sessionID)
	status := s.runtimeFor(target).Status()
	status.Target = target
	return status, nil
}

func (s *Service) targetFor(sessionID domain.SessionID) string {
	if s.chrome == nil {
		return TargetPanel
	}
	return s.targets.Get(sessionID)
}

func (s *Service) runtimeFor(target string) runtime {
	if target == TargetChrome && s.chrome != nil {
		return s.chrome
	}
	return s.runtime
}

// Execute validates ownership and dispatches one supported action.
func (s *Service) Execute(
	ctx context.Context,
	sessionID domain.SessionID,
	capability string,
	action string,
	args map[string]interface{},
) (browserruntime.Result, string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if err := s.authorize(ctx, sessionID, capability); err != nil {
		return browserruntime.Result{}, action, err
	}
	if _, ok := actions[action]; !ok {
		return browserruntime.Result{}, action, apierr.Invalid(
			"BROWSER_ACTION_UNSUPPORTED",
			"Unsupported browser action",
			nil,
		)
	}
	if action == "target" {
		result, err := s.target(sessionID, args)
		return result, action, err
	}
	target := s.targetFor(sessionID)
	if target == TargetChrome {
		// Chrome mode acts with the user's real identity; keep an audit line
		// per command (never arguments, which can hold typed text).
		s.log.Info("browser chrome command", "session", sessionID, "action", action)
	}
	result, err := s.runtimeFor(target).Execute(ctx, sessionID, action, args)
	return result, action, err
}

// target reads or changes which browser the session's commands go to.
func (s *Service) target(sessionID domain.SessionID, args map[string]interface{}) (browserruntime.Result, error) {
	if requested, ok := args["target"]; ok {
		target, _ := requested.(string)
		target = strings.ToLower(strings.TrimSpace(target))
		if !ValidTarget(target) {
			return browserruntime.Result{}, apierr.Invalid("INVALID_ARGUMENT", "target must be panel or chrome", nil)
		}
		if target == TargetChrome && s.chrome == nil {
			return browserruntime.Result{}, apierr.Invalid("BROWSER_CHROME_UNAVAILABLE", "Chrome mode is not available in this daemon", nil)
		}
		if s.chrome != nil {
			if err := s.targets.Set(sessionID, target); err != nil {
				return browserruntime.Result{}, err
			}
		}
	}
	target := s.targetFor(sessionID)
	status := s.runtimeFor(target).Status()
	return browserruntime.Result{Value: map[string]interface{}{
		"target":        target,
		"transport":     status.Transport,
		"connected":     status.Connected,
		"client":        status.Client,
		"clientVersion": status.ClientVersion,
	}}, nil
}

type sessionDestroyer interface {
	DestroySession(ctx context.Context, sessionID domain.SessionID) error
}

// Lifecycle tears down a session's browser state in every runtime and forgets
// its target. It implements the session manager's browser lifecycle hook.
type Lifecycle struct {
	Panel   sessionDestroyer
	Chrome  sessionDestroyer
	Targets *TargetStore
}

// DestroySession releases the session in both runtimes.
func (l Lifecycle) DestroySession(ctx context.Context, sessionID domain.SessionID) error {
	var errs []error
	if l.Panel != nil {
		errs = append(errs, l.Panel.DestroySession(ctx, sessionID))
	}
	if l.Chrome != nil {
		errs = append(errs, l.Chrome.DestroySession(ctx, sessionID))
	}
	errs = append(errs, l.Targets.Delete(sessionID))
	return errors.Join(errs...)
}

func (s *Service) authorize(ctx context.Context, sessionID domain.SessionID, capability string) error {
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.IsTerminated {
		return apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}
	if s.authority == nil || !s.authority.Valid(
		sessionID,
		strings.TrimSpace(capability),
		session.Metadata.BrowserCapabilityVerifier,
	) {
		return apierr.Forbidden("BROWSER_CAPABILITY_INVALID", "Browser capability is invalid")
	}
	return nil
}
