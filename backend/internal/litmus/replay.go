package litmus

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Replay walks the browser actions recorded by a completed head check on the
// base leg. It takes a fresh interactive snapshot before every step and uses
// the target's role and accessible name instead of reusing stale refs.
func (s *Service) Replay(ctx context.Context, sessionID, checkID, leg string) (map[string]any, error) {
	if strings.TrimSpace(leg) != "base" {
		return nil, invalid("TESTING_REPLAY_LEG", "replay only supports --leg base")
	}
	checkID = strings.TrimSpace(checkID)
	if checkID == "" {
		return nil, invalid("TESTING_ARGS_INVALID", "wrong number of arguments; usage: replay <headCheckId> --leg base")
	}

	s.mu.Lock()
	run, err := s.load(sessionID)
	var checks []Check
	if err == nil {
		checks, err = s.store.Checks(run.ID)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	headIndex := slices.IndexFunc(checks, func(c Check) bool { return c.ID == checkID })
	if headIndex < 0 {
		return nil, apierr.NotFound("TESTING_CHECK_NOT_FOUND", "no check "+strconv.Quote(checkID))
	}
	head := checks[headIndex]
	if head.onLeg() != "head" {
		return nil, invalid("TESTING_REPLAY_HEAD_CHECK", "replay needs a check from the head leg")
	}
	if head.State == CheckRunning {
		return nil, conflict("TESTING_REPLAY_CHECK_STATE", "end the head check before replaying it on base")
	}
	headReceipts := slices.DeleteFunc(slices.Clone(head.Receipts), func(r Receipt) bool { return r.onLeg() != "head" })
	if len(headReceipts) == 0 {
		return nil, conflict("TESTING_REPLAY_EMPTY", "check "+strconv.Quote(checkID)+" has no head actions to replay")
	}
	if _, err := pickLeg(run, "base"); err != nil {
		return nil, err
	}

	baseCheckID, err := s.openReplayCheck(run.ID, head)
	if err != nil {
		return nil, err
	}

	var lines []string
	lastSnapshot := ""
	stopped := ""
	for i, receipt := range headReceipts {
		step := i + 1
		snap, snapOut, snapErr := s.replaySnapshot(ctx, sessionID)
		if snapErr != nil {
			stopped = fmt.Sprintf("step %d: stopped: failed to load snapshot: %v", step, snapErr)
			lines = append(lines, stopped)
			break
		}
		lastSnapshot = snap
		action := strings.TrimSpace(receipt.Action)
		v, supported := verbs[action]
		if !supported || !v.receipt || v.refs > 1 {
			stopped = fmt.Sprintf("step %d: stopped: action %q cannot be replayed", step, action)
			lines = append(lines, stopped)
			break
		}
		args := slices.Clone(receipt.Args)
		if v.refs > 0 {
			ref, ambiguous := replayRef(snapOut, receipt.TargetRole, receipt.TargetName)
			if ref == "" {
				why := "missing"
				if ambiguous {
					why = "ambiguous"
				}
				stopped = fmt.Sprintf("step %d: stopped: %s %s %q", step, why, receipt.TargetRole, receipt.TargetName)
				observationReason := fmt.Sprintf("%s %s %q in fresh snapshot", why, receipt.TargetRole, receipt.TargetName)
				if observation, observationErr := s.replayObservation(ctx, run, receipt, observationReason); observationErr == nil {
					lines = append(lines, replayReceiptLine(filepath.Join(s.store.root, run.ID), step, observation)+" stopped: "+why)
				} else {
					lines = append(lines, stopped+" (observation failed: "+observationErr.Error()+")")
				}
				break
			}
			if len(args) == 0 {
				stopped = fmt.Sprintf("step %d: stopped: %s has no target", step, action)
				lines = append(lines, stopped)
				break
			}
			args[0] = ref
		}
		args = append(args, "--leg", "base")
		out, actionErr := s.Drive(ctx, sessionID, action, args)
		if rec, ok := out["receipt"].(actionRec); ok {
			line := replayReceiptLine(filepath.Join(s.store.root, run.ID), step, rec)
			if actionErr != nil {
				reason := rec.Reason
				if reason == "" {
					reason = actionErr.Error()
				}
				line += " stopped: " + clip(strings.ReplaceAll(reason, "\n", " | "), 400)
			}
			if slices.ContainsFunc(rec.Warnings, func(w string) bool { return strings.HasPrefix(w, "load:") }) {
				line += " stopped: failed load"
			}
			lines = append(lines, line)
		}
		if snap, ok := out["snapshot"].(string); ok && snap != "" {
			lastSnapshot = snap
		}
		if actionErr != nil {
			reason := actionErr.Error()
			if rec, ok := out["receipt"].(actionRec); ok && rec.Reason != "" {
				reason = rec.Reason
			}
			stopped = fmt.Sprintf("step %d: stopped: %s", step, clip(strings.ReplaceAll(reason, "\n", " | "), 400))
			if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], fmt.Sprintf("step %d:", step)) {
				lines = append(lines, stopped)
			}
			break
		}
		if len(lines) > 0 && strings.Contains(lines[len(lines)-1], "stopped: failed load") {
			stopped = fmt.Sprintf("step %d: stopped: failed load", step)
			break
		}
	}
	result := map[string]any{"check": checkID, "baseCheck": baseCheckID, "leg": "base", "steps": lines, "snapshot": lastSnapshot}
	if stopped != "" {
		result["stopped"] = stopped
	}
	textLines := append([]string{"base check: " + baseCheckID}, lines...)
	result["text"] = strings.Join(append(textLines, compactReplaySnapshot(lastSnapshot)...), "\n")
	return result, nil
}

// replayObservation records the screen at a replay stop without trying the
// missing or ambiguous action. The base twin can then be ended as absent with
// the after-shot as its evidence.
func (s *Service) replayObservation(ctx context.Context, run Run, source Receipt, reason string) (actionRec, error) {
	leg, err := pickLeg(run, "base")
	if err != nil {
		return actionRec{}, err
	}
	e := s.engineFor(run.ID, leg)
	e.mu.Lock()
	defer e.mu.Unlock()
	target := refInfo{role: source.TargetRole, name: source.TargetName}
	rec := s.receipt(ctx, run.ID, e, leg.Name, "observe", nil, target, nil, nil, 0, reason)
	return rec, nil
}

// openReplayCheck makes the base twin ready to receive receipts. A pre-added
// pending twin is reused; an ended twin is never overwritten.

func (s *Service) openReplayCheck(runID string, head Check) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	checks, err := s.store.Checks(runID)
	if err != nil {
		return "", err
	}
	baseIndex := -1
	for i := range checks {
		if checks[i].onLeg() != "base" || !strings.EqualFold(checks[i].Name, head.Name) {
			continue
		}
		baseIndex = i
		break
	}
	for i, check := range checks {
		if check.State != CheckRunning || i == baseIndex {
			continue
		}
		return "", conflict("TESTING_CHECK_STATE", "check "+check.ID+" is still running; end it first")
	}
	if baseIndex >= 0 {
		if checks[baseIndex].State == CheckPending {
			checks[baseIndex].State = CheckRunning
			return checks[baseIndex].ID, s.store.SaveChecks(runID, checks)
		}
		if checks[baseIndex].State == CheckRunning {
			return checks[baseIndex].ID, nil
		}
		return "", conflict("TESTING_REPLAY_CHECK_STATE", "base check "+strconv.Quote(checks[baseIndex].ID)+" is already "+string(checks[baseIndex].State))
	}
	n := len(checks) + 1
	for {
		id := "c" + strconv.Itoa(n)
		if slices.IndexFunc(checks, func(c Check) bool { return c.ID == id }) < 0 {
			checks = append(checks, Check{ID: id, Name: head.Name, Expect: head.Expect, State: CheckRunning, Leg: "base"})
			return id, s.store.SaveChecks(runID, checks)
		}
		n++
	}
}

func (s *Service) replaySnapshot(ctx context.Context, sessionID string) (compact, raw string, err error) {
	out, err := s.Drive(ctx, sessionID, "snapshot", []string{"-i", "--leg", "base"})
	if err != nil {
		return "", "", err
	}
	raw, _ = out["output"].(string)
	compact, _ = out["snapshot"].(string)
	if compact == "" {
		compact = compactSnapshot(raw)
	}
	return compact, raw, nil
}

func replayRef(snapshot, role, name string) (string, bool) {
	refs := parseRefs(snapshot)
	keys := make([]string, 0, len(refs))
	for ref := range refs {
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	var matches []string
	for _, ref := range keys {
		info := refs[ref]
		if strings.EqualFold(strings.TrimSpace(info.role), strings.TrimSpace(role)) && accessibleName(info.name) == accessibleName(name) {
			matches = append(matches, strings.TrimPrefix(ref, "@"))
		}
	}
	if len(matches) == 1 {
		return matches[0], false
	}
	return "", len(matches) > 1
}

func accessibleName(s string) string { return strings.Join(strings.Fields(strings.TrimSpace(s)), " ") }

func replayReceiptLine(runDir string, step int, rec actionRec) string {
	after := rec.After
	if after == "" {
		after = "(none)"
	} else {
		after = relPath(runDir, after)
	}
	line := fmt.Sprintf("step %d: %s after %s %dms", step, rec.Action, after, rec.MS)
	if rec.PageErrors != "" {
		line += " page errors: " + strings.ReplaceAll(rec.PageErrors, "\n", " | ")
	}
	if rec.Reason != "" {
		line += " reason: " + rec.Reason
	}
	if rec.ConsoleTail != "" {
		line += " console: " + strings.ReplaceAll(rec.ConsoleTail, "\n", " | ")
	}
	return line
}

func compactReplaySnapshot(snapshot string) []string {
	if strings.TrimSpace(snapshot) == "" {
		return nil
	}
	return []string{"final snapshot:", snapshot}
}
