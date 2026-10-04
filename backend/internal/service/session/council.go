package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// councilMinMembers is the smallest cohort that is actually a council: one
	// brief needs at least two models to be worth comparing side by side.
	councilMinMembers = 2
	// councilMaxMembers bounds a single fan-out so one request cannot spawn an
	// unbounded number of worktrees/agents at once.
	councilMaxMembers = 8
	// councilDisplayNameMax mirrors the per-session display-name cap so a member
	// label built from the base name plus its harness never exceeds it.
	councilDisplayNameMax = 100
)

// SpawnCouncil fans one brief out to several harnesses/models, creating one
// worker session per member and tagging them all with a shared council group id
// so clients can present the cohort for side-by-side comparison. The fan-out is
// best-effort: a member that fails to spawn is reported in its own result rather
// than discarding the members that started, so a partial council is still usable.
func (s *Service) SpawnCouncil(ctx context.Context, in ports.CouncilInput) (ports.CouncilResult, error) {
	members := in.Members
	if len(members) < councilMinMembers {
		return ports.CouncilResult{}, apierr.Invalid("COUNCIL_MIN_MEMBERS", fmt.Sprintf("A council needs at least %d models", councilMinMembers), nil)
	}
	if len(members) > councilMaxMembers {
		return ports.CouncilResult{}, apierr.Invalid("COUNCIL_MAX_MEMBERS", fmt.Sprintf("A council supports at most %d models", councilMaxMembers), nil)
	}
	for i, member := range members {
		if strings.TrimSpace(string(member.Harness)) == "" {
			return ports.CouncilResult{}, apierr.Invalid("COUNCIL_MEMBER_HARNESS_REQUIRED", fmt.Sprintf("Council member %d is missing a harness", i+1), nil)
		}
	}
	if !in.ApprovalMode.Valid() {
		return ports.CouncilResult{}, apierr.Invalid("INVALID_APPROVAL_MODE", "approvalMode is invalid", nil)
	}

	groupID, err := newCouncilGroupID()
	if err != nil {
		return ports.CouncilResult{}, apierr.Internal("COUNCIL_ID_FAILED", "Could not allocate a council id")
	}
	base := strings.TrimSpace(in.DisplayName)
	if base == "" {
		base = "Council"
	}

	result := ports.CouncilResult{GroupID: groupID, Members: make([]ports.CouncilMemberResult, 0, len(members))}
	for _, member := range members {
		cfg := ports.SpawnConfig{
			ProjectID:      in.ProjectID,
			Kind:           domain.KindWorker,
			Harness:        member.Harness,
			RequestedMode:  in.RequestedMode,
			Prompt:         in.Prompt,
			DisplayName:    councilMemberName(base, member.Harness),
			CouncilGroupID: groupID,
			EffortOverride: member.EffortOverride,
			AgentConfig:    ports.AgentConfig{Model: member.Model, Effort: member.Effort, Permissions: in.ApprovalMode},
			Attachments:    in.Attachments,
		}
		sess, _, _, spawnErr := s.Spawn(ctx, cfg)
		result.Members = append(result.Members, ports.CouncilMemberResult{
			Harness: member.Harness,
			Model:   member.Model,
			Session: sess,
			Err:     spawnErr,
		})
	}
	return result, nil
}

func newCouncilGroupID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "council_" + hex.EncodeToString(raw[:]), nil
}

// councilMemberName builds "<base> · <harness>" without exceeding the display
// name cap, trimming the base first so the harness stays visible.
func councilMemberName(base string, harness domain.AgentHarness) string {
	suffix := " · " + string(harness)
	limit := councilDisplayNameMax - utf8.RuneCountInString(suffix)
	if limit <= 0 {
		return truncateRunes(string(harness), councilDisplayNameMax)
	}
	return truncateRunes(base, limit) + suffix
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
