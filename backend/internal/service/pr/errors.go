package pr

import "errors"

var (
	ErrInvalidPR                = errors.New("pr: invalid identity")
	ErrPRNotFound               = errors.New("pr: not found")
	ErrPRNotMergeable           = errors.New("pr: not mergeable")
	ErrPRStacked                = errors.New("pr: unmerged layers below it in its stack")
	ErrPRHeadChanged            = errors.New("pr: head changed")
	ErrPRPreconditions          = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve         = errors.New("pr: nothing to resolve")
	ErrInvalidAction            = errors.New("pr: invalid action")
	ErrInvalidTransition        = errors.New("pr: invalid state transition")
	ErrPRRejected               = errors.New("pr: provider rejected mutation")
	ErrInvalidComment           = errors.New("pr: invalid comment")
	ErrThreadNotFound           = errors.New("pr: review thread not found")
	ErrUnsupported              = errors.New("pr: unsupported by provider")
	ErrInvalidAttachment        = errors.New("pr: invalid attachment")
	ErrAttachmentTooLarge       = errors.New("pr: attachment too large")
	ErrAttachmentTypeNotAllowed = errors.New("pr: attachment type not allowed")
	ErrAttachmentsForbidden     = errors.New("pr: no write access for attachments")
)
