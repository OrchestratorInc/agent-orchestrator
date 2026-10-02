package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func validSwitchAtom(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

// CreateAccountsManagerSwitch admits one idempotent operation at the observed binding and controller.
func (s *Store) CreateAccountsManagerSwitch(ctx context.Context, op domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, bool, error) {
	if !validSwitchAtom(op.ID, 128) || !validSwitchAtom(op.TargetGeneration, 128) || !op.Policy.Valid() || op.SourceRevision <= 0 ||
		!validAccountsManagerBinding(domain.AccountsManagerSessionRoute{SessionID: op.SessionID, Provider: op.Provider, Mode: op.TargetMode, AccountID: op.TargetAccountID}) {
		return domain.AccountsManagerSwitch{}, false, domain.ErrAccountsManagerSwitchConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	created := false
	err := s.inTx(ctx, "create accounts manager switch", func(q *gen.Queries) error {
		existing, err := q.GetAccountsManagerSwitch(ctx, op.ID)
		if err == nil {
			result, err = accountsManagerSwitchFromRow(existing)
			if err != nil {
				return err
			}
			if !result.SameRequest(op) {
				return domain.ErrAccountsManagerSwitchConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row, err := q.GetSession(ctx, op.SessionID)
		if err != nil {
			return err
		}
		session := getSessionRowToRecord(row)
		if session.IsTerminated || session.ProvisionState.WithDefault() != domain.SessionProvisionReady || session.ControllerOwner() != op.SourceOwner || session.Metadata.RuntimeHandleID != op.SourceRuntimeHandleID {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if (op.Provider == domain.AccountsManagerProviderCodex && session.Harness != domain.HarnessCodex) || (op.Provider == domain.AccountsManagerProviderClaude && session.Harness != domain.HarnessClaudeCode) {
			return domain.ErrAccountsManagerSwitchConflict
		}
		binding, err := s.getAccountsManagerSessionRoute(ctx, q, op.SessionID, op.Provider)
		if err != nil {
			return err
		}
		if binding.Revision != op.SourceRevision || binding.Mode != op.SourceMode || binding.AccountID != op.SourceAccountID {
			return domain.ErrAccountsManagerBindingConflict
		}
		if err := admitAccountSelection(ctx, q, binding.AccountID); err != nil {
			return err
		}
		if admissionErr := admitAccountSelection(ctx, q, op.TargetAccountID); admissionErr != nil {
			return admissionErr
		}
		owner, err := json.Marshal(op.SourceOwner)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		fresh := int64(0)
		if op.NewConversation {
			fresh = 1
		}
		err = q.InsertAccountsManagerSwitch(ctx, gen.InsertAccountsManagerSwitchParams{ID: op.ID, SessionID: string(op.SessionID), Provider: string(op.Provider), SourceMode: string(op.SourceMode), SourceAccountID: op.SourceAccountID, SourceRevision: op.SourceRevision, SourceOwner: string(owner), SourceRuntimeHandleID: op.SourceRuntimeHandleID, TargetMode: string(op.TargetMode), TargetAccountID: op.TargetAccountID, TargetGeneration: op.TargetGeneration, Policy: string(op.Policy), NewConversation: fresh, CreatedAt: now, UpdatedAt: now})
		if isSQLiteUnique(err) {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if err != nil {
			return err
		}
		created = true
		stored, err := q.GetAccountsManagerSwitch(ctx, op.ID)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(stored)
		return err
	})
	return result, created && err == nil, err
}

// GetAccountsManagerSwitch distinguishes absence from unreadable durable state.
func (s *Store) GetAccountsManagerSwitch(ctx context.Context, id string) (domain.AccountsManagerSwitch, bool, error) {
	row, err := s.qr.GetAccountsManagerSwitch(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerSwitch{}, false, nil
	}
	if err != nil {
		return domain.AccountsManagerSwitch{}, false, fmt.Errorf("get accounts manager switch: %w", err)
	}
	op, err := accountsManagerSwitchFromRow(row)
	return op, err == nil, err
}

// GetLatestAccountsManagerSwitch retains the latest journal for public status and admission.
func (s *Store) GetLatestAccountsManagerSwitch(ctx context.Context, id domain.SessionID) (domain.AccountsManagerSwitch, bool, error) {
	row, err := s.qr.GetLatestAccountsManagerSwitch(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerSwitch{}, false, nil
	}
	if err != nil {
		return domain.AccountsManagerSwitch{}, false, fmt.Errorf("get latest accounts manager switch: %w", err)
	}
	op, err := accountsManagerSwitchFromRow(row)
	return op, err == nil, err
}

// ListActiveAccountsManagerSwitches exposes outstanding startup recovery obligations.
func (s *Store) ListActiveAccountsManagerSwitches(ctx context.Context) ([]domain.AccountsManagerSwitch, error) {
	rows, err := s.qr.ListActiveAccountsManagerSwitches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts manager switches: %w", err)
	}
	result := make([]domain.AccountsManagerSwitch, 0, len(rows))
	for _, row := range rows {
		op, err := accountsManagerSwitchFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}

// AdvanceAccountsManagerSwitch requires the expected durable phase.
func (s *Store) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	if !expected.CanAdvance(next) || (code != "" && !validSwitchAtom(code, 64)) {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	err := s.inTx(ctx, "advance accounts manager switch", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		if row.Phase != string(expected) || (next == domain.AccountsManagerSwitchStarting && row.TargetRevision <= 0) || (next == domain.AccountsManagerSwitchStopping && row.TargetRevision > 0) {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if err := advanceAccountsManagerSwitch(ctx, q, id, expected, next, code); err != nil {
			return err
		}
		row, err = q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(row)
		return err
	})
	return result, err
}

func advanceAccountsManagerSwitch(ctx context.Context, q *gen.Queries, id string, expected, next domain.AccountsManagerSwitchPhase, code string) error {
	changed, err := q.AdvanceAccountsManagerSwitch(ctx, gen.AdvanceAccountsManagerSwitchParams{ID: id, ExpectedPhase: string(expected), NextPhase: string(next), ErrorCode: code, UpdatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if changed != 1 {
		return domain.ErrAccountsManagerSwitchConflict
	}
	return nil
}

// PrepareAccountsManagerSwitchStop preserves history proof before making cancellation irreversible.
func (s *Store) PrepareAccountsManagerSwitchStop(ctx context.Context, id string, empty bool, nativeID string, expected domain.SessionControllerOwner) (domain.AccountsManagerSwitch, error) {
	if (empty && nativeID != "") || (nativeID != "" && !validSwitchAtom(nativeID, 512)) {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	err := s.inTx(ctx, "prepare account switch stop", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		op, err := accountsManagerSwitchFromRow(row)
		if err != nil {
			return err
		}
		if (op.Phase != domain.AccountsManagerSwitchWaiting && op.Phase != domain.AccountsManagerSwitchRecoveryRequired) || op.TargetRevision != 0 {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if !op.NewConversation && !empty && nativeID == "" {
			return domain.ErrAccountsManagerSwitchConflict
		}
		sessionRow, err := q.GetSession(ctx, op.SessionID)
		if err != nil {
			return err
		}
		session := getSessionRowToRecord(sessionRow)
		if session.IsTerminated || session.ControllerOwner() != expected || session.Metadata.RuntimeHandleID != op.SourceRuntimeHandleID ||
			expected.Harness != op.SourceOwner.Harness || expected.Mode != op.SourceOwner.Mode || expected.RuntimeLaunchID != op.SourceOwner.RuntimeLaunchID || expected.ControllerGeneration != op.SourceOwner.ControllerGeneration {
			return domain.ErrAccountsManagerSwitchConflict
		}
		proof := int64(0)
		if empty {
			proof = 1
		}
		changed, err := q.PrepareAccountsManagerSwitchStop(ctx, gen.PrepareAccountsManagerSwitchStopParams{ID: id, EmptySource: proof, SourceNativeConversationID: nativeID, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrAccountsManagerSwitchConflict
		}
		row, err = q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(row)
		return err
	})
	return result, err
}

// CommitAccountsManagerSwitch changes the binding and journal in the same transaction.
func (s *Store) CommitAccountsManagerSwitch(ctx context.Context, id string) (domain.AccountsManagerSwitch, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	err := s.inTx(ctx, "commit accounts manager switch", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		op, err := accountsManagerSwitchFromRow(row)
		if err != nil {
			return err
		}
		if op.Phase != domain.AccountsManagerSwitchStopped || op.TargetRevision != 0 {
			return domain.ErrAccountsManagerSwitchConflict
		}
		sessionRow, err := q.GetSession(ctx, op.SessionID)
		if err != nil {
			return err
		}
		session := getSessionRowToRecord(sessionRow)
		owner := session.ControllerOwner()
		if session.IsTerminated || owner.Harness != op.SourceOwner.Harness || owner.Mode != op.SourceOwner.Mode || owner.RuntimeLaunchID != op.SourceOwner.RuntimeLaunchID || owner.ControllerGeneration != op.SourceOwner.ControllerGeneration {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if err := admitAccountSelection(ctx, q, op.TargetAccountID); err != nil {
			return err
		}
		changed, err := q.CompareAndSwapAccountsManagerSessionRoute(ctx, gen.CompareAndSwapAccountsManagerSessionRouteParams{SessionID: string(op.SessionID), Provider: string(op.Provider), ConnectionMode: string(op.TargetMode), AccountID: op.TargetAccountID, Revision: op.SourceRevision, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrAccountsManagerBindingConflict
		}
		binding, err := s.getAccountsManagerSessionRoute(ctx, q, op.SessionID, op.Provider)
		if err != nil {
			return err
		}
		changed, err = q.CommitAccountsManagerSwitch(ctx, gen.CommitAccountsManagerSwitchParams{ID: id, TargetRevision: binding.Revision, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrAccountsManagerSwitchConflict
		}
		row, err = q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(row)
		return err
	})
	return result, err
}

// RetryAccountsManagerSwitch rotates controller identity and authorization after a confirmed stop.
func (s *Store) RetryAccountsManagerSwitch(ctx context.Context, id, generation string, expected domain.SessionControllerOwner) (domain.AccountsManagerSwitch, error) {
	if !validSwitchAtom(generation, 128) {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	err := s.inTx(ctx, "retry accounts manager switch", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		op, err := accountsManagerSwitchFromRow(row)
		if err != nil {
			return err
		}
		if op.Phase != domain.AccountsManagerSwitchRecoveryRequired || op.TargetRevision <= 0 || op.TargetGeneration == generation {
			return domain.ErrAccountsManagerSwitchConflict
		}
		sessionRow, err := q.GetSession(ctx, op.SessionID)
		if err != nil {
			return err
		}
		session := getSessionRowToRecord(sessionRow)
		if session.IsTerminated || session.ControllerOwner() != expected || expected.Harness != op.SourceOwner.Harness || expected.Mode != op.SourceOwner.Mode {
			return domain.ErrAccountsManagerSwitchConflict
		}
		currentGeneration, sourceGeneration := expected.RuntimeLaunchID, op.SourceOwner.RuntimeLaunchID
		if expected.Mode == domain.SessionModeChat {
			currentGeneration, sourceGeneration = expected.ControllerGeneration, op.SourceOwner.ControllerGeneration
		}
		retired := currentGeneration != "" && currentGeneration == op.RetiredTargetGeneration && session.Metadata.RuntimeHandleID == op.RetiredTargetHandleID
		if currentGeneration != sourceGeneration && currentGeneration != op.TargetGeneration && !retired {
			return domain.ErrAccountsManagerSwitchConflict
		}
		binding, err := s.getAccountsManagerSessionRoute(ctx, q, op.SessionID, op.Provider)
		if err != nil {
			return err
		}
		if binding.Revision != op.TargetRevision || binding.Mode != op.TargetMode || binding.AccountID != op.TargetAccountID {
			return domain.ErrAccountsManagerBindingConflict
		}
		if err := admitAccountSelection(ctx, q, op.TargetAccountID); err != nil {
			return err
		}
		changed, err := q.CompareAndSwapAccountsManagerSessionRoute(ctx, gen.CompareAndSwapAccountsManagerSessionRouteParams{SessionID: string(op.SessionID), Provider: string(op.Provider), ConnectionMode: string(op.TargetMode), AccountID: op.TargetAccountID, Revision: op.TargetRevision, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrAccountsManagerBindingConflict
		}
		binding, err = s.getAccountsManagerSessionRoute(ctx, q, op.SessionID, op.Provider)
		if err != nil {
			return err
		}
		changed, err = q.RetryAccountsManagerSwitch(ctx, gen.RetryAccountsManagerSwitchParams{ID: id, TargetRevision: binding.Revision, TargetGeneration: generation, RetiredTargetGeneration: currentGeneration, RetiredTargetHandleID: session.Metadata.RuntimeHandleID, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrAccountsManagerSwitchConflict
		}
		row, err = q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(row)
		return err
	})
	return result, err
}

// AcknowledgeAccountsManagerSwitch requires the committed binding and intended controller generation.
func (s *Store) AcknowledgeAccountsManagerSwitch(ctx context.Context, id, expectedGeneration string) (domain.AccountsManagerSwitch, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSwitch
	err := s.inTx(ctx, "acknowledge accounts manager switch", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		op, err := accountsManagerSwitchFromRow(row)
		if err != nil {
			return err
		}
		if op.Phase != domain.AccountsManagerSwitchStarting || op.TargetRevision <= 0 || op.TargetGeneration != expectedGeneration {
			return domain.ErrAccountsManagerSwitchConflict
		}
		binding, err := s.getAccountsManagerSessionRoute(ctx, q, op.SessionID, op.Provider)
		if err != nil {
			return err
		}
		if binding.Revision != op.TargetRevision || binding.Mode != op.TargetMode || binding.AccountID != op.TargetAccountID {
			return domain.ErrAccountsManagerBindingConflict
		}
		sessionRow, err := q.GetSession(ctx, op.SessionID)
		if err != nil {
			return err
		}
		session := getSessionRowToRecord(sessionRow)
		generation := session.Metadata.RuntimeLaunchID
		if domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat {
			generation = session.Metadata.ControllerGeneration
		}
		if session.IsTerminated || session.Harness != op.SourceOwner.Harness || domain.NormalizeSessionMode(session.Mode) != op.SourceOwner.Mode || generation != op.TargetGeneration {
			return domain.ErrAccountsManagerSwitchConflict
		}
		if err := advanceAccountsManagerSwitch(ctx, q, id, op.Phase, domain.AccountsManagerSwitchReady, ""); err != nil {
			return err
		}
		row, err = q.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		result, err = accountsManagerSwitchFromRow(row)
		return err
	})
	return result, err
}

func accountsManagerSwitchFromRow(row gen.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	var owner domain.SessionControllerOwner
	if err := json.Unmarshal([]byte(row.SourceOwner), &owner); err != nil {
		return domain.AccountsManagerSwitch{}, fmt.Errorf("read accounts manager controller ownership: %w", err)
	}
	return domain.AccountsManagerSwitch{ID: row.ID, SessionID: domain.SessionID(row.SessionID), Provider: domain.AccountsManagerProvider(row.Provider), SourceMode: domain.AccountsManagerConnectionMode(row.SourceMode), SourceAccountID: row.SourceAccountID, SourceRevision: row.SourceRevision, SourceOwner: owner, SourceRuntimeHandleID: row.SourceRuntimeHandleID, TargetMode: domain.AccountsManagerConnectionMode(row.TargetMode), TargetAccountID: row.TargetAccountID, TargetRevision: row.TargetRevision, TargetGeneration: row.TargetGeneration, RetiredTargetGeneration: row.RetiredTargetGeneration, RetiredTargetHandleID: row.RetiredTargetHandleID, Policy: domain.SessionInterfaceTransitionPolicy(row.Policy), NewConversation: row.NewConversation != 0, EmptySource: row.EmptySource != 0, SourceNativeConversationID: row.SourceNativeConversationID, Phase: domain.AccountsManagerSwitchPhase(row.Phase), ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}
