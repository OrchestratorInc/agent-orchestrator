package sentryobs

import (
	"context"
	"errors"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// Background capture covers daemon work that never answers an HTTP request:
// async spawns, chat turns, restore and reconcile passes, pollers and the
// reaper. Those failures used to reach only the log (and sometimes PostHog),
// so Sentry could not see them.
//
// Every component and operation must be registered below. The values are fixed
// by AO, never copied from an error, and they name the Sentry issue title.
var backgroundComponents = map[string]struct{}{
	"session_manager": {},
	"chat":            {},
	"reaper":          {},
	"lifecycle":       {},
	"scm":             {},
	"systeminstall":   {},
	"trackerintake":   {},
	"agent_catalog":   {},
}

var backgroundOperationLabels = map[string]string{
	"chat_spawn":           "Async chat spawn failed",
	"chat_turn":            "Chat turn failed",
	"chat_projection":      "Chat event projection failed",
	"chat_provider_error":  "Chat provider reported an error",
	"chat_wake":            "Chat wake or queued delivery failed",
	"boot_restore":         "Boot restore failed",
	"reconcile":            "Reconcile pass failed",
	"interface_transition": "Interface transition failed",
	"session_quarantine":   "Session quarantined",
	"reaper_tick":          "Reaper tick failed",
	"reaper_breaker":       "Reaper mass-death breaker tripped",
	"runtime_observation":  "Runtime observation failed",
	"scm_persist":          "SCM observer write failed",
	"agent_install":        "Agent install job failed",
	"tracker_spawn":        "Tracker auto-spawn failed",
	"model_discovery":      "Model discovery failed",
}

// safeAgentID matches AO's fixed harness identifiers (claude-code, codex,
// opencode, ...). It is a tag only; the identifier never becomes a title.
var safeAgentID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func init() {
	for operation, label := range backgroundOperationLabels {
		operationLabels[operation] = label
	}
}

func isBackgroundComponent(value string) bool {
	_, ok := backgroundComponents[value]
	return ok
}

// CaptureBackground reports a background daemon failure. The caller decides
// what counts as expected user state (signed out, a user's own setup script,
// network loss) and must not call this for it. Canceled work is never sent.
//
// extra may carry only registered tags; anything else is dropped at the
// scrubber. The fingerprint groups by component, operation, error type and
// code, so one failing path is one Sentry issue.
func CaptureBackground(ctx context.Context, err error, component, operation string, extra map[string]string) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !isBackgroundComponent(component) {
		component = "daemon"
	}
	if _, ok := backgroundOperationLabels[operation]; !ok {
		return
	}
	kind, code := telemetrymeta.ErrorKindAndCode(err)
	errorType := telemetrymeta.ErrorType(err)
	tags := map[string]string{
		"component":  component,
		"operation":  operation,
		"error_kind": kind,
		"error_type": errorType,
	}
	if code != "" {
		tags["error_code"] = code
	}
	if area := Area(err, code); area != "" {
		tags["area"] = area
	}
	for key, value := range extra {
		if _, exists := tags[key]; !exists {
			tags[key] = value
		}
	}
	fingerprint := telemetrymeta.Fingerprint("background", component, operation, errorType, code, tags["agent"], tags["reason"])
	captureErrorFor(ctx, err, tags, fingerprint, telemetrymeta.SafeRoute{}, telemetrymeta.FailureSite{}, operation)
}

// backgroundReasons are fixed sub-reasons a background capture may tag, for
// failures that have no error type of their own (a tripped breaker, a
// quarantine, a provider-reported error event).
var backgroundReasons = map[string]struct{}{
	"mass_death":         {},
	"durable_completion": {},
	"state_record":       {},
	"queued_delivery":    {},
	"wake":               {},
	"drain":              {},
	"provision":          {},
	"workspace":          {},
	"controller":         {},
	"prompt":             {},
	"attachments":        {},
	"record_failure":     {},
	"retry":              {},
	"relaunch":           {},
	"restore_workspace":  {},
	"restore_attachment": {},
	"reconcile_live":     {},
	"reap":               {},
	"chat_host":          {},
	"runtime":            {},
	"graphql":            {},
	"db_write":           {},
	"notify":             {},
	"verify":             {},
	"persist":            {},
	"timeout":            {},
	"stop":               {},
	"send":               {},
	"settle":             {},
}

func safeBackgroundTag(key, value string) (bool, bool) {
	switch key {
	case "agent":
		return safeAgentID.MatchString(value), true
	case "reason":
		_, ok := backgroundReasons[value]
		return ok, true
	}
	return false, false
}
