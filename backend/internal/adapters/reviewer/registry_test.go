package reviewer

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestRegistryMatchesDomainVocabulary enforces that the shipped reviewer
// adapters and domain.AllReviewerHarnesses stay in sync: every registered
// adapter is a known reviewer harness, and every known harness has an adapter.
func TestRegistryMatchesDomainVocabulary(t *testing.T) {
	registered := map[domain.ReviewerHarness]bool{}
	oneShotReviewers := map[domain.ReviewerHarness]bool{
		domain.ReviewerAider:  true,
		domain.ReviewerAuggie: true,
		domain.ReviewerDroid:  true,
	}
	for _, a := range Constructors() {
		h := a.Harness()
		if !h.IsKnown() {
			t.Errorf("adapter harness %q is not in domain.AllReviewerHarnesses", h)
		}
		if registered[h] {
			t.Errorf("reviewer harness %q registered twice", h)
		}
		if _, ok := a.(ports.ReviewerRestorer); !ok {
			t.Errorf("reviewer harness %q does not implement restore", h)
		}
		canceller, ok := a.(ports.ReviewerCanceller)
		if !ok {
			t.Errorf("reviewer harness %q does not implement cancellation", h)
		} else if spec, err := canceller.ReviewCancel(context.Background()); err != nil {
			t.Errorf("reviewer harness %q cancel spec: %v", h, err)
		} else {
			switch h {
			case domain.ReviewerCodex, domain.ReviewerKiro, domain.ReviewerPi, domain.ReviewerMuse:
				if spec.Mode != ports.ReviewCancelInput {
					t.Errorf("reviewer harness %q cancel mode = %q, want %q", h, spec.Mode, ports.ReviewCancelInput)
				}
				if spec.Input != "\x1b" || len(spec.Inputs) != 0 {
					t.Errorf("reviewer harness %q cancel input = %q inputs=%#v, want single escape", h, spec.Input, spec.Inputs)
				}
			case domain.ReviewerClaudeCode, domain.ReviewerOpenCode, domain.ReviewerOpenCodeV2:
				if spec.Mode != ports.ReviewCancelInput {
					t.Errorf("reviewer harness %q cancel mode = %q, want %q", h, spec.Mode, ports.ReviewCancelInput)
				}
				if len(spec.Inputs) != 2 || spec.Inputs[0] != "\x1b" || spec.Inputs[1] != "\x1b" {
					t.Errorf("reviewer harness %q cancel inputs = %#v, want double escape", h, spec.Inputs)
				}
			case domain.ReviewerAgy, domain.ReviewerDevin, domain.ReviewerDroid:
				if spec.Mode != ports.ReviewCancelInterrupt {
					t.Errorf("reviewer harness %q cancel mode = %q, want %q", h, spec.Mode, ports.ReviewCancelInterrupt)
				}
				if spec.Interrupts != 1 {
					t.Errorf("reviewer harness %q cancel interrupts = %d, want 1", h, spec.Interrupts)
				}
			default:
				if spec.Mode != ports.ReviewCancelInterrupt {
					t.Errorf("reviewer harness %q cancel mode = %q, want %q", h, spec.Mode, ports.ReviewCancelInterrupt)
				}
				if spec.Interrupts != 2 {
					t.Errorf("reviewer harness %q cancel interrupts = %d, want 2", h, spec.Interrupts)
				}
			}
		}
		policy, hasPolicy := a.(ports.ReviewerReusePolicy)
		reusable := !hasPolicy || policy.ReviewProcessReusable()
		if oneShotReviewers[h] == reusable {
			t.Errorf("reviewer harness %q reusable = %v, want %v", h, reusable, !oneShotReviewers[h])
		}
		registered[h] = true
	}
	for _, h := range domain.AllReviewerHarnesses {
		if !registered[h] {
			t.Errorf("reviewer harness %q has no registered adapter", h)
		}
	}
}

func TestNewResolverResolvesShippedReviewers(t *testing.T) {
	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, h := range domain.AllReviewerHarnesses {
		if _, ok := resolver.Reviewer(h); !ok {
			t.Errorf("resolver missing reviewer %q", h)
		}
	}
	if _, ok := resolver.Reviewer("nope"); ok {
		t.Error("resolver returned an adapter for an unknown harness")
	}
	for _, removed := range []domain.ReviewerHarness{"continue", "goose", "vibe", "qwen"} {
		if _, ok := resolver.Reviewer(removed); ok {
			t.Errorf("resolver returned removed reviewer %q", removed)
		}
	}
}

type cachingReviewer struct {
	ports.Reviewer
	invalidated int
}

func (r *cachingReviewer) InvalidateBinaryResolution() { r.invalidated++ }

// Reviewers launch through worker plugins of their own; an install must clear
// their cached executable too, or new reviews keep running the old binary.
func TestResolverInvalidatesOneReviewersBinary(t *testing.T) {
	codex, claude := &cachingReviewer{}, &cachingReviewer{}
	resolver := &Resolver{reviewers: map[domain.ReviewerHarness]ports.Reviewer{
		domain.ReviewerCodex: codex, domain.ReviewerClaudeCode: claude,
	}}

	resolver.InvalidateBinaryResolution(domain.HarnessCodex)
	resolver.InvalidateBinaryResolution(domain.HarnessAider)

	if codex.invalidated != 1 || claude.invalidated != 0 {
		t.Fatalf("invalidations codex=%d claude=%d, want 1 and 0", codex.invalidated, claude.invalidated)
	}
}

// Every reviewer built on a caching worker plugin forwards the invalidation.
func TestCachingReviewersForwardBinaryInvalidation(t *testing.T) {
	for _, harness := range []domain.ReviewerHarness{
		domain.ReviewerClaudeCode, domain.ReviewerCodex, domain.ReviewerCopilot, domain.ReviewerCursor,
		domain.ReviewerKimchi, domain.ReviewerOpenCode, domain.ReviewerKiloCode, domain.ReviewerOpenCodeV2,
	} {
		resolver, err := NewResolver()
		if err != nil {
			t.Fatal(err)
		}
		reviewer, _ := resolver.Reviewer(harness)
		if _, ok := reviewer.(ports.AgentBinaryResolutionInvalidator); !ok {
			t.Errorf("%s reviewer does not forward binary invalidation", harness)
		}
	}
}

// unverified marks a level taken from the adapter's launch code alone: the
// harness was not installed when the level was set, so no live probe run
// confirmed it.
const unverified = "unverified: level read from the adapter's launch code, not run live"

// TestEveryReviewerDeclaresPolicyEnforcement pins how the #6842 reviewer
// policy is enforced for each harness, and the evidence for that level, so
// changing a level (or registering a reviewer) is a deliberate, reviewed
// decision rather than a silent default. "Verified" means the conformance
// probes (ao spawn/send/kill/stop, writes, commits, pushes, a legitimate
// submit) were run live against that harness version.
func TestEveryReviewerDeclaresPolicyEnforcement(t *testing.T) {
	type pin struct {
		level    domain.ReviewerEnforcement
		evidence string
	}
	want := map[domain.ReviewerHarness]pin{
		domain.ReviewerClaudeCode: {domain.ReviewerEnforcedHook, "verified live: Claude Code 2.1.310"},
		domain.ReviewerCodex:      {domain.ReviewerEnforcedSandbox, "verified live: codex-cli 0.154 (Terminal and Chat; sandbox plus review-guard hook)"},
		domain.ReviewerCursor:     {domain.ReviewerUserApproved, "verified live: Cursor CLI 2026.10.22 (asks before commands; reviewer profile not applied, #7297)"},
		domain.ReviewerCopilot:    {domain.ReviewerEnforcedConfig, "verified live: Copilot CLI 1.0.98 (commit/push denied; gh api cannot be scoped)"},
		domain.ReviewerOpenCode:   {domain.ReviewerEnforcedConfig, "verified live: OpenCode 1.19.3 (submit with any review text; redirects denied)"},
		domain.ReviewerOpenCodeV2: {domain.ReviewerEnforcedConfig, "verified live: OpenCode 2.0.20"},
		domain.ReviewerAutohand:   {domain.ReviewerUserApproved, "verified live: Autohand 0.9.8 (asks before every command; declining blocks it)"},
		domain.ReviewerAgy:        {domain.ReviewerHostTrusted, "verified live: Agy 1.4.7 asks before commands, but its sandbox is not OS isolation and offers always-allow"},
		domain.ReviewerKiloCode:   {domain.ReviewerEnforcedConfig, unverified},
		domain.ReviewerKiro:       {domain.ReviewerEnforcedConfig, unverified},
		domain.ReviewerPi:         {domain.ReviewerEnforcedGateway, unverified},
		domain.ReviewerAmp:        {domain.ReviewerUserApproved, unverified},
		domain.ReviewerAider:      {domain.ReviewerUserApproved, unverified},
		domain.ReviewerAuggie:     {domain.ReviewerUserApproved, unverified},
		domain.ReviewerCline:      {domain.ReviewerUserApproved, unverified},
		domain.ReviewerCrush:      {domain.ReviewerUserApproved, unverified},
		domain.ReviewerGrok:       {domain.ReviewerUserApproved, unverified},
		domain.ReviewerMuse:       {domain.ReviewerHostTrusted, unverified},
		domain.ReviewerDevin:      {domain.ReviewerHostTrusted, unverified},
		domain.ReviewerDroid:      {domain.ReviewerHostTrusted, unverified},
		domain.ReviewerKimchi:     {domain.ReviewerHostTrusted, unverified},
		domain.ReviewerKimi:       {domain.ReviewerHostTrusted, unverified},
	}
	adapters := Constructors()
	if len(adapters) != len(want) {
		t.Fatalf("%d reviewers registered, %d pinned: pin the new reviewer's enforcement and evidence here", len(adapters), len(want))
	}
	for _, a := range adapters {
		got := a.ReviewEnforcement()
		pinned, ok := want[a.Harness()]
		if !ok || pinned.evidence == "" {
			t.Errorf("%s has no pinned enforcement evidence", a.Harness())
			continue
		}
		if !got.IsKnown() {
			t.Errorf("%s declares unknown enforcement %q", a.Harness(), got)
		}
		if got != pinned.level {
			t.Errorf("%s enforcement = %q, want %q (%s)", a.Harness(), got, pinned.level, pinned.evidence)
		}
	}
}
