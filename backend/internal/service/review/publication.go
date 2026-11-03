package review

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// validatePublication copies and canonicalizes a receipt before storage. It does
// not contact the provider: the reviewer owns publication and its readback.
func validatePublication(run domain.ReviewRun, submitted SubmittedReview) (*domain.ReviewPublication, string, error) {
	if submitted.Publication == nil {
		return nil, submitted.GithubReviewID, nil
	}
	p := *submitted.Publication
	target, err := domain.ParseChangeRequestURL(run.PRURL)
	if err != nil {
		return nil, "", fmt.Errorf("publication requires a valid run target")
	}
	ref, err := domain.ParseChangeRequestURL(p.PRURL)
	if err != nil || ref.Key() != target.Key() || p.Provider != target.Provider || p.TargetSHA == "" || p.TargetSHA != run.TargetSHA {
		return nil, "", fmt.Errorf("publication provider, target and head must match the review run")
	}
	p.PRURL = target.URL
	switch p.Status {
	case "published":
		if p.Error != "" {
			return nil, "", fmt.Errorf("published receipt must not include a publication error")
		}
	case "blocked", "unconfirmed":
		if strings.TrimSpace(p.Error) == "" {
			return nil, "", fmt.Errorf("blocked or unconfirmed publication requires an error")
		}
	default:
		return nil, "", fmt.Errorf("publication status must be published, blocked or unconfirmed")
	}
	if len(p.Refs) > 256 || len(p.Error) > 16384 {
		return nil, "", fmt.Errorf("publication receipt exceeds limits")
	}
	p.Refs = slices.Clone(p.Refs)
	seen := make(map[domain.ReviewPublicationRef]bool, len(p.Refs))
	var reviewID string
	noteCount, hasDiscussion := 0, false
	for _, object := range p.Refs {
		if object.ID == "" || strings.IndexFunc(object.ID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || len(object.ID) > 256 || seen[object] {
			return nil, "", fmt.Errorf("publication references require unique, nonempty provider IDs")
		}
		seen[object] = true
		if p.Provider == "github" {
			if object.Kind != "review" || reviewID != "" {
				return nil, "", fmt.Errorf("GitHub receipt requires one review reference")
			}
			reviewID = object.ID
		} else {
			switch object.Kind {
			case "note":
				noteCount++
			case "discussion":
				hasDiscussion = true
			case "draft_note":
				if p.Status == "published" {
					return nil, "", fmt.Errorf("published receipt cannot contain pending drafts")
				}
			default:
				return nil, "", fmt.Errorf("GitLab receipt requires note, discussion or draft_note references")
			}
		}
	}
	if p.Status == "published" {
		if (p.Provider == "github" && reviewID == "") || (p.Provider == "gitlab" && (noteCount == 0 || (submitted.Verdict == domain.VerdictChangesRequested && (!hasDiscussion || noteCount < 2)))) {
			return nil, "", fmt.Errorf("published receipt requires summary/review and required diff discussion references")
		}
	} else if submitted.GithubReviewID != "" {
		return nil, "", fmt.Errorf("unconfirmed or blocked publication must not carry a confirmed GitHub review id")
	}
	if p.Provider == "github" && p.Status == "published" {
		if submitted.GithubReviewID != "" && submitted.GithubReviewID != reviewID {
			return nil, "", fmt.Errorf("GitHub review reference disagrees with githubReviewId")
		}
	} else {
		reviewID = ""
	}
	slices.SortFunc(p.Refs, func(a, b domain.ReviewPublicationRef) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(p.Refs) == 0 {
		p.Refs = nil
	}
	return &p, reviewID, nil
}
