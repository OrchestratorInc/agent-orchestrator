package sentryobs

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

var backgroundComponents = map[string]struct{}{
	"session_manager": {},
	"chat":            {},
	"reaper":          {},
	"lifecycle":       {},
	"scm":             {},
	"systeminstall":   {},
	"trackerintake":   {},
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
}

var safeAgentID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var backgroundSends = make(chan struct{}, 4)

func init() {
	for operation, label := range backgroundOperationLabels {
		operationLabels[operation] = label
	}
}

func isBackgroundComponent(value string) bool {
	_, ok := backgroundComponents[value]
	return ok
}

func CaptureBackground(ctx context.Context, err error, component, operation string, extra map[string]string) {
	if err == nil || errors.Is(err, context.Canceled) || IsRepeat(err) {
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
		if safe, known := safeBackgroundTag(key, value); safe && known {
			tags[key] = value
		}
	}
	fingerprint := telemetrymeta.Fingerprint("background", component, operation, errorType, code, tags["agent"], tags["reason"])
	captureAsync(ctx, withErrorTags(tags, err), fingerprint, telemetrymeta.SafeRoute{}, telemetrymeta.FailureSite{}, operation)
}

func captureAsync(ctx context.Context, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite, operation string) {
	if ctx.Err() != nil || !sampled.Load() || !enterCapture(ctx) {
		return
	}
	select {
	case backgroundSends <- struct{}{}:
	default:
		leaveCapture()
		return
	}
	ok, dropped := admitErrorFingerprint(fingerprint, operation, time.Now().UTC())
	if !ok {
		<-backgroundSends
		leaveCapture()
		return
	}
	tags = maps.Clone(withDropped(tags, dropped))
	go func() {
		defer leaveCapture()
		defer func() { <-backgroundSends }()
		capture(sentry.LevelError, tags, fingerprint, route, site, false)
	}()
}

var backgroundReasons = map[string]struct{}{
	"mass_death":         {},
	"durable_completion": {},
	"state_record":       {},
	"queued_delivery":    {},
	"wake":               {},
	"drain":              {},
	"record_failure":     {},
	"relaunch":           {},
	"restore_workspace":  {},
	"restore_attachment": {},
	"reconcile_live":     {},
	"reap":               {},
	"chat_host":          {},
	"runtime":            {},
	"db_write":           {},
	"notify":             {},
	"verify":             {},
	"persist":            {},
	"stop":               {},
	"send":               {},
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

type repeatError struct{ err error }

func (e *repeatError) Error() string { return e.err.Error() }
func (e *repeatError) Unwrap() error { return e.err }

func Repeat(err error) error {
	if err == nil {
		return nil
	}
	return &repeatError{err: err}
}

func IsRepeat(err error) bool {
	var repeat *repeatError
	return errors.As(err, &repeat)
}
