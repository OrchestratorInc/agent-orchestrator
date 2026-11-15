package ports

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ErrCodexAccountSwitchInProgress          = errors.New("codex account switch already in progress")
	ErrCodexAccountAlreadyActive             = errors.New("codex account is already active")
	ErrCodexAccountSwitchIdempotencyConflict = errors.New("codex account switch idempotency conflict")
	ErrCodexAccountLoginInProgress           = errors.New("codex account login in progress")
	ErrCodexGlobalAccountChanged             = errors.New("global codex account changed")
	ErrCodexGlobalCredentialStoreUnsupported = errors.New("global codex credential store is not safely file-backed")
	ErrCodexAccountSwitchNotCommitted        = errors.New("codex account switch did not mutate the device credential")
)

type CodexOperationLease interface {
	Release()
}

type CodexOperationGate interface {
	AcquireShared(context.Context) (release func(), err error)
	AcquireSharedWait(context.Context) (release func(), err error)
	AcquireExclusive(context.Context) (CodexOperationLease, error)
	ExclusivePendingOrHeld() bool
}

type CodexAccountCredentialManager interface {
	WaitCodexAccountStoreReady(context.Context) error
	EnsureCodexDeviceAccountReconciled(context.Context) error
	BeginCodexAccountMutation(context.Context) error
	EndCodexAccountMutation()
	PrepareCodexAccountForSwitch(context.Context, string, string) (domain.CodexAccountSwitchSource, error)
	InspectCodexAccountSwitch(context.Context, string, domain.CodexAccountSwitchSourceKind, string, string) (domain.CodexAccountSwitchInstallationState, error)
	ActivatePreparedCodexAccountSwitch(context.Context, domain.CodexAccountSwitchSourceKind, string, string) error
	CleanupCodexAccountSwitch(context.Context, string) error
	CleanupInactiveCodexAccountSwitches(context.Context, string) error
}

type CodexAccountSwitchConfig struct {
	TargetAccountID string
	IdempotencyKey  string
}

type CodexAccountSwitchStore interface {
	CreateCodexAccountSwitch(context.Context, domain.CodexAccountSwitch) (domain.CodexAccountSwitch, bool, error)
	GetCodexAccountSwitch(context.Context, string) (domain.CodexAccountSwitch, bool, error)
	GetCodexAccountSwitchByIdempotency(context.Context, string) (domain.CodexAccountSwitch, bool, error)
	GetActiveCodexAccountSwitch(context.Context) (domain.CodexAccountSwitch, bool, error)
	UpdateCodexAccountSwitch(context.Context, domain.CodexAccountSwitch, domain.CodexAccountSwitchPhase) (bool, error)
}
