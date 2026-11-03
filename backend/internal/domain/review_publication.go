package domain

// ReviewPublication records the reviewer's provider readback separately from
// the local verdict. AO records this receipt; it does not perform the writes or
// independently attest to provider publication or worker delivery.
type ReviewPublication struct {
	// Provider identifies the SCM provider for this receipt.
	Provider string `json:"provider" enum:"github,gitlab"`
	// Status is published only after complete provider readback; blocked means
	// no publication was possible, and unconfirmed means a write may have happened.
	Status string `json:"status" enum:"published,blocked,unconfirmed"`
	// PRURL binds all references to one PR/MR, including its host and project.
	PRURL string `json:"prUrl"`
	// TargetSHA is the head that the reviewer checked before writing.
	TargetSHA string `json:"targetSha"`
	// Refs contains provider object identifiers, scoped by PRURL. GitLab has no
	// aggregate review ID: its summary note and diff discussions are separate.
	Refs []ReviewPublicationRef `json:"refs,omitempty"`
	// Error explains blocked or uncertain publication without changing the verdict.
	Error string `json:"error,omitempty"`
}

// ReviewPublicationRef identifies one provider object within a receipt's target.
type ReviewPublicationRef struct {
	// Kind identifies a GitHub review or a GitLab note, discussion or pending draft.
	Kind string `json:"kind" enum:"review,note,discussion,draft_note"`
	// ID is the provider's real object ID, never a fabricated aggregate identifier.
	ID string `json:"id"`
}

// PublishedToProvider reports a recorded, confirmed publication. Legacy GitHub
// results retain their review-ID behavior; missing GitLab receipts never count.
func (r ReviewRun) PublishedToProvider() bool {
	if r.Publication != nil {
		return r.Publication.Status == "published"
	}
	ref, err := ParseChangeRequestURL(r.PRURL)
	return err == nil && ref.Provider == "github" && r.GithubReviewID != ""
}
