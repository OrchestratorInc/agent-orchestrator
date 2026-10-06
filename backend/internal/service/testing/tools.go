package testing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ToolNames lists the exact native testing tools mounted by HTTP and MCP.
var ToolNames = []string{"screenshot", "click", "type", "key", "read_target_logs", "target_daemon_query", "submit_report"}

// IsTool reports whether name belongs to the native testing profile.
func IsTool(name string) bool {
	for _, n := range ToolNames {
		if n == name {
			return true
		}
	}
	return false
}
func validOutcome(out domain.TestOutcome) bool {
	switch out {
	case domain.TestOutcomeReproduced, domain.TestOutcomeNotReproduced, domain.TestOutcomeNeedsInformation, domain.TestOutcomeEnvironmentBlocked, domain.TestOutcomePartial, domain.TestOutcomeCancelled:
		return true
	}
	return false
}
func validKey(key string) bool {
	if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')) {
		return true
	}
	switch key {
	case "Enter", "Tab", "Escape", "Backspace", "Delete", "Space", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown", "Control", "Shift", "Alt", "Meta":
		return true
	}
	return false
}
func decodeInput(raw json.RawMessage, name string) (any, json.RawMessage, error) {
	var input any
	var required []string
	switch name {
	case "screenshot":
		input = &domain.TestScreenshotRequest{}
	case "click":
		input = &domain.TestClickRequest{}
		required = []string{"screenshotId", "x", "y"}
	case "type":
		input = &domain.TestTypeRequest{}
		required = []string{"screenshotId", "x", "y", "text"}
	case "key":
		input = &domain.TestKeyRequest{}
		required = []string{"screenshotId", "keys"}
	case "read_target_logs":
		input = &domain.TestReadLogsRequest{}
	case "target_daemon_query":
		input = &domain.TestDaemonQueryRequest{}
		required = []string{"resource"}
	case "submit_report":
		input = &domain.TestSubmitReportRequest{}
		required = []string{"outcome", "markdown"}
	default:
		return nil, nil, invalid("Unknown testing tool")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, nil, invalid("Tool input must be a JSON object")
	}
	for _, field := range required {
		value, ok := fields[field]
		if !ok || bytes.Equal(value, []byte("null")) {
			return nil, nil, invalid("Missing required tool input")
		}
	}
	for _, value := range fields {
		if bytes.Equal(value, []byte("null")) {
			return nil, nil, invalid("Null tool input is not allowed")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(input) != nil {
		return nil, nil, invalid("Invalid testing tool input")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, nil, invalid("Trailing tool input")
	}
	switch v := input.(type) {
	case *domain.TestClickRequest:
		if v.ScreenshotID == "" || v.X < 0 || v.Y < 0 {
			return nil, nil, invalid("Screenshot and nonnegative coordinates are required")
		}
		if _, provided := fields["button"]; !provided {
			v.Button = domain.TestMouseButtonLeft
		}
		if v.Button != domain.TestMouseButtonLeft && v.Button != domain.TestMouseButtonRight && v.Button != domain.TestMouseButtonMiddle {
			return nil, nil, invalid("Invalid mouse button")
		}
	case *domain.TestTypeRequest:
		if v.ScreenshotID == "" || v.X < 0 || v.Y < 0 || !utf8.ValidString(v.Text) || utf8.RuneCountInString(v.Text) > 16384 {
			return nil, nil, invalid("Invalid type input")
		}
	case *domain.TestKeyRequest:
		if v.ScreenshotID == "" || len(v.Keys) < 1 || len(v.Keys) > 4 {
			return nil, nil, invalid("Invalid key input")
		}
		for _, key := range v.Keys {
			if !validKey(key) {
				return nil, nil, invalid("Key is not allowed")
			}
		}
	case *domain.TestReadLogsRequest:
		if _, provided := fields["maxBytes"]; !provided {
			v.MaxBytes = 65536
		}
		if v.MaxBytes < 1 || v.MaxBytes > 262144 || len(v.Cursor) > 256 {
			return nil, nil, invalid("Invalid log bounds")
		}
	case *domain.TestDaemonQueryRequest:
		if v.Resource != domain.TestDaemonProjects && v.Resource != domain.TestDaemonSessions {
			return nil, nil, invalid("Invalid daemon resource")
		}
	case *domain.TestSubmitReportRequest:
		if !validOutcome(v.Outcome) || len(v.Markdown) > 65536 || !utf8.ValidString(v.Markdown) {
			return nil, nil, invalid("Invalid report outcome or markdown over 64 KiB")
		}
	}
	canonical, err := json.Marshal(input)
	return input, canonical, err
}

func (s *Service) authorize(ctx context.Context, id domain.TestAttemptID, session domain.SessionID, token string) (*attemptState, capability, error) {
	hash := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.caps[session]
	if !ok || token == "" || grant.link.AttemptID != id || subtle.ConstantTimeCompare(grant.hash[:], hash[:]) != 1 {
		return nil, capability{}, apierr.Forbidden("INVALID_TEST_CAPABILITY", "Testing capability is missing, revoked or does not own this attempt")
	}
	st := s.attempts[id]
	if s.closed || st == nil || st.ctx.Err() != nil || grant.ctx.Err() != nil || st.record.Phase != domain.TestAttemptActive || st.record.CancelledAt != nil || !s.deps.Clock.Now().Before(st.record.Deadline) {
		return nil, capability{}, inactive()
	}
	r, found, err := s.deps.Store.GetTestAttempt(ctx, id)
	if err != nil {
		return nil, capability{}, err
	}
	if !found || r.Phase != domain.TestAttemptActive || r.CancelledAt != nil || !s.deps.Clock.Now().Before(r.Deadline) {
		return nil, capability{}, inactive()
	}
	if !sameTarget(r.Target, grant.target) || !sameTarget(r.Target, st.record.Target) || r.LeaseGeneration != grant.target.Generation {
		return nil, capability{}, targetChanged()
	}
	link, found, err := s.deps.Store.GetTestToolBinding(ctx, session)
	if err != nil {
		return nil, capability{}, err
	}
	if !found || link != grant.link {
		return nil, capability{}, apierr.Forbidden("INVALID_TEST_CAPABILITY", "Session testing binding changed")
	}
	return st, grant, nil
}

// Execute validates ownership and journals a tool before dispatch.
func (s *Service) Execute(ctx context.Context, id domain.TestAttemptID, session domain.SessionID, token, requestID, name string, raw json.RawMessage) (result ToolResult, err error) {
	if err := s.configured(); err != nil {
		return result, err
	}
	if requestID == "" || len(requestID) > 128 || len(raw) > 256*1024 {
		return result, invalid("Request ID and bounded tool input are required")
	}
	input, canonical, err := decodeInput(raw, name)
	if err != nil {
		return result, err
	}
	st, grant, err := s.authorize(ctx, id, session, token)
	if err != nil {
		return result, err
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(canonical), token) || strings.Contains(requestID, token) {
		return result, invalid("Capability secrets cannot be written as tool data")
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopAttempt := context.AfterFunc(st.ctx, cancel)
	defer stopAttempt()
	stopCap := context.AfterFunc(grant.ctx, cancel)
	defer stopCap()
	s.mu.Lock()
	if st.seen[requestID] {
		s.mu.Unlock()
		return result, apierr.Conflict("DUPLICATE_TEST_REQUEST", "Request ID was already admitted; inspect evidence instead of repeating input", nil)
	}
	st.seen[requestID] = true
	s.mu.Unlock()
	select {
	case <-callCtx.Done():
		return result, callCtx.Err()
	case <-st.gate:
	}
	defer func() { st.gate <- struct{}{} }()
	if _, _, err = s.authorize(callCtx, id, session, token); err != nil {
		return result, err
	}
	if err = s.deps.Target.Probe(callCtx, grant.target); err != nil {
		return result, targetChanged()
	}
	record := domain.TestActionRecord{AttemptID: id, RequestID: requestID, Tool: name, Input: canonical, State: "dispatching", At: s.deps.Clock.Now().UTC()}
	if name == "click" || name == "type" || name == "key" {
		record.ConfiguredDeliveryMode = s.deliveryMode()
		record.DeliveryMode = record.ConfiguredDeliveryMode
		if policy, ok := s.deps.Desktop.(ports.TestingDesktopPolicy); ok {
			record.DeliveryMode = policy.InputDeliveryMode(name)
		}
	}
	if err = s.deps.Evidence.AppendAction(callCtx, record); err != nil {
		return result, apierr.Internal("TEST_EVIDENCE_WRITE_FAILED", "Cannot save action journal; tool was not dispatched")
	}
	defer func() {
		journalCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		record.At = s.deps.Clock.Now().UTC()
		record.State = "completed"
		record.Detail = "Tool completed"
		if err != nil {
			record.State = "failed"
			record.Detail = "Tool failed or was cancelled; partial action may have been delivered"
			var failure *apierr.Error
			if errors.As(err, &failure) && failure.Code == "TEST_INPUT_REFUSED" {
				record.State = "refused"
				record.Detail = "Input refused before dispatch; nothing was sent"
			}
		}
		if e := s.deps.Evidence.AppendAction(journalCtx, record); e != nil {
			err = apierr.Internal("TEST_EVIDENCE_WRITE_FAILED", "Cannot save action completion; partial action may have been delivered")
		}
	}()
	if _, _, err = s.authorize(callCtx, id, session, token); err != nil {
		return result, err
	}
	if err = s.deps.Target.Probe(callCtx, grant.target); err != nil {
		return result, targetChanged()
	}
	if err := callCtx.Err(); err != nil {
		return result, err
	}
	result, err = s.dispatch(callCtx, st, grant.target, input)
	if err != nil {
		return result, err
	}
	if callCtx.Err() != nil && name != "submit_report" {
		return result, callCtx.Err()
	}
	return result, nil
}
func (s *Service) frame(st *attemptState, target domain.TestTargetIdentity, id string, x, y *int) (domain.TestDesktopFrame, error) {
	s.mu.Lock()
	f, ok := st.frames[id]
	s.mu.Unlock()
	if !ok || !sameTarget(f.Target, target) || f.Width < 1 || f.Height < 1 || f.Bounds.Width <= 0 || f.Bounds.Height <= 0 || s.deps.Clock.Now().Sub(f.CapturedAt) > 2*time.Minute || f.CapturedAt.After(s.deps.Clock.Now()) {
		return f, invalid("Screenshot is foreign, stale or has invalid geometry; capture again")
	}
	if x != nil && (*x < 0 || *y < 0 || *x >= f.Width || *y >= f.Height) {
		return f, invalid("Coordinates are outside the screenshot")
	}
	return f, nil
}
func (s *Service) save(ctx context.Context, st *attemptState, result *ToolResult, kind, mime string, data []byte, frame *domain.TestDesktopFrame) (domain.TestEvidenceReceipt, error) {
	receipt, err := s.deps.Evidence.Write(ctx, st.record.ID, ports.TestingEvidenceArtifact{Kind: kind, MIMEType: mime, Frame: frame}, bytes.NewReader(data))
	if err != nil {
		return receipt, apierr.Internal("TEST_EVIDENCE_WRITE_FAILED", "Cannot save tool evidence; partial action may have been delivered")
	}
	result.Evidence = append(result.Evidence, receipt)
	return receipt, nil
}
func (s *Service) dispatch(ctx context.Context, st *attemptState, target domain.TestTargetIdentity, input any) (result ToolResult, err error) {
	result.Evidence = []domain.TestEvidenceReceipt{}
	switch v := input.(type) {
	case *domain.TestScreenshotRequest:
		shot, e := s.deps.Desktop.Screenshot(ctx, target)
		if e != nil {
			return result, apierr.Unavailable("TEST_SCREENSHOT_FAILED", "Bound target screenshot failed")
		}
		if !sameTarget(shot.Frame.Target, target) || shot.Frame.Width < 1 || shot.Frame.Height < 1 || shot.Frame.Bounds.Width <= 0 || shot.Frame.Bounds.Height <= 0 || shot.MIMEType != "image/png" || len(shot.Data) == 0 {
			return result, targetChanged()
		}
		geometry, e := png.DecodeConfig(bytes.NewReader(shot.Data))
		if e != nil || geometry.Width != shot.Frame.Width || geometry.Height != shot.Frame.Height {
			return result, targetChanged()
		}
		// Evidence receives metadata only. Keep the adapter's private capture
		// receipt intact in memory for subsequent input, even if a store mutates
		// the metadata pointer it receives.
		metadata := shot.Frame
		metadata.Target = domain.TestTargetIdentity{}
		metadata.CaptureHandle = ""
		receipt, e := s.save(ctx, st, &result, "screenshot", shot.MIMEType, shot.Data, &metadata)
		if e != nil {
			return result, e
		}
		shot.Frame.ScreenshotID = receipt.ID
		s.mu.Lock()
		st.frames[receipt.ID] = shot.Frame
		s.mu.Unlock()
		result.Screenshot = &shot
	case *domain.TestClickRequest:
		f, e := s.frame(st, target, v.ScreenshotID, &v.X, &v.Y)
		if e != nil {
			return result, e
		}
		action, e := s.deps.Desktop.Click(ctx, target, f, *v)
		if e != nil {
			return result, inputFailure(e)
		}
		result.Action = &action
	case *domain.TestTypeRequest:
		f, e := s.frame(st, target, v.ScreenshotID, &v.X, &v.Y)
		if e != nil {
			return result, e
		}
		action, e := s.deps.Desktop.Type(ctx, target, f, *v)
		if e != nil {
			return result, inputFailure(e)
		}
		result.Action = &action
	case *domain.TestKeyRequest:
		f, e := s.frame(st, target, v.ScreenshotID, nil, nil)
		if e != nil {
			return result, e
		}
		action, e := s.deps.Desktop.Key(ctx, target, f, *v)
		if e != nil {
			return result, inputFailure(e)
		}
		result.Action = &action
	case *domain.TestReadLogsRequest:
		logs, e := s.deps.Target.ReadLogs(ctx, target, *v)
		if e != nil {
			return result, apierr.Unavailable("TEST_LOGS_FAILED", "Target logs failed")
		}
		if len(logs.Text) > v.MaxBytes {
			return result, apierr.Internal("TEST_LOGS_OVERSIZED", "Provider returned oversized logs")
		}
		if _, e = s.save(ctx, st, &result, "logs", "text/plain", []byte(logs.Text), nil); e != nil {
			return result, e
		}
		result.Logs = &logs
	case *domain.TestDaemonQueryRequest:
		query, e := s.deps.Target.QueryDaemon(ctx, target, *v)
		if e != nil {
			return result, apierr.Unavailable("TEST_QUERY_FAILED", "Target daemon query failed")
		}
		if !json.Valid(query.Data) || query.Resource != v.Resource {
			return result, apierr.Internal("TEST_QUERY_INVALID", "Provider returned invalid daemon query")
		}
		if _, e = s.save(ctx, st, &result, "daemon_query", "application/json", query.Data, nil); e != nil {
			return result, e
		}
		result.Query = &query
	case *domain.TestSubmitReportRequest:
		receipt, e := s.save(ctx, st, &result, "report", "text/markdown", []byte(v.Markdown), nil)
		if e != nil {
			return result, e
		}
		if e := s.deps.Store.SetTestRunReport(ctx, st.record.RunID, receipt.ID); e != nil {
			return result, e
		}
		if _, e = s.finish(ctx, st.record.ID, v.Outcome, false); e != nil {
			return result, e
		}
		result.Report = &domain.TestSubmitReportResult{Outcome: v.Outcome, EvidenceID: receipt.ID}
	}
	if result.Action != nil {
		data, _ := json.Marshal(result.Action)
		if _, err = s.save(ctx, st, &result, "delivery", "application/json", data, nil); err != nil {
			return result, err
		}
	}
	return result, nil
}

func inputFailure(err error) error {
	if errors.Is(err, ports.ErrTestingInputRefused) {
		return apierr.Conflict("TEST_INPUT_REFUSED", "Target input refused before dispatch; nothing was sent", nil)
	}
	return apierr.Unavailable("TEST_INPUT_FAILED", "Target input failed; delivery is unverified")
}
