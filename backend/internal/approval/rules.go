// Package approval pauses consequential agent actions until the user allows
// or denies them, and records every gated action in an audit log.
//
// The rules that decide what needs approval live in backend/pkg/approval so
// AO Cloud applies the same ones; this file re-exports them. Gate holds a
// request open until the user answers it in the app or it times out (a
// timeout is a deny). Other features, such as the credential vault's "first
// use of a saved login on this site", call Gate.Request directly with their
// own Kind and Category.
package approval

import rules "github.com/aoagents/agent-orchestrator/backend/pkg/approval"

type (
	// Mode is how much a session asks before acting.
	Mode = rules.Mode
	// Target describes what a browser action would act on.
	Target = rules.Target
	// Verdict is the explainable result of Classify.
	Verdict = rules.Verdict
)

// Request kinds, modes and categories, as the public rules define them.
const (
	KindBrowserAction = rules.KindBrowserAction
	KindSavedLogin    = rules.KindSavedLogin

	ModeOff         = rules.ModeOff
	ModeRisky       = rules.ModeRisky
	ModeEverySubmit = rules.ModeEverySubmit

	CategoryPayment    = rules.CategoryPayment
	CategoryMessage    = rules.CategoryMessage
	CategoryPublish    = rules.CategoryPublish
	CategoryDelete     = rules.CategoryDelete
	CategorySecurity   = rules.CategorySecurity
	CategoryAccount    = rules.CategoryAccount
	CategorySubmit     = rules.CategorySubmit
	CategoryCredential = rules.CategoryCredential
	CategoryScript     = rules.CategoryScript
)

var (
	// Classify decides whether target needs approval under mode.
	Classify = rules.Classify
	// ClassifyDialog judges accepting a page dialog.
	ClassifyDialog = rules.ClassifyDialog
	// Truncate cuts text to at most max characters, never splitting one.
	Truncate = rules.Truncate
	// MayNeedApproval reports whether a browser command can ask the user,
	// before anything about the page is read.
	MayNeedApproval = rules.MayNeedApproval
	// TypesLineBreak reports a type whose text holds a line break (an Enter).
	TypesLineBreak = rules.TypesLineBreak
	// ValidMode reports whether m is a known mode.
	ValidMode = rules.ValidMode
	// Site returns the host a page URL belongs to.
	Site = rules.Site
	// IsEnterKey reports whether a press dispatches Enter.
	IsEnterKey = rules.IsEnterKey
	// IsActivationKey reports whether a press activates a focused control.
	IsActivationKey = rules.IsActivationKey
	// HasLineBreak reports whether typed text contains a line break.
	HasLineBreak = rules.HasLineBreak
	// TypedEnter turns typing a line break into the Enter press it is.
	TypedEnter = rules.TypedEnter
	// Host names a page's host for prompts; rules key on Site.
	Host = rules.Host
	// GatedActions are the browser actions Classify can flag.
	GatedActions = rules.GatedActions
)
