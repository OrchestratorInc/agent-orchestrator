// This file defines provider-neutral Tracker DTOs used at the boundary between
// the (future) Tracker observer, persistence layer, and lifecycle manager. The
// shape mirrors ports.SCMObservation so the lifecycle reducer in
// lifecycle.Manager has the same "Fetched + ObservedAt + normalized facts +
// Changed discriminator" contract for both lanes.
package ports

import (
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// TrackerObservation is the provider-neutral issue observation emitted by the
// Tracker observer and consumed by lifecycle.Manager.ApplyTrackerFacts.
// Provider adapters normalize their tracker-specific payloads into this DTO
// before the observer persists/notifies.
type TrackerObservation struct {
	// Fetched is true only when the provider refresh succeeded and the nested
	// facts are authoritative for this poll.
	Fetched bool
	// ObservedAt is the observer timestamp for this normalized snapshot.
	ObservedAt time.Time

	// Provider is the normalized tracker provider name, e.g. "github".
	Provider string
	// Host is the tracker host that served this observation.
	Host string
	// Repo is the full repository/project name shown to AO users, usually
	// "owner/name" for GitHub-issue trackers.
	Repo string

	// Issue contains the normalized issue facts (state, assignee, title, body).
	Issue TrackerIssueObservation
	// Comments contains the normalized comments observed on the issue. The
	// observer is responsible for windowing/dedup; lifecycle treats every
	// entry as a fact about the current snapshot.
	Comments []TrackerCommentObservation

	// Changed marks which semantic buckets changed compared with the DB snapshot.
	Changed TrackerChanged
}

// TrackerChanged marks which semantic state buckets changed in the successful
// poll. The discriminator lets lifecycle skip work cheaply when only one
// bucket moved; today it also lets the reducer fire reactions on the right
// edges (assignee-only change vs comment-only change).
type TrackerChanged struct {
	// State is true when Issue.State changed since the last persisted snapshot.
	State bool
	// Assignee is true when Issue.Assignee changed since the last persisted snapshot.
	Assignee bool
	// Comments is true when the comment set changed (new comment, edit, or removal).
	Comments bool
}

// TrackerIssueObservation carries the normalized issue facts. The field set is
// deliberately the minimum that lifecycle reactions need today; provider
// adapters keep richer per-provider metadata behind their own packages.
type TrackerIssueObservation struct {
	// URL is the canonical issue browser URL used as the persistence key.
	URL string
	// Number is the provider's issue number within the repository/project.
	Number int
	// State is AO's normalized issue state from domain.NormalizedIssueState
	// (open, in_progress, review, done, cancelled).
	State domain.NormalizedIssueState
	// Title is the provider issue title.
	Title string
	// Body is the issue description as plain text/markdown.
	Body string
	// Assignee is the login/identifier of the currently primary assignee, or
	// "" when the issue is unassigned. Multi-assignee tracking is not part of
	// the lifecycle contract today.
	Assignee string
	// Author is the login/name of the issue author.
	Author string
	// Labels is the normalized label set on the issue.
	Labels []string

	// CreatedAtProvider is the provider's issue creation timestamp.
	CreatedAtProvider time.Time
	// UpdatedAtProvider is the provider's last issue update timestamp.
	UpdatedAtProvider time.Time
	// ClosedAtProvider is the provider's close timestamp when the issue is closed.
	ClosedAtProvider time.Time
}

// TrackerCommentObservation is one normalized issue comment.
type TrackerCommentObservation struct {
	// ID is the provider's stable comment identifier.
	ID string
	// Author is the provider login/name of the commenter.
	Author string
	// Body is the comment text.
	Body string
	// URL is a provider link to the comment.
	URL string
	// IsBot is true when the provider identifies the author as a bot. The
	// lifecycle reducer treats new bot comments as actionable nudges.
	IsBot bool
	// CreatedAtProvider is the provider's comment creation timestamp.
	CreatedAtProvider time.Time
	// UpdatedAtProvider is the provider's last comment update timestamp when
	// the provider exposes one.
	UpdatedAtProvider time.Time
}
