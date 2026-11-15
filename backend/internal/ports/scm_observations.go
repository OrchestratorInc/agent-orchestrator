package ports

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var ErrSCMNotFound = errors.New("scm: not found")

type SCMRepo struct {
	Provider string
	Host     string
	Owner    string
	Name     string
	Repo     string
}

type SCMPRRef struct {
	Repo             SCMRepo
	Number           int
	URL              string
	DetailsUpdatedAt time.Time
}

type SCMGuardResult struct {
	ETag        string
	NotModified bool
}

type SCMObservation struct {
	Fetched    bool
	ObservedAt time.Time

	Provider string
	Host     string
	Repo     string

	PR           SCMPRObservation
	CI           SCMCIObservation
	Review       SCMReviewObservation
	Mergeability SCMMergeabilityObservation

	Error error

	Changed SCMChanged
}

type SCMChanged struct {
	Metadata bool
	CI       bool
	Review   bool
}

type SCMIdentity struct {
	Login string
	Human bool
}

func (id SCMIdentity) Authored(author string) bool {
	login := strings.TrimSpace(id.Login)
	return id.Human && login != "" && strings.EqualFold(strings.TrimSpace(author), login)
}

type ScopedIdentityResolver interface {
	AuthenticatedIdentityForProvider(ctx context.Context, provider, host string) (SCMIdentity, error)
}

type SCMPRObservation struct {
	ProviderID      string
	URL             string
	URLAlias        string
	Number          int
	State           string
	Draft           bool
	Merged          bool
	Closed          bool
	SourceBranch    string
	HeadRepo        string
	BaseRepo        string
	TargetBranch    string
	HeadSHA         string
	Title           string
	Additions       int
	Deletions       int
	ChangedFiles    int
	Author          string
	AuthorAvatarURL string
	BaseSHA         string
	MergeCommitSHA  string

	ProviderState            string
	ProviderMergeable        string
	ProviderMergeStateStatus string
	HTMLURL                  string

	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
	MergedAtProvider  time.Time
	ClosedAtProvider  time.Time

	Details        *domain.PullRequestDetails `json:"-"`
	DetailsPending bool                       `json:"-"`
}

type SCMCIObservation struct {
	Summary           string
	HeadSHA           string
	FailedFingerprint string
	Checks            []SCMCheckObservation
	FailedChecks      []SCMCheckObservation
	FailureLogTail    string
	Partial           bool
}

func (ci *SCMCIObservation) ApplyStoredLogTails(checks []domain.PullRequestCheck) bool {
	tailsByName := map[string]string{}
	for _, ch := range checks {
		if ci.HeadSHA != "" && ch.CommitHash != "" && ch.CommitHash != ci.HeadSHA {
			continue
		}
		if ch.LogTail != "" && ch.Status == domain.PRCheckFailed {
			tailsByName[ch.Name] = ch.LogTail
		}
	}
	if len(tailsByName) == 0 {
		return false
	}
	tails := make([]string, 0, len(ci.FailedChecks))
	for i := range ci.FailedChecks {
		tail := tailsByName[ci.FailedChecks[i].Name]
		if tail == "" {
			return false
		}
		tails = append(tails, tail)
	}
	for i := range ci.FailedChecks {
		ci.FailedChecks[i].LogTail = tailsByName[ci.FailedChecks[i].Name]
	}
	for i := range ci.Checks {
		if tail := tailsByName[ci.Checks[i].Name]; tail != "" {
			ci.Checks[i].LogTail = tail
		}
	}
	ci.FailureLogTail = strings.Join(tails, "\n---\n")
	return true
}

type SCMCheckObservation struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
	LogTail    string
	ProviderID string
}

type SCMReviewObservation struct {
	Decision string
	Reviews  []SCMReviewSummaryObservation
	Threads  []SCMReviewThreadObservation
	Partial  bool
}

type SCMReviewSummaryObservation struct {
	ID          string
	Author      string
	State       string
	URL         string
	Body        string
	TargetSHA   string
	IsBot       bool
	SubmittedAt time.Time
}

type SCMReviewThreadObservation struct {
	ID       string
	Path     string
	Line     int
	Resolved bool
	IsBot    bool
	Comments []SCMReviewCommentObservation
}

type SCMReviewCommentObservation struct {
	ID        string
	ReviewID  string
	Author    string
	Body      string
	URL       string
	IsBot     bool
	CreatedAt time.Time
}

type SCMMergeabilityObservation struct {
	State      string
	Mergeable  bool
	Conflict   bool
	BehindBase bool
	Blockers   []string
}

type GitHubRepository struct {
	ID             int64
	FullName       string
	OwnerAvatarURL string
}

type GitHubRepositoryResolver interface {
	ResolveGitHubRepository(ctx context.Context, owner, name string) (GitHubRepository, error)
}
