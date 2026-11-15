package ports

import (
	"context"
	"errors"
)

type ChatSteerer interface {
	Steer(ctx context.Context, providerTurnID string, msg ChatUserMessage) (ChatTurnRef, error)
}

type ChatSteerWithdrawer interface {
	WithdrawPendingSteers() (restore func())
}

var (
	ErrChatNoSteerableTurn         = errors.New("no active turn to steer")
	ErrChatTurnNotSteerable        = errors.New("the running turn cannot be steered")
	ErrChatSteerContentUnsupported = errors.New("steer content is unsupported")
)
