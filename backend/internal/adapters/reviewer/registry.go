package reviewer

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/amp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/auggie"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/crush"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/devin"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/grok"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/kilocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/kimchi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/kiro"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/pi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Adapter interface {
	ports.Reviewer
	Harness() domain.ReviewerHarness
}

func Constructors() []Adapter {
	return []Adapter{
		aider.New(),
		agy.New(),
		amp.New(),
		auggie.New(),
		autohand.New(),
		claudecode.New(),
		cline.New(),
		codex.New(),
		copilot.New(),
		cursor.New(),
		devin.New(),
		droid.New(),
		crush.New(),
		grok.New(),
		kimchi.New(),
		kilocode.New(),
		kiro.New(),
		kimi.New(),
		muse.New(),
		opencode.New(),
		opencodev2.New(),
		pi.New(),
	}
}

type Resolver struct {
	reviewers map[domain.ReviewerHarness]ports.Reviewer
}

var _ ports.ReviewerResolver = (*Resolver)(nil)

func NewResolver() (*Resolver, error) {
	m := make(map[domain.ReviewerHarness]ports.Reviewer)
	for _, a := range Constructors() {
		h := a.Harness()
		if !h.IsKnown() {
			return nil, fmt.Errorf("reviewer adapter %q is not in domain.AllReviewerHarnesses", h)
		}
		if _, dup := m[h]; dup {
			return nil, fmt.Errorf("reviewer harness %q is registered twice", h)
		}
		m[h] = a
	}
	for _, harness := range domain.AllReviewerHarnesses {
		if _, ok := m[harness]; !ok {
			return nil, fmt.Errorf("reviewer harness %q has no registered adapter", harness)
		}
	}
	return &Resolver{reviewers: m}, nil
}

func (r *Resolver) Reviewer(harness domain.ReviewerHarness) (ports.Reviewer, bool) {
	rv, ok := r.reviewers[harness]
	return rv, ok
}

func (r *Resolver) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if invalidator, ok := r.reviewers[domain.ReviewerHarness(harness)].(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}
