package pr

import "errors"

// Sentinel errors returned by the PR action service.
var (
	ErrInvalidPR        = errors.New("pr: invalid identity")
	ErrPRNotFound       = errors.New("pr: not found")
	ErrPRNotMergeable   = errors.New("pr: not mergeable")
	ErrPRHeadChanged    = errors.New("pr: head changed")
	ErrPRPreconditions  = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve = errors.New("pr: nothing to resolve")
	// ErrInvalidAction is an unknown merge method or state action.
	ErrInvalidAction = errors.New("pr: invalid action")
	// ErrInvalidTransition is a state action the PR's current state does not allow.
	ErrInvalidTransition = errors.New("pr: invalid state transition")
	// ErrPRRejected is the provider refusing a well-formed mutation.
	ErrPRRejected = errors.New("pr: provider rejected mutation")
	// ErrInvalidComment is an empty or oversized comment body.
	ErrInvalidComment = errors.New("pr: invalid comment")
	// ErrThreadNotFound is a review thread id that is not part of the PR.
	ErrThreadNotFound = errors.New("pr: review thread not found")
	// ErrUnsupported is a mutation the PR's SCM provider cannot express.
	ErrUnsupported = errors.New("pr: unsupported by provider")
	// ErrInvalidAttachment is an attachment with no name or no content.
	ErrInvalidAttachment = errors.New("pr: invalid attachment")
	// ErrAttachmentTooLarge is an attachment over MaxAttachmentBytes.
	ErrAttachmentTooLarge = errors.New("pr: attachment too large")
	// ErrAttachmentTypeNotAllowed is an attachment whose declared type, file
	// extension, or content is outside the allowlist or disagrees with the others.
	ErrAttachmentTypeNotAllowed = errors.New("pr: attachment type not allowed")
)
