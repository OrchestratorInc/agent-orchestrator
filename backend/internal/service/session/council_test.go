package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func councilErrorCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not an *apierr.Error", err)
	}
	return apiErr.Code
}

func TestSpawnCouncilValidation(t *testing.T) {
	t.Parallel()
	svc := &Service{}
	ctx := context.Background()
	valid := domain.PermissionMode("default")

	cases := []struct {
		name string
		in   ports.CouncilInput
		code string
	}{
		{
			name: "too few members",
			in:   ports.CouncilInput{ApprovalMode: valid, Members: []ports.CouncilMember{{Harness: "codex"}}},
			code: "COUNCIL_MIN_MEMBERS",
		},
		{
			name: "too many members",
			in: ports.CouncilInput{ApprovalMode: valid, Members: func() []ports.CouncilMember {
				out := make([]ports.CouncilMember, 0, councilMaxMembers+1)
				for i := 0; i < councilMaxMembers+1; i++ {
					out = append(out, ports.CouncilMember{Harness: "codex"})
				}
				return out
			}()},
			code: "COUNCIL_MAX_MEMBERS",
		},
		{
			name: "missing harness",
			in:   ports.CouncilInput{ApprovalMode: valid, Members: []ports.CouncilMember{{Harness: "codex"}, {Harness: "  "}}},
			code: "COUNCIL_MEMBER_HARNESS_REQUIRED",
		},
		{
			name: "invalid approval mode",
			in:   ports.CouncilInput{ApprovalMode: domain.PermissionMode("bogus"), Members: []ports.CouncilMember{{Harness: "codex"}, {Harness: "claude-code"}}},
			code: "INVALID_APPROVAL_MODE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.SpawnCouncil(ctx, tc.in)
			if got := councilErrorCode(t, err); got != tc.code {
				t.Fatalf("SpawnCouncil code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestNewCouncilGroupIDIsUniqueAndPrefixed(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id, err := newCouncilGroupID()
		if err != nil {
			t.Fatalf("newCouncilGroupID: %v", err)
		}
		if !strings.HasPrefix(id, "council_") {
			t.Fatalf("group id %q missing council_ prefix", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate group id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestCouncilMemberNameStaysWithinCap(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200)
	name := councilMemberName(long, domain.AgentHarness("claude-code"))
	if utf8.RuneCountInString(name) > councilDisplayNameMax {
		t.Fatalf("name length = %d, want <= %d", utf8.RuneCountInString(name), councilDisplayNameMax)
	}
	if !strings.HasSuffix(name, "· claude-code") {
		t.Fatalf("name %q dropped the harness suffix", name)
	}
	short := councilMemberName("Compare", domain.AgentHarness("codex"))
	if short != "Compare · codex" {
		t.Fatalf("name = %q, want %q", short, "Compare · codex")
	}
}
