package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var ErrConversationNotFound = domain.ErrNoConversation

var ErrNoQueuedTurn = domain.ErrNoQueuedTurn

var ErrQueuedTurnNotAvailable = errors.New("queued turn is not available for promotion")

type conversationCreateOptions struct {
	id            string
	scope         domain.ConversationScope
	project       domain.ProjectID
	session       domain.SessionID
	contextReset  *domain.ConversationActivity
	preserveOwner bool
	adoptQueue    bool
	now           time.Time
}

func (s *Store) CreateConversation(
	ctx context.Context,
	id string,
	scope domain.ConversationScope,
	project domain.ProjectID,
	session domain.SessionID,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:      id,
		scope:   scope,
		project: project,
		session: session,
		now:     now,
	})
}

func (s *Store) OpenNativeConversation(ctx context.Context, id string, scope domain.ConversationScope, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id: id, scope: scope, project: project, session: session, now: now, preserveOwner: true,
	})
}

func (s *Store) CreateReviewConversation(ctx context.Context, id, reviewID string, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var conversation domain.ConversationRecord
	rootBranchID := id + ":root"
	if err := s.inTx(ctx, "insert reviewer conversation", func(q *gen.Queries) error {
		review, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return fmt.Errorf("select reviewer %s: %w", reviewID, err)
		}
		if review.SessionID != session || review.ProjectID != project {
			return fmt.Errorf("reviewer %s is not the current chat owner", reviewID)
		}
		mode := string(domain.ReviewerInterfaceChat)
		if n, err := q.SetReviewInterfaceMode(ctx, gen.SetReviewInterfaceModeParams{InterfaceMode: mode, Column2: mode, Column3: mode, Column4: mode, UpdatedAt: now, ID: reviewID}); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("reviewer %s is not the current chat owner", reviewID)
		}
		if existing, err := q.SelectConversationByReview(ctx, nullableString(reviewID)); err == nil {
			conversation = conversationToDomain(existing)
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("select conversation for review %s: %w", reviewID, err)
		}
		if err := q.InsertReviewConversation(ctx, gen.InsertReviewConversationParams{
			ID: id, ProjectID: optionalProjectID(project), ReviewID: nullableString(reviewID), CurrentReviewID: nullableString(reviewID),
			ActiveBranchID: rootBranchID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := q.InsertReviewConversationBranch(ctx, gen.InsertReviewConversationBranchParams{
			ID: rootBranchID, ConversationID: id, SessionID: nullableString(string(session)), ReviewID: nullableString(reviewID),
			ProviderConversationID: review.ProviderConversationID, CreatedAt: now,
		}); err != nil {
			return err
		}
		conversation = domain.ConversationRecord{ID: id, Scope: domain.ConversationScopeReview, ProjectID: project, ReviewID: reviewID, SessionID: session, ActiveBranchID: rootBranchID, CreatedAt: now, UpdatedAt: now}
		return nil
	}); err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("insert reviewer conversation %s: %w", reviewID, err)
	}
	return conversation, nil
}

func (s *Store) CreateProjectConversationWithContextReset(
	ctx context.Context,
	id string,
	project domain.ProjectID,
	session domain.SessionID,
	reset domain.ConversationActivity,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:           id,
		scope:        domain.ConversationScopeProject,
		project:      project,
		session:      session,
		contextReset: &reset,
		now:          now,
	})
}

func (s *Store) CreateProjectConversationAdoptingQueue(
	ctx context.Context,
	id string,
	project domain.ProjectID,
	session domain.SessionID,
	reset domain.ConversationActivity,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:           id,
		scope:        domain.ConversationScopeProject,
		project:      project,
		session:      session,
		contextReset: &reset,
		adoptQueue:   true,
		now:          now,
	})
}

func (s *Store) createConversation(
	ctx context.Context,
	options conversationCreateOptions,
) (domain.ConversationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	scope := options.scope
	if scope == domain.ConversationScopeProject {
		if existing, err := s.qw.SelectProjectConversation(ctx, optionalProjectID(options.project)); err == nil {
			if options.preserveOwner {
				if existing.CurrentSessionID == nil || *existing.CurrentSessionID != options.session {
					return domain.ConversationRecord{}, fmt.Errorf("project conversation %s is no longer owned by session %s", existing.ID, options.session)
				}
				return conversationToDomain(existing), nil
			}
			transactionName := "rebind project conversation"
			if options.contextReset != nil {
				transactionName += " with reset"
			}
			rebind := func(q *gen.Queries) error {
				if err := q.BindProjectConversationSession(ctx, gen.BindProjectConversationSessionParams{
					CurrentSessionID: &options.session,
					UpdatedAt:        options.now,
					ID:               existing.ID,
				}); err != nil {
					return fmt.Errorf("bind project conversation %s to %s: %w", existing.ID, options.session, err)
				}
				if existing.CurrentSessionID == nil || *existing.CurrentSessionID != options.session {
					if _, err := q.ReleaseUntouchedConversationProvider(ctx, gen.ReleaseUntouchedConversationProviderParams{
						SessionID:       &options.session,
						ProviderScopeID: uuid.NewString(),
					}); err != nil {
						return fmt.Errorf("release untouched provider of project conversation %s: %w", existing.ID, err)
					}
				}
				if options.contextReset == nil ||
					existing.LatestSequence <= 0 ||
					options.contextReset.ProviderItemID == "" {
					return nil
				}
				reset := options.contextReset
				detail := string(reset.Detail)
				_, lookupErr := q.SelectConversationActivityByProviderItem(ctx,
					gen.SelectConversationActivityByProviderItemParams{
						ConversationID: existing.ID,
						ProviderItemID: reset.ProviderItemID,
					})
				if lookupErr == nil {
					return q.SettleConversationActivity(ctx, gen.SettleConversationActivityParams{
						Status:         reset.Status,
						Summary:        reset.Summary,
						DetailJson:     detail,
						UpdatedAt:      options.now,
						ConversationID: existing.ID,
						ProviderItemID: reset.ProviderItemID,
					})
				}
				if !errors.Is(lookupErr, sql.ErrNoRows) {
					return fmt.Errorf("lookup reset boundary %s: %w", reset.ProviderItemID, lookupErr)
				}
				sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
					UpdatedAt: options.now,
					ID:        existing.ID,
				})
				if seqErr != nil {
					return fmt.Errorf("allocate reset boundary sequence: %w", seqErr)
				}
				existing.LatestSequence = sequence
				return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
					ID:             reset.ID,
					ConversationID: existing.ID,
					TurnID:         sql.NullString{},
					Sequence:       sequence,
					Kind:           reset.Kind,
					Status:         reset.Status,
					Summary:        reset.Summary,
					DetailJson:     detail,
					RequestID:      reset.RequestID,
					ProviderItemID: reset.ProviderItemID,
					CreatedAt:      options.now,
					UpdatedAt:      options.now,
				})
			}
			err = s.inTx(ctx, transactionName, func(q *gen.Queries) error {
				if err := rebind(q); err != nil {
					return err
				}
				if !options.adoptQueue {
					return nil
				}
				latest, err := adoptProvisioningQueue(ctx, q, options.session, existing.ID, options.now)
				if err != nil {
					return err
				}
				existing.LatestSequence = latest
				return nil
			})
			if err != nil {
				return domain.ConversationRecord{}, err
			}
			existing.CurrentSessionID = &options.session
			existing.UpdatedAt = options.now
			return conversationToDomain(existing), nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.ConversationRecord{}, fmt.Errorf("select project conversation for %s: %w", options.project, err)
		}
	} else {
		scope = domain.ConversationScopeSession
		if existing, err := s.qw.SelectConversationBySession(ctx, &options.session); err == nil {
			return conversationToDomain(existing), nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.ConversationRecord{}, fmt.Errorf("select conversation for %s: %w", options.session, err)
		}
	}

	var ownerSession *domain.SessionID
	if scope == domain.ConversationScopeSession {
		ownerSession = &options.session
	}
	rootBranchID := options.id + ":root"
	var latestSequence int64
	err := s.inTx(ctx, "insert conversation", func(q *gen.Queries) error {
		owner, getErr := q.GetSession(ctx, options.session)
		if getErr != nil {
			return fmt.Errorf("select controller session %s: %w", options.session, getErr)
		}
		if insertErr := q.InsertConversation(ctx, gen.InsertConversationParams{
			ID:               options.id,
			Scope:            scope,
			ProjectID:        optionalProjectID(options.project),
			SessionID:        ownerSession,
			CurrentSessionID: &options.session,
			ActiveBranchID:   rootBranchID,
			CreatedAt:        options.now,
			UpdatedAt:        options.now,
		}); insertErr != nil {
			return insertErr
		}
		if err := q.InsertConversationBranch(ctx, gen.InsertConversationBranchParams{
			ID:                     rootBranchID,
			ConversationID:         options.id,
			SessionID:              nullableString(string(options.session)),
			ProviderConversationID: owner.ProviderConversationID,
			ForkAfterSequence:      0,
			ProviderScopeID:        rootBranchID,
			ProviderIdsScoped:      1,
			CreatedAt:              options.now,
		}); err != nil {
			return err
		}
		if scope == domain.ConversationScopeSession && owner.ImportSource != "" {
			var seedErr error
			latestSequence, seedErr = q.SeedImportedConversationSequence(ctx, gen.SeedImportedConversationSequenceParams{SessionID: string(options.session), ID: options.id})
			return seedErr
		}
		if !options.adoptQueue || scope != domain.ConversationScopeProject {
			return nil
		}
		var adoptErr error
		latestSequence, adoptErr = adoptProvisioningQueue(ctx, q, options.session, options.id, options.now)
		return adoptErr
	})
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("insert conversation %s: %w", options.id, err)
	}

	return domain.ConversationRecord{
		ID:             options.id,
		Scope:          scope,
		ProjectID:      options.project,
		SessionID:      options.session,
		ActiveBranchID: rootBranchID,
		LatestSequence: latestSequence,
		CreatedAt:      options.now,
		UpdatedAt:      options.now,
	}, nil
}

func adoptProvisioningQueue(
	ctx context.Context,
	q *gen.Queries,
	session domain.SessionID,
	targetID string,
	now time.Time,
) (int64, error) {
	target, err := q.SelectConversationByID(ctx, targetID)
	if err != nil {
		return 0, fmt.Errorf("select adopting conversation %s: %w", targetID, err)
	}
	sources, err := q.SelectProvisioningConversationsForSession(ctx,
		gen.SelectProvisioningConversationsForSessionParams{CurrentSessionID: &session, ID: targetID})
	if err != nil {
		return 0, fmt.Errorf("select provisioning conversations for %s: %w", session, err)
	}
	latest := target.LatestSequence
	for _, source := range sources {
		if err := retargetExcerptReferences(ctx, q, source.ID, targetID); err != nil {
			return 0, err
		}
		if err := q.MoveConversationTurns(ctx, gen.MoveConversationTurnsParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move turns from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationMessages(ctx, gen.MoveConversationMessagesParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SequenceOffset: latest, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move messages from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationActivities(ctx, gen.MoveConversationActivitiesParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SequenceOffset: latest, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move activities from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationQueuedEditDeliveries(ctx, gen.MoveConversationQueuedEditDeliveriesParams{
			TargetID: targetID, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move queued edit receipts from %s: %w", source.ID, err)
		}
		if err := q.AdvanceConversationSequence(ctx, gen.AdvanceConversationSequenceParams{
			LatestSequence: source.LatestSequence, UpdatedAt: now, ID: targetID,
		}); err != nil {
			return 0, fmt.Errorf("advance sequence of %s: %w", targetID, err)
		}
		latest += source.LatestSequence
		if err := q.CancelAllQueuedConversationTurns(ctx, gen.CancelAllQueuedConversationTurnsParams{
			CompletedAt: timeToNullTime(now), ConversationID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("settle duplicate turns in %s: %w", source.ID, err)
		}
		if err := q.DetachSessionConversation(ctx, gen.DetachSessionConversationParams{
			UpdatedAt: now, ID: source.ID, CurrentSessionID: &session,
		}); err != nil {
			return 0, fmt.Errorf("detach provisioning conversation %s: %w", source.ID, err)
		}
	}
	return latest, nil
}

func retargetExcerptReferences(ctx context.Context, q *gen.Queries, sourceID, targetID string) error {
	rows, err := q.SelectConversationDeliveryContent(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("select delivery content in %s: %w", sourceID, err)
	}
	for _, row := range rows {
		var content []ports.ChatContent
		if err := json.Unmarshal([]byte(row.DeliveryContentJson), &content); err != nil {
			continue
		}
		changed := false
		for i := range content {
			if excerpt := content[i].Excerpt; excerpt != nil && excerpt.Reference.ConversationID == sourceID {
				excerpt.Reference.ConversationID = targetID
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("encode delivery content of %s: %w", row.ID, err)
		}
		if err := q.SetConversationMessageDeliveryContent(ctx, gen.SetConversationMessageDeliveryContentParams{
			DeliveryContentJson: string(encoded), ID: row.ID,
		}); err != nil {
			return fmt.Errorf("retarget excerpts of %s: %w", row.ID, err)
		}
	}
	return nil
}

func (s *Store) CreateConversationBranch(
	ctx context.Context,
	branch domain.ConversationBranch,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "insert conversation branch", func(q *gen.Queries) error {
		return insertConversationBranchTx(ctx, q, branch, now)
	})
}

func insertConversationBranchTx(
	ctx context.Context,
	q *gen.Queries,
	branch domain.ConversationBranch,
	now time.Time,
) error {
	conversation, err := q.SelectConversationByID(ctx, branch.ConversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: conversation %s", domain.ErrNoConversationBranch, branch.ConversationID)
	}
	if err != nil {
		return fmt.Errorf("select conversation %s: %w", branch.ConversationID, err)
	}
	if branch.SessionID == "" && conversation.CurrentSessionID != nil {
		branch.SessionID = *conversation.CurrentSessionID
	}
	if branch.ParentBranchID != "" {
		if _, err := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: branch.ConversationID,
			BranchID:       branch.ParentBranchID,
		}); err != nil {
			return fmt.Errorf("parent branch %s is not in conversation %s: %w",
				branch.ParentBranchID, branch.ConversationID, err)
		}
	}
	for label, turnID := range map[string]string{
		"fork-after":  branch.ForkAfterTurnID,
		"replaced":    branch.ReplacedTurnID,
		"replacement": branch.ReplacementTurnID,
	} {
		if turnID == "" {
			continue
		}
		turn, turnErr := q.SelectConversationTurnByID(ctx, turnID)
		if turnErr != nil || turn.ConversationID != branch.ConversationID {
			if turnErr == nil {
				turnErr = ErrConversationTurnNotFound
			}
			return fmt.Errorf("%s turn %s is not in conversation %s: %w",
				label, turnID, branch.ConversationID, turnErr)
		}
	}
	if now.IsZero() {
		now = branch.CreatedAt
	}
	if err := q.InsertConversationBranch(ctx, gen.InsertConversationBranchParams{
		ID:                     branch.ID,
		ConversationID:         branch.ConversationID,
		SessionID:              nullableString(string(branch.SessionID)),
		ProviderConversationID: branch.ProviderConversationID,
		ParentBranchID:         nullableString(branch.ParentBranchID),
		ForkAfterTurnID:        nullableString(branch.ForkAfterTurnID),
		ReplacedTurnID:         nullableString(branch.ReplacedTurnID),
		ReplacementTurnID:      nullableString(branch.ReplacementTurnID),
		ForkAfterSequence:      branch.ForkAfterSequence,
		Strategy:               string(domain.NormalizeConversationBranchStrategy(branch.Strategy)),
		ReplayCutoffSequence:   branch.ReplayCutoffSequence,
		ReplayTruncated:        boolInt(branch.ReplayTruncated),
		ProviderScopeID:        branch.ProviderScopeID,
		ProviderIdsScoped:      boolInt(branch.ProviderIDsScoped),
		CreatedAt:              now,
		HandoffNote:            branch.HandoffNote,
	}); err != nil {
		return fmt.Errorf("insert conversation branch %s: %w", branch.ID, err)
	}
	return nil
}

func (s *Store) CreateAndActivateConversationBranch(
	ctx context.Context,
	sessionID domain.SessionID,
	branch domain.ConversationBranch,
	generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "create and activate conversation branch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, now); err != nil {
			return err
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx, gen.ActivateConversationBranchSessionParams{
			ProviderConversationID: branch.ProviderConversationID,
			ControllerGeneration:   generation,
			UpdatedAt:              now,
			ID:                     sessionID,
		})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		}
		if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", sessionID)
		}
		return nil
	})
}

func (s *Store) CommitProviderSwitch(ctx context.Context, rec domain.SessionRecord, branch domain.ConversationBranch, generation string, settings domain.ConversationSettings, activity domain.ConversationActivity) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "commit provider switch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, rec.UpdatedAt); err != nil {
			return err
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx, gen.ActivateConversationBranchSessionParams{
			ProviderConversationID: branch.ProviderConversationID, ControllerGeneration: generation, UpdatedAt: rec.UpdatedAt, ID: rec.ID,
		})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		} else if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", rec.ID)
		}
		if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
			return fmt.Errorf("update switched session: %w", err)
		}
		if err := q.UpdateConversationTurnSettings(ctx, gen.UpdateConversationTurnSettingsParams{
			Model: nullableString(settings.Model), ReasoningEffort: nullableString(settings.ReasoningEffort),
			ApprovalMode: nullableString(string(settings.ApprovalMode)), OpencodeMode: settings.OpenCodeMode,
			AutoCompactWindow: settings.AutoCompactWindow, UpdatedAt: rec.UpdatedAt, ID: branch.ConversationID,
		}); err != nil {
			return fmt.Errorf("save switched conversation settings: %w", err)
		}
		if err := q.UpdateConversationModelReroute(ctx, gen.UpdateConversationModelRerouteParams{
			ModelRerouteJson: sql.NullString{}, UpdatedAt: rec.UpdatedAt, ID: branch.ConversationID,
		}); err != nil {
			return fmt.Errorf("clear switched conversation model reroute: %w", err)
		}
		sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{UpdatedAt: rec.UpdatedAt, ID: branch.ConversationID})
		if err != nil {
			return fmt.Errorf("allocate provider switch activity sequence: %w", err)
		}
		return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
			ID: activity.ID, ConversationID: branch.ConversationID, Sequence: sequence,
			Kind: activity.Kind, Status: activity.Status, Summary: activity.Summary,
			DetailJson: string(activity.Detail), RequestID: activity.RequestID,
			ProviderItemID: activity.ProviderItemID, CreatedAt: rec.UpdatedAt, UpdatedAt: rec.UpdatedAt,
		})
	})
}

func (s *Store) CreateAndActivateReviewConversationBranch(
	ctx context.Context,
	reviewID string,
	branch domain.ConversationBranch,
	generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "create and activate reviewer conversation branch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, now); err != nil {
			return err
		}
		reviewRows, err := q.ClaimReviewChatController(ctx, gen.ClaimReviewChatControllerParams{
			ProviderConversationID: branch.ProviderConversationID,
			ControllerGeneration:   generation,
			UpdatedAt:              now,
			ID:                     reviewID,
		})
		if err != nil {
			return fmt.Errorf("move reviewer controller: %w", err)
		}
		if reviewRows != 1 {
			return fmt.Errorf("move reviewer controller: chat review %s not found", reviewID)
		}
		return nil
	})
}

func insertAndActivateConversationBranchTx(ctx context.Context, q *gen.Queries, branch domain.ConversationBranch, now time.Time) error {
	if err := insertConversationBranchTx(ctx, q, branch, now); err != nil {
		return err
	}
	rows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
		ActiveBranchID: branch.ID,
		UpdatedAt:      now,
		ID:             branch.ConversationID,
	})
	if err != nil {
		return fmt.Errorf("move conversation head: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("move conversation head: conversation %s not found", branch.ConversationID)
	}
	return reconcileSpanningPlans(ctx, q, branch.ConversationID, now)
}

func (s *Store) CommitChatSpawn(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
) error {
	return s.commitChatSpawn(ctx, rec, branch, nil, nil)
}

func (s *Store) CommitChatSpawnPrepared(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if prepare == nil {
		return errors.New("commit Chat spawn: provider-history preparation is missing")
	}
	return s.commitChatSpawn(ctx, rec, branch, handoff, prepare)
}

func (s *Store) commitChatSpawn(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if rec.ID == "" || branch.ID == "" || branch.ConversationID == "" ||
		branch.SessionID != rec.ID || branch.ParentBranchID == "" ||
		branch.ProviderConversationID == "" ||
		branch.ProviderConversationID != rec.Metadata.ProviderConversationID ||
		branch.ProviderScopeID != branch.ID || rec.Metadata.ControllerGeneration == "" {
		return errors.New("commit Chat spawn: incomplete or mismatched provider boundary ownership")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "commit Chat spawn", func(q *gen.Queries) error {
		owner, err := q.GetSession(ctx, rec.ID)
		if err != nil {
			return fmt.Errorf("select Chat session %s: %w", rec.ID, err)
		}
		if domain.NormalizeSessionMode(owner.SessionMode) != domain.SessionModeChat {
			return fmt.Errorf("session %s is not in Chat mode", rec.ID)
		}
		if handoff != nil && (handoff.BoundaryID != branch.ID || handoff.ConversationID != branch.ConversationID ||
			handoff.PreviousBranchID != branch.ParentBranchID ||
			rowToRecord(owner).ControllerOwner() != handoff.ExpectedControllerOwner) {
			return errors.New("native Chat handoff controller ownership changed")
		}
		conversation, err := q.SelectConversationByID(ctx, branch.ConversationID)
		if err != nil {
			return fmt.Errorf("select conversation %s: %w", branch.ConversationID, err)
		}
		expectedSession := rec.ID
		if handoff != nil {
			expectedSession = handoff.PreviousSessionID
			if conversation.LatestSequence != handoff.PreviousSequence {
				return errors.New("native Chat handoff history changed")
			}
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != expectedSession {
			return fmt.Errorf("conversation %s is no longer owned by session %s",
				branch.ConversationID, rec.ID)
		}
		if conversation.ActiveBranchID != branch.ParentBranchID {
			return fmt.Errorf("conversation %s active branch changed from %s to %s",
				branch.ConversationID, branch.ParentBranchID, conversation.ActiveBranchID)
		}
		if expectedSession != rec.ID {
			previous, err := q.GetSession(ctx, expectedSession)
			if err != nil {
				return err
			}
			if conversation.Scope != domain.ConversationScopeProject || previous.ProjectID == nil || *previous.ProjectID != rec.ProjectID ||
				!previous.IsTerminated || !rec.CreatedAt.After(previous.CreatedAt) {
				return errors.New("native Chat handoff project owner is not a retired predecessor")
			}
			sessions, err := q.ListSessionsByProject(ctx, optionalProjectID(rec.ProjectID))
			if err != nil {
				return err
			}
			for _, session := range sessions {
				if session.Kind == domain.KindOrchestrator && session.ID != rec.ID && !session.IsTerminated {
					return errors.New("native Chat handoff has a competing live orchestrator")
				}
			}
			if err := q.BindProjectConversationSession(ctx, gen.BindProjectConversationSessionParams{
				CurrentSessionID: &rec.ID, UpdatedAt: rec.UpdatedAt, ID: conversation.ID,
			}); err != nil {
				return err
			}
		}
		if err := insertConversationBranchTx(ctx, q, branch, branch.CreatedAt); err != nil {
			return err
		}
		rows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
			ActiveBranchID: branch.ID,
			UpdatedAt:      rec.UpdatedAt,
			ID:             branch.ConversationID,
		})
		if err != nil {
			return fmt.Errorf("move conversation head: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("move conversation head: conversation %s not found", branch.ConversationID)
		}
		if err := moveUndispatchedIntakeTx(ctx, q, rec.ID, branch); err != nil {
			return err
		}
		if prepare != nil {
			rows, err := q.ClaimChatControllerGeneration(ctx, gen.ClaimChatControllerGenerationParams{
				ControllerGeneration: rec.Metadata.ControllerGeneration,
				ID:                   rec.ID,
			})
			if err != nil {
				return fmt.Errorf("stage Chat controller generation: %w", err)
			}
			if rows != 1 {
				return fmt.Errorf("stage Chat controller generation: Chat session %s not found", rec.ID)
			}
			txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
			if err := prepare(txCtx); err != nil {
				return fmt.Errorf("project native provider history: %w", err)
			}
		}
		if err := updateSessionRow(ctx, q, rec); err != nil {
			return fmt.Errorf("commit Chat session owner: %w", err)
		}
		return nil
	})
}

func moveUndispatchedIntakeTx(ctx context.Context, q *gen.Queries, session domain.SessionID, branch domain.ConversationBranch) error {
	if err := q.MoveUndispatchedTurnMessagesToBranch(ctx, gen.MoveUndispatchedTurnMessagesToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake messages: %w", err)
	}
	if err := q.MoveUndispatchedTurnActivitiesToBranch(ctx, gen.MoveUndispatchedTurnActivitiesToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake activities: %w", err)
	}
	if err := q.MoveUndispatchedTurnsToBranch(ctx, gen.MoveUndispatchedTurnsToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake turns: %w", err)
	}
	return nil
}

func (s *Store) ConversationBranch(
	ctx context.Context,
	conversationID, branchID string,
) (domain.ConversationBranch, error) {
	row, err := s.qr.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
		ConversationID: conversationID,
		BranchID:       branchID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationBranch{}, fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
	}
	if err != nil {
		return domain.ConversationBranch{}, fmt.Errorf("select conversation branch %s: %w", branchID, err)
	}
	return conversationBranchToDomain(row), nil
}

func (s *Store) ConversationBranches(
	ctx context.Context,
	conversationID string,
) ([]domain.ConversationBranch, error) {
	rows, err := s.qr.SelectConversationBranches(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select conversation branches for %s: %w", conversationID, err)
	}
	branches := make([]domain.ConversationBranch, 0, len(rows))
	for _, row := range rows {
		branches = append(branches, conversationBranchListToDomain(row))
	}
	return branches, nil
}

func (s *Store) ConversationEditAnchor(
	ctx context.Context,
	conversationID, replacedTurnID string,
) (domain.ConversationEditAnchor, error) {
	row, err := s.qr.SelectConversationEditAnchor(ctx, gen.SelectConversationEditAnchorParams{
		ConversationID: conversationID,
		ReplacedTurnID: nullableString(replacedTurnID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationEditAnchor{}, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, replacedTurnID)
	}
	if err != nil {
		return domain.ConversationEditAnchor{}, fmt.Errorf("select conversation edit anchor %s: %w", replacedTurnID, err)
	}
	return domain.ConversationEditAnchor{
		ConversationID:              row.ConversationID,
		SourceBranchID:              row.SourceBranchID,
		ReplacedTurnID:              row.ReplacedTurnID.String,
		PreviousProviderTurnID:      row.PreviousProviderTurnID,
		ForkAfterSequence:           row.ForkAfterSequence,
		ReplayFloorSequence:         row.ReplayFloorSequence,
		HasPriorContext:             row.HasPriorContext,
		OriginalDeliveryContentJSON: row.OriginalDeliveryContentJson,
		RetryActiveBranch:           row.RetryActiveBranch.Valid && row.RetryActiveBranch.Bool,
	}, nil
}

func (s *Store) UpdateConversationBranchReplacement(
	ctx context.Context,
	branchID, replacementTurnID string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateConversationBranchReplacement(ctx,
		gen.UpdateConversationBranchReplacementParams{
			ReplacementTurnID: nullableString(replacementTurnID),
			BranchID:          branchID,
		})
	if err != nil {
		return fmt.Errorf("update conversation branch %s replacement: %w", branchID, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
	}
	return nil
}

func (s *Store) RepairIncompleteConversationEdit(
	ctx context.Context,
	sessionID domain.SessionID,
	conversationID string,
	now time.Time,
) (repaired domain.ConversationBranch, restoredProviderOwner bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.inTx(ctx, "repair incomplete conversation edit", func(q *gen.Queries) error {
		conversation, selectErr := q.SelectConversationByID(ctx, conversationID)
		if selectErr != nil {
			return fmt.Errorf("select conversation %s: %w", conversationID, selectErr)
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != sessionID {
			return fmt.Errorf("conversation %s is not controlled by session %s", conversationID, sessionID)
		}
		activeRow, selectErr := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       conversation.ActiveBranchID,
		})
		if selectErr != nil {
			return fmt.Errorf("select active conversation branch %s: %w",
				conversation.ActiveBranchID, selectErr)
		}
		active := conversationBranchToDomain(activeRow)
		if active.ParentBranchID == "" || active.ReplacedTurnID == "" || active.ReplacementTurnID != "" {
			return nil
		}

		replacementTurnID, selectErr := q.SelectFirstHumanTurnOnBranch(
			ctx, gen.SelectFirstHumanTurnOnBranchParams{
				ConversationID: conversationID,
				BranchID:       active.ID,
			})
		if selectErr == nil {
			rows, updateErr := q.UpdateConversationBranchReplacement(
				ctx, gen.UpdateConversationBranchReplacementParams{
					ReplacementTurnID: nullableString(replacementTurnID),
					BranchID:          active.ID,
				})
			if updateErr != nil {
				return fmt.Errorf("link recovered edit branch %s: %w", active.ID, updateErr)
			}
			if rows != 1 {
				return fmt.Errorf("link recovered edit branch %s: branch disappeared", active.ID)
			}
			active.ReplacementTurnID = replacementTurnID
			repaired = active
			return nil
		}
		if !errors.Is(selectErr, sql.ErrNoRows) {
			return fmt.Errorf("select first prompt on edit branch %s: %w", active.ID, selectErr)
		}

		parentRow, selectErr := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       active.ParentBranchID,
		})
		if selectErr != nil {
			return fmt.Errorf("select source conversation branch %s: %w",
				active.ParentBranchID, selectErr)
		}
		parent := conversationBranchToDomain(parentRow)
		conversationRows, updateErr := q.ActivateConversationBranch(
			ctx, gen.ActivateConversationBranchParams{
				ActiveBranchID: parent.ID,
				UpdatedAt:      now,
				ID:             conversationID,
			})
		if updateErr != nil {
			return fmt.Errorf("restore source conversation head: %w", updateErr)
		}
		if conversationRows != 1 {
			return fmt.Errorf("restore source conversation head: conversation %s not found", conversationID)
		}
		if reconcileErr := reconcileSpanningPlans(ctx, q, conversationID, now); reconcileErr != nil {
			return reconcileErr
		}
		parent.Active = true
		repaired = parent
		if active.SessionID != sessionID {
			return nil
		}
		sessionRows, updateErr := q.ActivateConversationBranchSession(
			ctx, gen.ActivateConversationBranchSessionParams{
				ProviderConversationID: parent.ProviderConversationID,
				ControllerGeneration:   "",
				UpdatedAt:              now,
				ID:                     sessionID,
			})
		if updateErr != nil {
			return fmt.Errorf("restore source session controller: %w", updateErr)
		}
		if sessionRows != 1 {
			return fmt.Errorf("restore source session controller: live chat session %s not found", sessionID)
		}
		restoredProviderOwner = true
		return nil
	})
	if err != nil {
		return domain.ConversationBranch{}, false, err
	}
	return repaired, restoredProviderOwner, nil
}

func (s *Store) ActivateConversationBranch(
	ctx context.Context,
	sessionID domain.SessionID,
	conversationID, branchID, providerConversationID, generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "activate conversation branch", func(q *gen.Queries) error {
		conversation, err := q.SelectConversationByID(ctx, conversationID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: conversation %s", domain.ErrNoConversationBranch, conversationID)
		}
		if err != nil {
			return fmt.Errorf("select conversation %s: %w", conversationID, err)
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != sessionID {
			return fmt.Errorf("conversation %s is not controlled by session %s", conversationID, sessionID)
		}
		branch, err := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       branchID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
		}
		if err != nil {
			return fmt.Errorf("select conversation branch %s: %w", branchID, err)
		}
		if branch.ProviderConversationID != providerConversationID {
			return fmt.Errorf("activate conversation branch %s: provider conversation %q does not match stored %q",
				branchID, providerConversationID, branch.ProviderConversationID)
		}
		conversationRows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
			ActiveBranchID: branchID,
			UpdatedAt:      now,
			ID:             conversationID,
		})
		if err != nil {
			return fmt.Errorf("move conversation head: %w", err)
		}
		if conversationRows != 1 {
			return fmt.Errorf("move conversation head: conversation %s not found", conversationID)
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx,
			gen.ActivateConversationBranchSessionParams{
				ProviderConversationID: providerConversationID,
				ControllerGeneration:   generation,
				UpdatedAt:              now,
				ID:                     sessionID,
			})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		}
		if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", sessionID)
		}
		return reconcileSpanningPlans(ctx, q, conversationID, now)
	})
}

func (s *Store) ProjectConversation(ctx context.Context, project domain.ProjectID) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectProjectConversation(ctx, optionalProjectID(project))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select project conversation: %w", err)
	}
	return conversationToDomain(row), nil
}

func (s *Store) SessionForActivityProviderItem(ctx context.Context, providerItemID string) (domain.SessionID, bool, error) {
	sessionID, err := s.qr.SelectSessionForActivityProviderItem(ctx, providerItemID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return *sessionID, true, nil
}

func (s *Store) ConversationForSession(
	ctx context.Context,
	session domain.SessionID,
) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectConversationBySession(ctx, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select conversation for %s: %w", session, err)
	}
	return conversationToDomain(row), nil
}

func (s *Store) ListInterruptedSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]bool, error) {
	out := make(map[domain.SessionID]bool)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListInterruptedSessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list interrupted sessions: %w", err)
	}
	for _, id := range rows {
		out[domain.SessionID(id)] = true
	}
	return out, nil
}

func (s *Store) ConversationForReview(ctx context.Context, reviewID string) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectConversationByReview(ctx, nullableString(reviewID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select conversation for review %s: %w", reviewID, err)
	}
	return conversationToDomain(row), nil
}

func (s *Store) HasConversationTurns(ctx context.Context, conversationID string) (bool, error) {
	hasTurns, err := s.qr.HasConversationTurns(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("check conversation turns %s: %w", conversationID, err)
	}
	return hasTurns, nil
}

func (s *Store) LatestConversationTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectLatestConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select latest conversation turn: %w", err)
	}
	return turnToDomain(row), nil
}

func (s *Store) TurnByProviderID(ctx context.Context, conversationID, providerTurnID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
		ConversationID: conversationID, ProviderTurnID: providerTurnID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select turn by provider id %s: %w", providerTurnID, err)
	}
	return turnToDomain(row), nil
}

func (s *Store) LatestVisibleUserTurnSettled(ctx context.Context, conversationID string, sessionID domain.SessionID) (bool, error) {
	settled, err := s.qr.LatestVisibleUserTurnSettled(ctx, gen.LatestVisibleUserTurnSettledParams{
		ConversationID: conversationID,
		SessionID:      sessionID,
	})
	if err != nil {
		return false, fmt.Errorf("check latest user turn for %s: %w", conversationID, err)
	}
	return settled, nil
}

func (s *Store) AppendUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	now time.Time,
) (created bool, err error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, "", false, now)
}

func (s *Store) AppendReviewUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, reviewID, generation, msg, turnID, "", false, now)
}

func (s *Store) AppendRetryUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	retryOfTurnID string,
	now time.Time,
) (created bool, err error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, retryOfTurnID, false, now)
}

func (s *Store) AppendAutoResumeRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, retryOfTurnID, true, now)
}

func (s *Store) AppendReviewRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, reviewID, generation, msg, turnID, retryOfTurnID, false, now)
}

func (s *Store) appendUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID string,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	retryOfTurnID string,
	autoResume bool,
	now time.Time,
) (created bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if generation == "" && reviewID == "" {
		record, readErr := s.qw.GetSession(ctx, session)
		if readErr != nil {
			return false, fmt.Errorf("check queued session %s: %w", session, readErr)
		}
		if record.IsTerminated {
			return false, domain.ErrSessionNotProvisioning
		}
		if !record.ProvisionState.IsProvisioning() && !record.HibernatedAt.Valid {
			move, moveErr := s.qw.GetActiveSessionInterfaceTransition(ctx, session)
			if moveErr != nil && !errors.Is(moveErr, sql.ErrNoRows) {
				return false, fmt.Errorf("check account move of %s: %w", session, moveErr)
			}
			if moveErr != nil || !transitionRowToDomain(move).AccountMoveAwaitsTarget() {
				return false, domain.ErrSessionNotProvisioning
			}
		}
		conversation, readErr := s.qw.SelectConversationByID(ctx, conversationID)
		if readErr != nil {
			return false, fmt.Errorf("check queued conversation %s: %w", conversationID, readErr)
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != session {
			return false, domain.ErrConversationRebound
		}
	}

	if retryOfTurnID != "" {
		_, lookupErr := s.qr.SelectConversationRetryTurnIDBySource(ctx,
			gen.SelectConversationRetryTurnIDBySourceParams{
				ConversationID: conversationID,
				RetryOfTurnID:  sql.NullString{String: retryOfTurnID, Valid: true},
			})
		if lookupErr == nil {
			return false, nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return false, fmt.Errorf("lookup retry of turn %s: %w", retryOfTurnID, lookupErr)
		}
	} else if msg.ClientMessageID != "" {
		existing, lookupErr := s.qr.SelectConversationMessageByClientID(ctx,
			gen.SelectConversationMessageByClientIDParams{
				ConversationID:  conversationID,
				ClientMessageID: msg.ClientMessageID,
			})
		if lookupErr == nil {
			if existing.ClientPayloadHash.Valid {
				if msg.ClientPayloadHash == "" || existing.ClientPayloadHash.String != msg.ClientPayloadHash {
					return false, domain.ErrClientMessageConflict
				}
			} else if existing.Text != msg.Text || existing.Origin != msg.Origin ||
				existing.DeliveryContentJson != msg.DeliveryContentJSON {
				return false, domain.ErrClientMessageConflict
			}
			return false, nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return false, fmt.Errorf("lookup client message %s: %w", msg.ClientMessageID, lookupErr)
		}
	}

	err = s.inTx(ctx, "append user message", func(q *gen.Queries) error {
		if autoResume {
			source, err := q.SelectConversationTurnByID(ctx, retryOfTurnID)
			if err != nil {
				return fmt.Errorf("read auto-resume source: %w", err)
			}
			changed, err := q.SessionAgentAccountChangedSince(ctx, gen.SessionAgentAccountChangedSinceParams{
				SessionID: source.HandledBySessionID, RequestedAt: source.RequestedAt,
			})
			if err != nil {
				return fmt.Errorf("read auto-resume account binding: %w", err)
			}
			if changed {
				return ports.ErrAgentAccountChanged
			}
			consumed, err := q.ConsumeConversationTurnAutoResume(ctx, gen.ConsumeConversationTurnAutoResumeParams{
				ID: retryOfTurnID, ConversationID: conversationID, SessionID: session, Now: timeToNullTime(now),
			})
			if err != nil {
				return fmt.Errorf("consume due auto-resume consent: %w", err)
			}
			if consumed == 0 {
				if source.UsageLimitAutoResume != 0 && source.UsageLimitResetsAt.Valid && source.UsageLimitResetsAt.Time.After(now) {
					return domain.ErrConversationAutoResumeNotDue
				}
				return domain.ErrNoConversationTurn
			}
		}
		sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
			UpdatedAt: now,
			ID:        conversationID,
		})
		if err != nil {
			return fmt.Errorf("allocate sequence: %w", err)
		}

		if reviewID == "" {
			if err := q.InsertConversationTurn(ctx, gen.InsertConversationTurnParams{ID: turnID, ConversationID: conversationID, HandledBySessionID: session, ControllerGeneration: generation, RetryOfTurnID: nullableString(retryOfTurnID), State: domain.TurnStateQueued, RequestedAt: now}); err != nil {
				return fmt.Errorf("insert turn: %w", err)
			}
		} else if err := q.InsertReviewConversationTurn(ctx, gen.InsertReviewConversationTurnParams{ID: turnID, ConversationID: conversationID, HandledBySessionID: session, HandledByReviewID: nullableString(reviewID), ControllerGeneration: generation, RetryOfTurnID: nullableString(retryOfTurnID), State: domain.TurnStateQueued, RequestedAt: now}); err != nil {
			return fmt.Errorf("insert reviewer turn: %w", err)
		}
		if retryOfTurnID != "" && !autoResume {
			if _, err := q.SetConversationTurnAutoResume(ctx, gen.SetConversationTurnAutoResumeParams{
				ID: retryOfTurnID, ConversationID: conversationID,
			}); err != nil {
				return fmt.Errorf("consume source auto-resume: %w", err)
			}
		}

		if err := q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
			ID:                  msg.ID,
			ConversationID:      conversationID,
			TurnID:              sql.NullString{String: turnID, Valid: true},
			Sequence:            sequence,
			Role:                domain.MessageRoleUser,
			Origin:              msg.Origin,
			Text:                msg.Text,
			ProviderItemID:      "",
			ClientMessageID:     msg.ClientMessageID,
			ClientPayloadHash:   sql.NullString{String: msg.ClientPayloadHash, Valid: msg.ClientPayloadHash != ""},
			DeliveryContentJson: msg.DeliveryContentJSON,
			Continuation:        boolInt(msg.Continuation),
			AuthoredByUser:      boolInt(msg.AuthoredByUser),
			SenderSessionID:     msg.SenderSessionID,
			SenderProjectID:     msg.SenderProjectID,
			SenderDisplayName:   msg.SenderDisplayName,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			return err
		}
		if retryOfTurnID == "" && reviewID == "" && !msg.SpawnOpeningPrompt && msg.Origin == domain.MessageOriginHuman &&
			msg.SenderSessionID == "" && s.usageAllowed() {
			if err := q.IncrementUsageMessageSummary(ctx, usageMessageIncrement(ctx, session, usageMessageChat, now)); err != nil {
				return fmt.Errorf("record usage message count: %w", err)
			}
		}
		interactionAt := msg.InteractionAt
		if interactionAt.IsZero() {
			interactionAt = now
		}
		if msg.SenderSessionID != "" {
			if err := recordSessionInteraction(ctx, q, session, msg.SenderSessionID, interactionAt); err != nil {
				return err
			}
		}
		if msg.Origin == domain.MessageOriginHuman || msg.AuthoredByUser {
			if _, err := q.RecordSessionHumanMessage(ctx, gen.RecordSessionHumanMessageParams{
				ID:                 session,
				LatestUserPrompt:   msg.Text,
				LatestUserPromptAt: timeToNullTime(interactionAt),
			}); err != nil {
				return fmt.Errorf("record latest human message: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ConversationMessageByClientID(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationMessage, bool, error) {
	row, err := s.qr.SelectConversationMessageByClientID(ctx,
		gen.SelectConversationMessageByClientIDParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationMessage{}, false, nil
	}
	if err != nil {
		return domain.ConversationMessage{}, false, err
	}
	return messageToDomain(row), true, nil
}

func (s *Store) ConversationMessages(ctx context.Context, conversationID string) ([]domain.ConversationMessage, error) {
	rows, err := s.qr.SelectConversationMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.ConversationMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, messageToDomain(row))
	}
	return messages, nil
}

func (s *Store) AdoptProviderTurn(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation, turnID, providerTurnID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.AdoptProviderConversationTurn(ctx, gen.AdoptProviderConversationTurnParams{
		ID:                   turnID,
		ConversationID:       conversationID,
		HandledBySessionID:   session,
		ProviderTurnID:       providerTurnID,
		ControllerGeneration: generation,
		RequestedAt:          now,
		StartedAt:            sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("adopt provider turn %s: %w", providerTurnID, err)
	}
	return nil
}

func (s *Store) AdoptReviewProviderTurn(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation, turnID, providerTurnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.AdoptReviewProviderConversationTurn(ctx, gen.AdoptReviewProviderConversationTurnParams{
		ID: turnID, ConversationID: conversationID, HandledBySessionID: session, HandledByReviewID: nullableString(reviewID),
		ProviderTurnID: providerTurnID, ControllerGeneration: generation, RequestedAt: now, StartedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("adopt reviewer provider turn %s: %w", providerTurnID, err)
	}
	return nil
}

func (s *Store) AppendImportedUserMessage(
	ctx context.Context,
	conversationID, providerTurnID string,
	msg domain.ConversationMessage,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	_, err := q.SelectConversationUserMessageByTurn(ctx,
		gen.SelectConversationUserMessageByTurnParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup imported user message for turn %s: %w", providerTurnID, err)
	}

	turn, err := q.SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err != nil {
		return fmt.Errorf("lookup imported provider turn %s: %w", providerTurnID, err)
	}
	sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
		UpdatedAt: now,
		ID:        conversationID,
	})
	if err != nil {
		return fmt.Errorf("allocate imported user message sequence: %w", err)
	}
	if err := q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
		ID:                  msg.ID,
		ConversationID:      conversationID,
		TurnID:              sql.NullString{String: turn.ID, Valid: true},
		Sequence:            sequence,
		Role:                domain.MessageRoleUser,
		Origin:              domain.MessageOriginHuman,
		Text:                msg.Text,
		ProviderItemID:      msg.ProviderItemID,
		ClientMessageID:     msg.ClientMessageID,
		DeliveryContentJson: msg.DeliveryContentJSON,
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		return fmt.Errorf("insert imported user message for turn %s: %w", providerTurnID, err)
	}
	return nil
}

func (s *Store) MarkTurnDispatching(ctx context.Context, turnID string) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.MarkConversationTurnStarted(ctx, gen.MarkConversationTurnStartedParams{
		ID: turnID,
	}); err != nil {
		return fmt.Errorf("mark turn %s dispatching: %w", turnID, err)
	}
	return nil
}

func (s *Store) RecordProviderInput(ctx context.Context, turnID, text string) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.SetConversationProviderInputText(ctx, gen.SetConversationProviderInputTextParams{
		ProviderInputText: text, ID: turnID,
	})
	return expectBatchRow("record provider input", turnID, rows, err)
}

func expectBatchRow(action, id string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s %s: %w", action, id, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s %s: expected one row, got %d", action, id, rows)
	}
	return nil
}

func (s *Store) BindTurnToProvider(ctx context.Context, turnID, providerTurnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.BindConversationTurnProviderID(ctx, gen.BindConversationTurnProviderIDParams{
		ProviderTurnID: providerTurnID,
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		ID:             turnID,
	}); err != nil {
		return fmt.Errorf("bind turn %s to provider turn %s: %w", turnID, providerTurnID, err)
	}
	if err := q.MarkConversationTurnStarted(ctx, gen.MarkConversationTurnStartedParams{
		StartedAt: sql.NullTime{Time: now, Valid: true},
		ID:        turnID,
	}); err != nil {
		return fmt.Errorf("mark turn %s started: %w", turnID, err)
	}
	return nil
}

func (s *Store) SettleTurn(
	ctx context.Context,
	conversationID, providerTurnID string,
	state domain.TurnState,
	errMessage string,
	now time.Time,
) error {
	return s.SettleTurnWithFailureCode(ctx, conversationID, providerTurnID, state, errMessage, "", time.Time{}, now)
}

func (s *Store) SettleTurnWithFailureCode(
	ctx context.Context,
	conversationID, providerTurnID string,
	state domain.TurnState,
	errMessage string,
	failureCode domain.ConversationFailureCode,
	usageLimitResetsAt time.Time,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	turn, err := q.SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select turn %s: %w", providerTurnID, err)
	}
	if errMessage == "" && state == domain.TurnStateInterrupted &&
		turn.State == domain.TurnStateFailed && turn.ErrorMessage != "" {
		state = domain.TurnStateFailed
	}
	if errMessage == "" && turn.State == state {
		errMessage = turn.ErrorMessage
	}

	if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
		State:              state,
		ErrorMessage:       errMessage,
		FailureCode:        string(failureCode),
		CompletedAt:        sql.NullTime{Time: now, Valid: true},
		UsageLimitResetsAt: timeToNullTime(usageLimitResetsAt),
		ID:                 turn.ID,
	}); err != nil {
		return fmt.Errorf("settle turn %s: %w", turn.ID, err)
	}
	if err := q.SettleStreamingConversationMessagesForTurn(ctx,
		gen.SettleStreamingConversationMessagesForTurnParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
			TurnID:         sql.NullString{String: turn.ID, Valid: true},
		}); err != nil {
		return fmt.Errorf("settle streaming messages for turn %s: %w", turn.ID, err)
	}
	if err := q.SettleRunningConversationActivitiesForTurn(ctx,
		gen.SettleRunningConversationActivitiesForTurnParams{
			Status:         terminalActivityStatus(state),
			UpdatedAt:      now,
			ConversationID: conversationID,
			TurnID:         sql.NullString{String: turn.ID, Valid: true},
		}); err != nil {
		return fmt.Errorf("settle running activities for turn %s: %w", turn.ID, err)
	}
	if turn.HandledByReviewID.Valid {
		if err := q.FailUnsubmittedReviewBatchForChatTurn(ctx, turn.ID); err != nil {
			return fmt.Errorf("settle unsubmitted review batch for turn %s: %w", turn.ID, err)
		}
	}
	if state == domain.TurnStateCompleted {
		if err := finalizeCompletedTurnPlan(ctx, q, turn, now); err != nil {
			return err
		}
	}
	return nil
}

func finalizeCompletedTurnPlan(
	ctx context.Context,
	q *gen.Queries,
	turn gen.ConversationTurn,
	now time.Time,
) error {
	if turn.PlanJson == "" {
		return nil
	}
	var plan domain.ConversationPlan
	if err := json.Unmarshal([]byte(turn.PlanJson), &plan); err != nil {
		return fmt.Errorf("decode plan for completed turn %s: %w", turn.ID, err)
	}
	if plan.SpansTurns {
		return nil
	}
	changed := false
	for i := range plan.Steps {
		if plan.Steps[i].Status != domain.PlanStepCompleted {
			plan.Steps[i].Status = domain.PlanStepCompleted
			changed = true
		}
	}
	if !changed {
		return nil
	}

	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode finalized plan for turn %s: %w", turn.ID, err)
	}
	if _, err := q.UpdateConversationTurnPlan(ctx, gen.UpdateConversationTurnPlanParams{
		PlanJson:       string(encodedPlan),
		ConversationID: turn.ConversationID,
		ProviderTurnID: turn.ProviderTurnID,
	}); err != nil {
		return fmt.Errorf("finalize turn plan %s: %w", turn.ID, err)
	}

	detail := map[string]any{"event": "plan", "steps": plan.Steps}
	if plan.Explanation != "" {
		detail["explanation"] = plan.Explanation
	}
	encodedDetail, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode finalized plan activity for turn %s: %w", turn.ID, err)
	}
	if err := q.FinalizeConversationPlanActivity(ctx, gen.FinalizeConversationPlanActivityParams{
		Summary:        fmt.Sprintf("Plan %d/%d steps done", len(plan.Steps), len(plan.Steps)),
		DetailJson:     string(encodedDetail),
		UpdatedAt:      now,
		ConversationID: turn.ConversationID,
		TurnID:         sql.NullString{String: turn.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("finalize plan activity for turn %s: %w", turn.ID, err)
	}
	return nil
}

func terminalActivityStatus(state domain.TurnState) domain.ActivityStatus {
	switch state {
	case domain.TurnStateCompleted:
		return domain.ActivityStatusCompleted
	case domain.TurnStateRecovered:
		return domain.ActivityStatusRecovered
	case domain.TurnStateInterrupted:
		return domain.ActivityStatusCancelled
	default:
		return domain.ActivityStatusFailed
	}
}

func (s *Store) SettleOrphanedTurns(ctx context.Context, session domain.SessionID, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailOrphanedConversationActivities(ctx,
		gen.FailOrphanedConversationActivitiesParams{
			UpdatedAt:          now,
			HandledBySessionID: session,
		}); err != nil {
		return fmt.Errorf("settle orphaned activities for %s: %w", session, err)
	}
	if err := q.SettleOrphanedConversationTurns(ctx,
		gen.SettleOrphanedConversationTurnsParams{
			CompletedAt:        sql.NullTime{Time: now, Valid: true},
			HandledBySessionID: session,
		}); err != nil {
		return fmt.Errorf("settle orphaned turns for %s: %w", session, err)
	}
	return nil
}

func (s *Store) SettleUnboundRunningTurn(ctx context.Context, conversationID string, session domain.SessionID, turnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	turn, err := q.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select unbound running turn %s: %w", turnID, err)
	}
	if turn.ConversationID != conversationID || turn.HandledBySessionID != session || turn.State != domain.TurnStateRunning || turn.ProviderTurnID != "" || turn.RolledBackAt.Valid {
		return nil
	}
	if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
		State: domain.TurnStateFailed, ErrorMessage: "provider delivery unconfirmed after reconnect",
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ID: turnID,
	}); err != nil {
		return fmt.Errorf("settle unbound running turn %s: %w", turnID, err)
	}
	return nil
}

func (s *Store) CleanupOwnedControllerWork(
	ctx context.Context,
	session domain.SessionID,
	conversationID, generation string,
	now time.Time,
) (owned bool, err error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return cleanupOwnedControllerWork(ctx, q, session, conversationID, generation, now)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.inTx(ctx, "clean up owned Chat controller work", func(q *gen.Queries) error {
		var cleanupErr error
		owned, cleanupErr = cleanupOwnedControllerWork(
			ctx, q, session, conversationID, generation, now)
		return cleanupErr
	})
	return owned, err
}

func cleanupOwnedControllerWork(
	ctx context.Context,
	q *gen.Queries,
	session domain.SessionID,
	conversationID, generation string,
	now time.Time,
) (bool, error) {
	owner, err := q.GetSession(ctx, session)
	if err != nil {
		return false, fmt.Errorf("read controller generation for %s: %w", session, err)
	}
	if owner.ControllerGeneration != generation {
		return false, nil
	}
	if err := q.FailOrphanedConversationActivities(ctx,
		gen.FailOrphanedConversationActivitiesParams{
			UpdatedAt: now, HandledBySessionID: session,
		}); err != nil {
		return false, fmt.Errorf("settle orphaned activities for %s: %w", session, err)
	}
	if err := q.SettleOrphanedConversationTurns(ctx,
		gen.SettleOrphanedConversationTurnsParams{
			CompletedAt:        sql.NullTime{Time: now, Valid: true},
			HandledBySessionID: session,
		}); err != nil {
		return false, fmt.Errorf("settle orphaned turns for %s: %w", session, err)
	}
	if err := q.FailPendingConversationRequestsForSession(ctx,
		gen.FailPendingConversationRequestsForSessionParams{
			UpdatedAt:            now,
			TargetConversationID: conversationID,
			HandledBySessionID:   session,
		}); err != nil {
		return false, fmt.Errorf("fail pending requests for %s on %s: %w", session, conversationID, err)
	}
	return true, nil
}

func (s *Store) CleanupOwnedReviewControllerWork(
	ctx context.Context,
	reviewID, conversationID, generation string,
	now time.Time,
) (owned bool, err error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversationID, generation, now)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "clean up owned reviewer Chat work", func(q *gen.Queries) error {
		var cleanupErr error
		owned, cleanupErr = cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversationID, generation, now)
		return cleanupErr
	})
	return owned, err
}

func cleanupOwnedReviewControllerWork(
	ctx context.Context,
	q *gen.Queries,
	reviewID, conversationID, generation string,
	now time.Time,
) (bool, error) {
	owner, err := q.GetReviewByID(ctx, reviewID)
	if err != nil {
		return false, fmt.Errorf("read reviewer controller generation for %s: %w", reviewID, err)
	}
	if owner.ControllerGeneration != generation {
		return false, nil
	}
	review := sql.NullString{String: reviewID, Valid: true}
	if err := q.FailOrphanedReviewActivities(ctx, gen.FailOrphanedReviewActivitiesParams{
		UpdatedAt: now, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer activities for %s: %w", reviewID, err)
	}
	if err := q.SettleOrphanedReviewTurns(ctx, gen.SettleOrphanedReviewTurnsParams{
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer turns for %s: %w", reviewID, err)
	}
	if err := q.FailPendingReviewRequests(ctx, gen.FailPendingReviewRequestsParams{
		UpdatedAt: now, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer requests for %s: %w", reviewID, err)
	}
	return true, nil
}

func (s *Store) ListVisibleRunningTurnProviderIDs(
	ctx context.Context,
	conversationID string,
) ([]string, error) {
	providerTurnIDs, err := s.qr.ListVisibleRunningTurnsForConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list visible running turns for %s: %w", conversationID, err)
	}
	return providerTurnIDs, nil
}

func (s *Store) SetConversationSettings(
	ctx context.Context,
	conversationID string,
	settings domain.ConversationSettings,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdateConversationTurnSettings(ctx, gen.UpdateConversationTurnSettingsParams{
		Model:           nullableString(settings.Model),
		ReasoningEffort: nullableString(settings.ReasoningEffort),
		ApprovalMode:    nullableString(string(settings.ApprovalMode)),
		OpencodeMode:    settings.OpenCodeMode,
		UpdatedAt:       now,
		ID:              conversationID,

		AutoCompactWindow: settings.AutoCompactWindow,
	}); err != nil {
		return fmt.Errorf("set conversation settings for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) MarkCompacted(ctx context.Context, conversationID string, at time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.MarkConversationCompacted(ctx, gen.MarkConversationCompactedParams{
		CompactedAt: sql.NullTime{Time: at, Valid: true},
		UpdatedAt:   at,
		ID:          conversationID,
	}); err != nil {
		return fmt.Errorf("mark conversation %s compacted: %w", conversationID, err)
	}
	return nil
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func (s *Store) RecordUsage(
	ctx context.Context,
	conversationID string,
	usage domain.ConversationUsage,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationUsage(ctx, gen.UpdateConversationUsageParams{
		ContextUsed:       nullablePositive(usage.ContextUsed),
		ContextWindow:     nullablePositive(usage.ContextWindow),
		UsageInputTokens:  nullablePositive(usage.InputTokens),
		UsageOutputTokens: nullablePositive(usage.OutputTokens),
		UsageCachedTokens: nullablePositive(usage.CachedTokens),
		UsageTotalTokens:  nullablePositive(usage.TotalTokens),
		UsageCost:         nullableCost(usage.Cost),
		UsageCurrency:     nullableString(usage.Currency),
		ID:                conversationID,
	}); err != nil {
		return fmt.Errorf("record usage for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) RecordRateLimits(
	ctx context.Context,
	conversationID string,
	limits domain.ConversationRateLimits,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationRateLimits(ctx, gen.UpdateConversationRateLimitsParams{
		RateLimitPrimaryPercent:    nullablePercent(limits.PrimaryUsedPercent),
		RateLimitSecondaryPercent:  nullablePercent(limits.SecondaryUsedPercent),
		RateLimitPrimaryResetsIn:   nullablePositive(limits.PrimaryResetsInSeconds),
		RateLimitSecondaryResetsIn: nullablePositive(limits.SecondaryResetsInSeconds),
		RateLimitPlan:              nullableString(limits.PlanLabel),
		ID:                         conversationID,
	}); err != nil {
		return fmt.Errorf("record rate limits for %s: %w", conversationID, err)
	}
	return nil
}

func nullablePositive(value int64) sql.NullInt64 {
	if value <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullablePercent(value float64) sql.NullFloat64 {
	if value < 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: value, Valid: true}
}

func (s *Store) RecordModelReroute(
	ctx context.Context,
	conversationID string,
	reroute domain.ConversationModelReroute,
) error {
	encoded, err := json.Marshal(reroute)
	if err != nil {
		return fmt.Errorf("encode model reroute for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationModelReroute(ctx,
		gen.UpdateConversationModelRerouteParams{
			ModelRerouteJson: sql.NullString{String: string(encoded), Valid: true},
			UpdatedAt:        reroute.At,
			ID:               conversationID,
		}); err != nil {
		return fmt.Errorf("record model reroute for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) RecordAccount(
	ctx context.Context,
	conversationID string,
	account domain.ConversationAccount,
	now time.Time,
) error {
	encoded, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("encode account for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationAccount(ctx, gen.UpdateConversationAccountParams{
		AccountJson: sql.NullString{String: string(encoded), Valid: true},
		UpdatedAt:   now,
		ID:          conversationID,
	}); err != nil {
		return fmt.Errorf("record account for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) ConversationAccount(ctx context.Context, conversationID string) (*domain.ConversationAccount, error) {
	row, err := s.conversationReader(ctx).SelectConversationByID(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return conversationToDomain(row).Account, nil
}

func (s *Store) ReconcileConversationAuthentication(ctx context.Context, conversationID, generation, providerTurnID string, now time.Time) (*domain.ConversationAccount, error) {
	if _, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); !ok {
		if account, verifiedAt, err := authenticationEvidence(ctx, s.qr, conversationID, generation, providerTurnID); err != nil || verifiedAt == nil {
			return account, err
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		tx, err := s.writeDB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		account, err := s.ReconcileConversationAuthentication(context.WithValue(ctx, conversationProjectionTxKey{}, s.qw.WithTx(tx)), conversationID, generation, providerTurnID, now)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return account, nil
	}
	account, verifiedAt, err := authenticationEvidence(ctx, s.conversationReader(ctx), conversationID, generation, providerTurnID)
	if err != nil || verifiedAt == nil {
		return account, err
	}
	markAuthenticated(account, *verifiedAt)
	if err := s.RecordAccount(ctx, conversationID, *account, now); err != nil {
		return nil, err
	}
	return account, nil
}

func authenticationEvidence(ctx context.Context, q *gen.Queries, conversationID, generation, providerTurnID string) (*domain.ConversationAccount, *time.Time, error) {
	row, err := q.SelectConversationByID(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	account := conversationToDomain(row).Account
	if account == nil {
		if providerTurnID == "" {
			return nil, nil, nil
		}
		account = &domain.ConversationAccount{AuthenticationState: "unknown"}
	}
	cutoff := row.CreatedAt
	if account.ReauthRequiredAt != nil {
		cutoff = *account.ReauthRequiredAt
	}
	if account.AuthChangedAt != nil && account.AuthChangedAt.After(cutoff) {
		cutoff = *account.AuthChangedAt
	}
	turn, err := q.SelectVerifiedAuthenticationTurn(ctx, gen.SelectVerifiedAuthenticationTurnParams{
		ConversationID: conversationID, Generation: generation, ProviderTurnID: providerTurnID,
		AfterAt: sql.NullTime{Time: cutoff, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return account, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if account.AuthVerifiedAt != nil && !turn.CompletedAt.Time.After(*account.AuthVerifiedAt) && account.ReauthRequiredAt == nil {
		return account, nil, nil
	}
	return account, &turn.CompletedAt.Time, nil
}

type ConversationAwaitingLogin struct {
	ConversationID string
	SessionID      domain.SessionID
	Harness        domain.AgentHarness
}

func (s *Store) ConversationsAwaitingLogin(ctx context.Context) ([]ConversationAwaitingLogin, error) {
	rows, err := s.qr.SelectConversationsAwaitingLogin(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationAwaitingLogin, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConversationAwaitingLogin(row))
	}
	return out, nil
}

func (s *Store) ConfirmConversationLogin(ctx context.Context, conversationID string, at, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cleared := false
	err := s.inTx(ctx, "confirm conversation login", func(q *gen.Queries) error {
		row, err := q.SelectConversationByID(ctx, conversationID)
		if err != nil {
			return err
		}
		account := conversationToDomain(row).Account
		if account == nil || account.ReauthRequiredAt == nil || !account.ReauthRequiredAt.Before(at) {
			return nil
		}
		markAuthenticated(account, at)
		cleared = true
		return s.RecordAccount(context.WithValue(ctx, conversationProjectionTxKey{}, q), conversationID, *account, now)
	})
	return cleared && err == nil, err
}

func (s *Store) RequireSessionLogin(ctx context.Context, id domain.SessionID, reason string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "require session login", func(q *gen.Queries) error {
		row, err := q.SelectConversationBySession(ctx, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		record := conversationToDomain(row)
		account := domain.ConversationAccount{AuthenticationState: "unknown"}
		if record.Account != nil {
			if record.Account.ReauthRequiredAt != nil {
				return nil
			}
			account = *record.Account
		}
		account.RequireLogin(reason, uuid.NewString(), now)
		return s.RecordAccount(context.WithValue(ctx, conversationProjectionTxKey{}, q), record.ID, account, now)
	})
}

func markAuthenticated(account *domain.ConversationAccount, verifiedAt time.Time) {
	if account.ReauthRequiredAt != nil && account.LastAuthFailureAt == nil {
		account.LastAuthFailureAt = account.ReauthRequiredAt
		account.LastAuthFailureReason = account.ReauthReason
	}
	account.AuthenticationState = "authenticated"
	account.AuthVerifiedAt = &verifiedAt
	account.ReauthRequiredAt = nil
	account.ReauthReason = ""
}

func (s *Store) RecordThreadState(
	ctx context.Context,
	conversationID string,
	state domain.ConversationThreadState,
) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode thread state for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationThreadState(ctx,
		gen.UpdateConversationThreadStateParams{
			ThreadStateJson: sql.NullString{String: string(encoded), Valid: true},
			ID:              conversationID,
		}); err != nil {
		return fmt.Errorf("record thread state for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) RecordMCPServers(
	ctx context.Context,
	conversationID string,
	servers []domain.ConversationMCPServer,
) error {
	if servers == nil {
		servers = []domain.ConversationMCPServer{}
	}
	encoded, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encode mcp servers for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationMcpServers(ctx,
		gen.UpdateConversationMcpServersParams{
			McpServersJson: sql.NullString{String: string(encoded), Valid: true},
			ID:             conversationID,
		}); err != nil {
		return fmt.Errorf("record mcp servers for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) SetTurnPlan(
	ctx context.Context,
	conversationID, providerTurnID string,
	plan domain.ConversationPlan,
) (bool, error) {
	if providerTurnID == "" {
		return false, nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return false, fmt.Errorf("encode plan for %s: %w", providerTurnID, err)
	}

	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.UpdateConversationTurnPlan(ctx, gen.UpdateConversationTurnPlanParams{
		PlanJson:       string(encoded),
		ConversationID: conversationID,
		ProviderTurnID: providerTurnID,
	})
	if err != nil {
		return false, fmt.Errorf("set turn plan for %s: %w", providerTurnID, err)
	}
	return rows > 0, nil
}

func (s *Store) ReconcileSpanningPlans(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	return reconcileSpanningPlans(ctx, q, conversationID, now)
}

func reconcileSpanningPlans(ctx context.Context, q *gen.Queries, conversationID string, now time.Time) error {
	copies, err := q.SelectSpanningPlanCopiesOnActivePath(ctx, conversationID)
	if err != nil {
		return fmt.Errorf("select spanning plan copies in %s: %w", conversationID, err)
	}
	current := -1
	var currentAt time.Time
	for index, planCopy := range copies {
		at := spanningPlanWrittenAt(planCopy)
		if current < 0 || !at.Before(currentAt) {
			current, currentAt = index, at
		}
	}
	for index, planCopy := range copies {
		superseded := index != current
		if err := q.SetConversationPlanActivitySuperseded(ctx, gen.SetConversationPlanActivitySupersededParams{
			Superseded: superseded,
			UpdatedAt:  now,
			ID:         planCopy.ID,
		}); err != nil {
			return fmt.Errorf("mark plan row %s: %w", planCopy.ID, err)
		}
		if planCopy.TurnID.Valid {
			if err := q.SetConversationTurnPlanSuperseded(ctx, gen.SetConversationTurnPlanSupersededParams{
				Superseded: superseded,
				ID:         planCopy.TurnID.String,
			}); err != nil {
				return fmt.Errorf("mark turn plan %s: %w", planCopy.TurnID.String, err)
			}
		}
	}
	return nil
}

func spanningPlanWrittenAt(planCopy gen.SelectSpanningPlanCopiesOnActivePathRow) time.Time {
	var detail struct {
		WrittenAt time.Time `json:"writtenAt"`
	}
	if json.Unmarshal([]byte(planCopy.DetailJson), &detail) == nil && !detail.WrittenAt.IsZero() {
		return detail.WrittenAt
	}
	if planCopy.TurnID.Valid && planCopy.TurnCompletedAt.Valid {
		return planCopy.TurnCompletedAt.Time
	}
	return planCopy.CreatedAt
}

const MaxStreamedTextChars = 32 * 1024

func (s *Store) AppendActivityStreamedText(
	ctx context.Context,
	conversationID, providerItemID, delta string,
	now time.Time,
) (bool, error) {
	if delta == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.AppendConversationActivityStreamedText(ctx,
		gen.AppendConversationActivityStreamedTextParams{
			Delta:          delta,
			MaxTextChars:   MaxStreamedTextChars,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("append streamed text to %s: %w", providerItemID, err)
	}
	if rows > 0 {
		return true, nil
	}
	found, err := q.ConversationActivityExistsForProviderItem(ctx,
		gen.ConversationActivityExistsForProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("find streamed text activity %s after no-op append: %w", providerItemID, err)
	}
	return found, nil
}

func (s *Store) SettleActivityStreamedText(
	ctx context.Context,
	conversationID, providerItemID, text string,
	now time.Time,
) (bool, error) {
	if providerItemID == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.SettleConversationActivityStreamedText(ctx,
		gen.SettleConversationActivityStreamedTextParams{
			StreamedText:   text,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("settle streamed text on %s: %w", providerItemID, err)
	}
	return rows > 0, nil
}

func (s *Store) NextQueuedTurn(ctx context.Context, conversationID string) (domain.QueuedTurn, error) {
	row, err := s.qr.SelectNextQueuedConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QueuedTurn{}, ErrNoQueuedTurn
	}
	if err != nil {
		return domain.QueuedTurn{}, fmt.Errorf("select next queued turn for %s: %w", conversationID, err)
	}
	return domain.QueuedTurn{
		TurnID:              row.ID,
		Text:                row.Text,
		ClientMessageID:     row.ClientMessageID,
		Origin:              row.Origin,
		DeliveryContentJSON: row.DeliveryContentJson,
		SenderSessionID:     row.SenderSessionID,
		SenderProjectID:     row.SenderProjectID,
		SenderDisplayName:   row.SenderDisplayName,
	}, nil
}

func (s *Store) ListQueuedBatch(ctx context.Context, conversationID string, limit int) ([]domain.QueuedTurn, error) {
	rows, err := s.qr.SelectQueuedConversationBatch(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list queued batch: %w", err)
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return nil, domain.ErrNoQueuedTurn
	}
	batch := make([]domain.QueuedTurn, 0, len(rows))
	for _, row := range rows {
		batch = append(batch, domain.QueuedTurn{
			TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
			Origin: row.Origin, DeliveryContentJSON: row.DeliveryContentJson,
			SenderSessionID: row.SenderSessionID, SenderProjectID: row.SenderProjectID,
			SenderDisplayName: row.SenderDisplayName,
		})
	}
	return batch, nil
}

func (s *Store) ClaimQueuedBatch(ctx context.Context, conversationID string, dispatch bool, limit int, now time.Time) ([]domain.QueuedTurn, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var batch []domain.QueuedTurn
	err := s.inTx(ctx, "claim queued batch", func(q *gen.Queries) error {
		rows, err := q.SelectQueuedConversationBatch(ctx, conversationID)
		if err != nil {
			return err
		}
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		for _, row := range rows {
			changed, err := q.ReserveQueuedConversationTurnForPromotion(ctx, gen.ReserveQueuedConversationTurnForPromotionParams{
				PromotionStartedAt: sql.NullTime{Time: now, Valid: true}, ID: row.ID, ConversationID: conversationID,
			})
			if checkErr := expectBatchRow("reserve queued batch turn", row.ID, changed, err); checkErr != nil {
				return checkErr
			}
			batch = append(batch, domain.QueuedTurn{
				TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
				Origin: row.Origin, DeliveryContentJSON: row.DeliveryContentJson,
				SenderSessionID: row.SenderSessionID, SenderProjectID: row.SenderProjectID,
				SenderDisplayName: row.SenderDisplayName,
			})
		}
		if dispatch && len(batch) > 0 {
			if err := q.MarkConversationTurnStarted(ctx, gen.MarkConversationTurnStartedParams{ID: batch[0].TurnID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim queued batch: %w", err)
	}
	if len(batch) == 0 {
		return nil, domain.ErrNoQueuedTurn
	}
	return batch, nil
}

func (s *Store) CompleteQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, providerTurnID, steerActivityID string, now time.Time) error {
	if len(turnIDs) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete queued batch", func(q *gen.Queries) error {
		target, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID, ProviderTurnID: providerTurnID,
		})
		if err != nil {
			return err
		}
		for _, id := range turnIDs {
			changed, err := q.CompleteQueuedConversationBatchMember(ctx, gen.CompleteQueuedConversationBatchMemberParams{
				CompletedAt:          sql.NullTime{Time: now, Valid: true},
				PromotedToTurnID:     sql.NullString{String: target.ID, Valid: true},
				BatchDeliveryKind:    batchDeliveryKind(steerActivityID),
				BatchDeliverySteerID: steerActivityID,
				ID:                   id, ConversationID: conversationID,
			})
			if checkErr := expectBatchRow("complete queued batch turn", id, changed, err); checkErr != nil {
				return checkErr
			}
		}
		return nil
	})
}

func (s *Store) ReleaseQueuedBatch(ctx context.Context, conversationID string, turnIDs []string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "release queued batch", func(q *gen.Queries) error {
		for _, id := range turnIDs {
			changed, err := q.ReleaseQueuedConversationTurnPromotion(ctx, gen.ReleaseQueuedConversationTurnPromotionParams{ID: id, ConversationID: conversationID})
			if checkErr := expectBatchRow("release queued batch turn", id, changed, err); checkErr != nil {
				return checkErr
			}
		}
		return nil
	})
}

func (s *Store) FailQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, reason string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "fail queued batch", func(q *gen.Queries) error {
		for _, id := range turnIDs {
			if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
				State: domain.TurnStateFailed, ErrorMessage: reason,
				CompletedAt: sql.NullTime{Time: now, Valid: true}, ID: id,
			}); err != nil {
				return fmt.Errorf("fail queued batch turn %s: %w", id, err)
			}
		}
		return nil
	})
}

func (s *Store) FailReservedQueuedBatch(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	return q.FailReservedQueuedConversationTurns(ctx, gen.FailReservedQueuedConversationTurnsParams{
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ConversationID: conversationID,
	})
}

func (s *Store) ReserveQueuedTurnForPromotion(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) (domain.QueuedTurn, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReserveQueuedConversationTurnForPromotion(ctx,
		gen.ReserveQueuedConversationTurnForPromotionParams{
			PromotionStartedAt: sql.NullTime{Time: now, Valid: true},
			ID:                 turnID, ConversationID: conversationID,
		})
	if err != nil {
		return domain.QueuedTurn{}, fmt.Errorf("reserve queued turn %s: %w", turnID, err)
	}
	if rows == 0 {
		return domain.QueuedTurn{}, fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	row, err := s.qw.SelectReservedConversationTurnForPromotion(ctx,
		gen.SelectReservedConversationTurnForPromotionParams{ID: turnID, ConversationID: conversationID})
	if err != nil {
		_, _ = s.qw.ReleaseQueuedConversationTurnPromotion(ctx,
			gen.ReleaseQueuedConversationTurnPromotionParams{ID: turnID, ConversationID: conversationID})
		return domain.QueuedTurn{}, fmt.Errorf("load reserved queued turn %s: %w", turnID, err)
	}
	return domain.QueuedTurn{
		TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
		Origin: row.Origin, AuthoredByUser: row.AuthoredByUser != 0,
		DeliveryContentJSON: row.DeliveryContentJson,
	}, nil
}

func (s *Store) ReleaseQueuedTurnPromotion(
	ctx context.Context,
	conversationID, turnID string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseQueuedConversationTurnPromotion(ctx,
		gen.ReleaseQueuedConversationTurnPromotionParams{ID: turnID, ConversationID: conversationID})
	if err != nil {
		return fmt.Errorf("release queued turn promotion %s: %w", turnID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	return nil
}

func (s *Store) CompleteQueuedTurnPromotion(
	ctx context.Context,
	conversationID, sourceTurnID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete queued turn promotion", func(q *gen.Queries) error {
		target, err := q.SelectConversationTurnByProviderID(ctx,
			gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID, ProviderTurnID: providerTurnID,
			})
		if err != nil {
			return fmt.Errorf("select promotion target %s: %w", providerTurnID, err)
		}
		txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
		if err := s.UpsertActivity(txCtx, conversationID, providerTurnID, activity, now); err != nil {
			return err
		}
		rows, err := q.CompleteQueuedConversationTurnPromotion(ctx,
			gen.CompleteQueuedConversationTurnPromotionParams{
				CompletedAt:      sql.NullTime{Time: now, Valid: true},
				PromotedToTurnID: sql.NullString{String: target.ID, Valid: true},
				ID:               sourceTurnID,
				ConversationID:   conversationID,
			})
		if err != nil {
			return fmt.Errorf("complete source turn %s: %w", sourceTurnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, sourceTurnID)
		}
		return nil
	})
}

func (s *Store) CancelQueuedTurns(
	ctx context.Context,
	conversationID string,
	cutoff, now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CancelQueuedConversationTurns(ctx, gen.CancelQueuedConversationTurnsParams{
		CompletedAt:    sql.NullTime{Time: now, Valid: true},
		ConversationID: conversationID,
		RequestedAt:    cutoff,
	}); err != nil {
		return fmt.Errorf("cancel queued turns for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) CancelAllQueuedTurns(
	ctx context.Context,
	conversationID string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CancelAllQueuedConversationTurns(ctx, gen.CancelAllQueuedConversationTurnsParams{
		CompletedAt:    sql.NullTime{Time: now, Valid: true},
		ConversationID: conversationID,
	}); err != nil {
		return fmt.Errorf("cancel all queued turns for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) CancelQueuedTurnByID(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.CancelQueuedConversationTurnByID(ctx,
		gen.CancelQueuedConversationTurnByIDParams{
			CompletedAt:    sql.NullTime{Time: now, Valid: true},
			ID:             turnID,
			ConversationID: conversationID,
		})
	if err != nil {
		return fmt.Errorf("cancel queued turn %s: %w", turnID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	return nil
}

var ErrInvalidQueuedTurnOrder = errors.New("invalid queued turn order")

func (s *Store) ReorderQueuedTurns(
	ctx context.Context,
	conversationID string,
	turnIDs []string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	current, err := s.qr.SelectQueuedConversationTurnOrder(ctx, conversationID)
	if err != nil {
		return fmt.Errorf("list queued turns for reorder: %w", err)
	}
	if len(current) != len(turnIDs) {
		return fmt.Errorf("%w: got %d ids for %d queued turns", ErrInvalidQueuedTurnOrder, len(turnIDs), len(current))
	}
	if len(current) == 0 {
		return nil
	}

	currentByID := make(map[string]time.Time, len(current))
	for _, row := range current {
		currentByID[row.ID] = row.RequestedAt
	}
	seen := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		if _, ok := currentByID[turnID]; !ok {
			return fmt.Errorf("%w: unknown turn %s", ErrInvalidQueuedTurnOrder, turnID)
		}
		if _, dup := seen[turnID]; dup {
			return fmt.Errorf("%w: duplicate turn %s", ErrInvalidQueuedTurnOrder, turnID)
		}
		seen[turnID] = struct{}{}
	}

	requestedAts := make([]time.Time, len(current))
	for i, row := range current {
		requestedAts[i] = row.RequestedAt
	}
	sort.Slice(requestedAts, func(i, j int) bool {
		return requestedAts[i].Before(requestedAts[j])
	})

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin queued turn reorder: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	for i, turnID := range turnIDs {
		rows, err := q.UpdateQueuedConversationTurnRequestedAt(ctx,
			gen.UpdateQueuedConversationTurnRequestedAtParams{
				RequestedAt:    requestedAts[i],
				ID:             turnID,
				ConversationID: conversationID,
			})
		if err != nil {
			return fmt.Errorf("reorder queued turn %s: %w", turnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit queued turn reorder: %w", err)
	}
	return nil
}

func (s *Store) QueuedTurnMessage(ctx context.Context, conversationID, turnID string) (domain.ConversationMessage, error) {
	row, err := s.qr.SelectQueuedConversationMessage(ctx, gen.SelectQueuedConversationMessageParams{
		ConversationID: conversationID, TurnID: nullableString(turnID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationMessage{}, ErrQueuedTurnNotAvailable
	}
	if err != nil {
		return domain.ConversationMessage{}, err
	}
	return messageToDomain(row), nil
}

func (s *Store) UpdateQueuedTurnMessage(
	ctx context.Context,
	conversationID, turnID, text, contentJSON string,
	revision int64,
	now time.Time,
	delivery domain.ConversationQueuedEditDelivery,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "update queued turn message", func(q *gen.Queries) error {
		if delivery.ClientMessageID != "" {
			hash, err := q.SelectConversationQueuedEditDelivery(ctx, gen.SelectConversationQueuedEditDeliveryParams{
				ConversationID: conversationID, ClientMessageID: delivery.ClientMessageID,
			})
			if err == nil {
				if hash != delivery.RequestHash {
					return ErrQueuedEditDeliveryConflict
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		rows, err := q.UpdateQueuedConversationMessageText(ctx,
			gen.UpdateQueuedConversationMessageTextParams{
				Text: text, DeliveryContentJson: contentJSON, Revision: revision,
				UpdatedAt: now, ConversationID: conversationID,
				TurnID: sql.NullString{String: turnID, Valid: true},
			})
		if err != nil {
			return fmt.Errorf("update queued turn message %s: %w", turnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
		}
		if delivery.ClientMessageID != "" {
			return q.InsertConversationQueuedEditDelivery(ctx, gen.InsertConversationQueuedEditDeliveryParams{
				ConversationID: conversationID, ClientMessageID: delivery.ClientMessageID,
				RequestHash: delivery.RequestHash, CreatedAt: now,
			})
		}
		return nil
	})
}

func (s *Store) SettleTurnByID(
	ctx context.Context,
	turnID string,
	state domain.TurnState,
	errMessage string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
		State:        state,
		ErrorMessage: errMessage,
		FailureCode:  "",
		CompletedAt:  sql.NullTime{Time: now, Valid: true},
		ID:           turnID,
	}); err != nil {
		return fmt.Errorf("settle turn %s: %w", turnID, err)
	}
	return nil
}

func (s *Store) AppendAssistantDelta(
	ctx context.Context,
	conversationID, providerItemID, providerTurnID, delta, messageID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	_, err := q.SelectConversationMessageByProviderItem(ctx,
		gen.SelectConversationMessageByProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	switch {
	case err == nil:
		if appendErr := q.AppendConversationMessageDelta(ctx,
			gen.AppendConversationMessageDeltaParams{
				Text:           delta,
				UpdatedAt:      now,
				ConversationID: conversationID,
				ProviderItemID: providerItemID,
			}); appendErr != nil {
			return fmt.Errorf("append delta to %s: %w", providerItemID, appendErr)
		}
		return nil

	case errors.Is(err, sql.ErrNoRows):
		var turnID sql.NullString
		streaming := int64(1)
		if providerTurnID != "" {
			turn, turnErr := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID,
				ProviderTurnID: providerTurnID,
			})
			if turnErr == nil {
				turnID = sql.NullString{String: turn.ID, Valid: true}
				if turn.State.Terminal() {
					streaming = 0
				}
			} else if !errors.Is(turnErr, sql.ErrNoRows) {
				return fmt.Errorf("lookup turn %s for assistant delta: %w", providerTurnID, turnErr)
			}
		}
		return s.inTx(ctx, "insert assistant message", func(q *gen.Queries) error {
			sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
				UpdatedAt: now,
				ID:        conversationID,
			})
			if seqErr != nil {
				return fmt.Errorf("allocate sequence: %w", seqErr)
			}
			return q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
				ID:             messageID,
				ConversationID: conversationID,
				TurnID:         turnID,
				Sequence:       sequence,
				Role:           domain.MessageRoleAssistant,
				Origin:         domain.MessageOriginProvider,
				Text:           delta,
				Streaming:      streaming,
				ProviderItemID: providerItemID,
				CreatedAt:      now,
				UpdatedAt:      now,
			})
		})

	default:
		return fmt.Errorf("lookup message %s: %w", providerItemID, err)
	}
}

func (s *Store) SettleAssistantMessage(
	ctx context.Context,
	conversationID, providerItemID, providerTurnID, text, messageID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	existing, err := q.SelectConversationMessageByProviderItem(ctx,
		gen.SelectConversationMessageByProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		turnID := s.turnIDFor(ctx, conversationID, providerTurnID)
		return s.inTx(ctx, "insert assistant message", func(q *gen.Queries) error {
			sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
				UpdatedAt: now,
				ID:        conversationID,
			})
			if seqErr != nil {
				return fmt.Errorf("allocate sequence: %w", seqErr)
			}
			return q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
				ID:             messageID,
				ConversationID: conversationID,
				TurnID:         turnID,
				Sequence:       sequence,
				Role:           domain.MessageRoleAssistant,
				Origin:         domain.MessageOriginProvider,
				Text:           text,
				ProviderItemID: providerItemID,
				CreatedAt:      now,
				UpdatedAt:      now,
			})
		})
	}
	if err != nil {
		return fmt.Errorf("lookup message %s: %w", providerItemID, err)
	}
	if text == "" {
		text = existing.Text
	}

	if err := q.SettleConversationMessage(ctx, gen.SettleConversationMessageParams{
		Text:           text,
		UpdatedAt:      now,
		ConversationID: conversationID,
		ProviderItemID: providerItemID,
	}); err != nil {
		return fmt.Errorf("settle message %s: %w", providerItemID, err)
	}
	return nil
}

func (s *Store) ActivityStatusByProviderItem(ctx context.Context, conversationID, providerItemID string) (domain.ActivityStatus, bool, error) {
	row, err := s.qr.SelectConversationActivityByProviderItem(ctx, gen.SelectConversationActivityByProviderItemParams{
		ConversationID: conversationID,
		ProviderItemID: providerItemID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Status, true, nil
}

func aoActionStatusSettled(status domain.ActivityStatus) bool {
	switch status {
	case domain.ActivityStatusCompleted, domain.ActivityStatusFailed, domain.ActivityStatusCancelled, domain.ActivityStatusResolved:
		return true
	default:
		return false
	}
}

func (s *Store) UpsertActivity(
	ctx context.Context,
	conversationID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	detail := string(activity.Detail)

	if activity.ProviderItemID != "" {
		row, err := q.SelectConversationActivityByProviderItem(ctx,
			gen.SelectConversationActivityByProviderItemParams{
				ConversationID: conversationID,
				ProviderItemID: activity.ProviderItemID,
			})
		if err == nil {
			if activity.Kind == domain.ActivityKindAOAction &&
				activity.Status == domain.ActivityStatusRunning &&
				aoActionStatusSettled(row.Status) {
				return nil
			}
			if settleErr := q.SettleConversationActivity(ctx,
				gen.SettleConversationActivityParams{
					Status:         activity.Status,
					Summary:        activity.Summary,
					DetailJson:     detail,
					UpdatedAt:      now,
					ConversationID: conversationID,
					ProviderItemID: activity.ProviderItemID,
				}); settleErr != nil {
				return fmt.Errorf("settle activity %s: %w", activity.ProviderItemID, settleErr)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup activity %s: %w", activity.ProviderItemID, err)
		}
	}

	if activity.RequestID != "" {
		if _, err := q.SelectConversationActivityByRequest(ctx,
			gen.SelectConversationActivityByRequestParams{
				ConversationID: conversationID,
				RequestID:      activity.RequestID,
			}); err == nil {
			if settleErr := q.SettleConversationActivityByRequest(ctx,
				gen.SettleConversationActivityByRequestParams{
					Status:         activity.Status,
					Summary:        activity.Summary,
					DetailJson:     detail,
					UpdatedAt:      now,
					ConversationID: conversationID,
					RequestID:      activity.RequestID,
				}); settleErr != nil {
				return fmt.Errorf("settle activity %s: %w", activity.RequestID, settleErr)
			}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup activity %s: %w", activity.RequestID, err)
		}
	}

	turnID := s.turnIDFor(ctx, conversationID, providerTurnID)
	return s.inTx(ctx, "insert activity", func(q *gen.Queries) error {
		sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
			UpdatedAt: now,
			ID:        conversationID,
		})
		if seqErr != nil {
			return fmt.Errorf("allocate sequence: %w", seqErr)
		}
		if err := recordSteerInteraction(ctx, q, conversationID, activity, now); err != nil {
			return err
		}
		return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
			ID:             activity.ID,
			ConversationID: conversationID,
			TurnID:         turnID,
			Sequence:       sequence,
			Kind:           activity.Kind,
			Status:         activity.Status,
			Summary:        activity.Summary,
			DetailJson:     detail,
			RequestID:      activity.RequestID,
			ProviderItemID: activity.ProviderItemID,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	})
}

const MaxCommandOutputChars = 64 * 1024

func (s *Store) AppendCommandOutput(
	ctx context.Context,
	conversationID, providerItemID, delta string,
	now time.Time,
) (bool, error) {
	if delta == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.AppendConversationActivityOutput(ctx,
		gen.AppendConversationActivityOutputParams{
			Delta:          delta,
			MaxOutputChars: MaxCommandOutputChars,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("append command output to %s: %w", providerItemID, err)
	}
	if rows > 0 {
		return true, nil
	}
	found, err := q.ConversationActivityExistsForProviderItem(ctx,
		gen.ConversationActivityExistsForProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("find command output activity %s after no-op append: %w", providerItemID, err)
	}
	return found, nil
}

func (s *Store) SetTurnDiff(
	ctx context.Context,
	conversationID, providerTurnID string,
	diff domain.ConversationTurnDiff,
	now time.Time,
) (bool, error) {
	if providerTurnID == "" {
		return false, nil
	}
	encoded, err := json.Marshal(diff)
	if err != nil {
		return false, fmt.Errorf("encode turn diff for %s: %w", providerTurnID, err)
	}

	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.UpdateConversationTurnDiff(ctx, gen.UpdateConversationTurnDiffParams{
		DiffJson:       string(encoded),
		ConversationID: conversationID,
		ProviderTurnID: providerTurnID,
	})
	if err != nil {
		return false, fmt.Errorf("set turn diff for %s: %w", providerTurnID, err)
	}
	return rows > 0, nil
}

func (s *Store) SetTurnArtifacts(
	ctx context.Context,
	conversationID, providerTurnID string,
	artifacts []domain.ConversationTurnArtifact,
) (bool, error) {
	if providerTurnID == "" {
		return false, nil
	}
	encoded := ""
	if len(artifacts) > 0 {
		raw, err := json.Marshal(domain.ConversationTurnArtifacts{Files: artifacts})
		if err != nil {
			return false, fmt.Errorf("encode turn artifacts for %s: %w", providerTurnID, err)
		}
		encoded = string(raw)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.UpdateConversationTurnArtifacts(ctx, gen.UpdateConversationTurnArtifactsParams{
		ArtifactsJson:  encoded,
		ConversationID: conversationID,
		ProviderTurnID: providerTurnID,
	})
	if err != nil {
		return false, fmt.Errorf("set turn artifacts for %s: %w", providerTurnID, err)
	}
	return rows > 0, nil
}

func (s *Store) ResolveApproval(
	ctx context.Context,
	conversationID, requestID, detailJSON string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.ResolveConversationApproval(ctx, gen.ResolveConversationApprovalParams{
		DetailJson:     detailJSON,
		UpdatedAt:      now,
		ConversationID: conversationID,
		RequestID:      requestID,
	}); err != nil {
		return fmt.Errorf("resolve approval %s: %w", requestID, err)
	}
	return nil
}

func (s *Store) HasPendingConversationInteractions(ctx context.Context, conversationID string) (bool, error) {
	pending, err := s.qr.HasPendingConversationInteractions(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("check pending interactions for %s: %w", conversationID, err)
	}
	return pending, nil
}

func (s *Store) FailPendingApprovals(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailPendingConversationApprovals(ctx,
		gen.FailPendingConversationApprovalsParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
		}); err != nil {
		return fmt.Errorf("fail pending approvals for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) FailPendingInputs(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailPendingConversationInputs(ctx,
		gen.FailPendingConversationInputsParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
		}); err != nil {
		return fmt.Errorf("fail pending inputs for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) ProjectProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	return s.projectProviderEvent(ctx, conversationID, session, "", generation, providerEventID, method, payloadJSON, now, project)
}

func (s *Store) ProjectReviewProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	return s.projectProviderEvent(ctx, conversationID, session, reviewID, generation, providerEventID, method, payloadJSON, now, project)
}

func (s *Store) projectProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return projectProviderEventTx(
			ctx, q, conversationID, session, reviewID, generation,
			providerEventID, method, payloadJSON, now, project,
		)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin provider event projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	projected, err := projectProviderEventTx(
		ctx, q, conversationID, session, reviewID, generation,
		providerEventID, method, payloadJSON, now, project,
	)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit provider event %s: %w", method, err)
	}
	return projected, nil
}

func projectProviderEventTx(
	ctx context.Context,
	q *gen.Queries,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if reviewID != "" {
		owner, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return false, fmt.Errorf("read reviewer controller generation for %s: %w", reviewID, err)
		}
		if owner.ControllerGeneration != generation || owner.InterfaceMode != "chat" || owner.SessionID != session {
			return false, nil
		}
	} else {
		owner, err := q.GetSession(ctx, session)
		if err != nil {
			return false, fmt.Errorf("read controller generation for %s: %w", session, err)
		}
		if owner.ControllerGeneration != generation {
			return false, nil
		}
	}
	inserted, err := q.InsertConversationProviderEvent(ctx, gen.InsertConversationProviderEventParams{
		ConversationID:  conversationID,
		SessionID:       session,
		ProviderEventID: providerEventID,
		Method:          method,
		PayloadJson:     payloadJSON,
		ReceivedAt:      now,
	})
	if err != nil {
		return false, fmt.Errorf("archive provider event %s: %w", method, err)
	}
	if inserted == 0 {
		return false, nil
	}
	txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
	if err := project(txCtx); err != nil {
		return false, fmt.Errorf("project provider event %s: %w", method, err)
	}
	return true, nil
}

var ErrConversationTurnNotFound = domain.ErrNoConversationTurn

func (s *Store) TurnByID(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, turnID)
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select turn %s: %w", turnID, err)
	}
	return turnToDomain(row), nil
}

func (s *Store) SetTurnAutoResume(ctx context.Context, conversationID, turnID string, resetsAt *time.Time, enabled bool, resumeError string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if enabled || resumeError != "" {
		turn, err := s.qw.SelectConversationTurnByID(ctx, turnID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNoConversationTurn
		}
		if err != nil {
			return fmt.Errorf("read turn auto-resume consent: %w", err)
		}
		if turn.ConversationID != conversationID {
			return domain.ErrNoConversationTurn
		}
		if !enabled && turn.UsageLimitAutoResume == 0 {
			return nil
		}
		if enabled {
			_, retryErr := s.qw.SelectConversationRetryTurnIDBySource(ctx, gen.SelectConversationRetryTurnIDBySourceParams{
				ConversationID: conversationID, RetryOfTurnID: sql.NullString{String: turnID, Valid: true},
			})
			if retryErr == nil {
				return domain.ErrNoConversationTurn
			}
			if !errors.Is(retryErr, sql.ErrNoRows) {
				return fmt.Errorf("read turn retry receipt: %w", retryErr)
			}
			changed, err := s.qw.SessionAgentAccountChangedSince(ctx, gen.SessionAgentAccountChangedSinceParams{
				SessionID: turn.HandledBySessionID, RequestedAt: turn.RequestedAt,
			})
			if err != nil {
				return fmt.Errorf("read turn account binding: %w", err)
			}
			if changed {
				return ports.ErrAgentAccountChanged
			}
		}
	}
	var armed int64
	if enabled {
		armed = 1
	}
	var reset sql.NullTime
	if resetsAt != nil {
		reset = timeToNullTime(*resetsAt)
	}
	n, err := s.qw.SetConversationTurnAutoResume(ctx, gen.SetConversationTurnAutoResumeParams{
		ID: turnID, ConversationID: conversationID, ResetsAt: reset, Enabled: armed, ResumeError: resumeError,
	})
	if err != nil {
		return fmt.Errorf("set turn auto-resume: %w", err)
	}
	if n == 0 {
		return domain.ErrNoConversationTurn
	}
	return nil
}

func (s *Store) PostponeTurnAutoResume(ctx context.Context, conversationID, turnID string, resetAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.PostponeConversationTurnAutoResume(ctx, gen.PostponeConversationTurnAutoResumeParams{
		ID: turnID, ConversationID: conversationID, ResetsAt: timeToNullTime(resetAt),
	})
	if err != nil {
		return false, fmt.Errorf("postpone turn auto-resume: %w", err)
	}
	return n > 0, nil
}

func (s *Store) AutoResumeTurns(ctx context.Context) ([]domain.ConversationTurn, error) {
	rows, err := s.qr.SelectAutoResumeConversationTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("read auto-resume turns: %w", err)
	}
	turns := make([]domain.ConversationTurn, 0, len(rows))
	for _, row := range rows {
		turns = append(turns, turnToDomain(row))
	}
	return turns, nil
}

func (s *Store) LatestAutoResumeTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectLatestAutoResumeConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("read latest auto-resume source: %w", err)
	}
	return turnToDomain(row), nil
}

func (s *Store) RollbackTurns(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	turn, err := s.qr.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, turnID)
	}
	if err != nil {
		return 0, fmt.Errorf("select turn %s: %w", turnID, err)
	}
	if turn.ConversationID != conversationID {
		return 0, fmt.Errorf("%w: %s is not in conversation %s",
			ErrConversationTurnNotFound, turnID, conversationID)
	}

	var discarded int64
	err = s.inTx(ctx, "roll back turns", func(q *gen.Queries) error {
		rows, markErr := q.MarkConversationTurnsRolledBack(ctx,
			gen.MarkConversationTurnsRolledBackParams{
				RolledBackAt:   sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID,
				AnchorTurnID:   turnID,
			})
		if markErr != nil {
			return fmt.Errorf("mark turns rolled back: %w", markErr)
		}
		discarded = rows

		if err := q.InterruptRolledBackQueuedTurns(ctx,
			gen.InterruptRolledBackQueuedTurnsParams{
				CompletedAt:    sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("interrupt rolled back queued turns: %w", err)
		}

		if err := q.AttachLegacyCompactionsToRollbackAnchor(ctx,
			gen.AttachLegacyCompactionsToRollbackAnchorParams{
				AnchorTurnID:         sql.NullString{String: turnID, Valid: true},
				UpdatedAt:            now,
				TargetConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("correlate legacy compactions with rollback: %w", err)
		}

		if err := q.RecomputeConversationCompactedAt(ctx,
			gen.RecomputeConversationCompactedAtParams{
				UpdatedAt:            now,
				TargetConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("recompute compacted conversation state: %w", err)
		}

		if err := q.FailRolledBackConversationApprovals(ctx,
			gen.FailRolledBackConversationApprovalsParams{
				UpdatedAt:        now,
				ConversationID:   conversationID,
				ConversationID_2: conversationID,
			}); err != nil {
			return fmt.Errorf("fail rolled back approvals: %w", err)
		}
		return reconcileSpanningPlans(ctx, q, conversationID, now)
	})
	if err != nil {
		return 0, err
	}
	return int(discarded), nil
}

func (s *Store) SetProviderTitle(
	ctx context.Context,
	conversationID, title string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationProviderTitle(ctx,
		gen.UpdateConversationProviderTitleParams{
			ProviderTitle: title,
			UpdatedAt:     now,
			ID:            conversationID,
		}); err != nil {
		return fmt.Errorf("set provider title for %s: %w", conversationID, err)
	}
	return nil
}

func (s *Store) ApplyProviderTitle(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	title string,
	now time.Time,
) (bool, error) {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	conversation, err := q.SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrConversationNotFound
	}
	if err != nil {
		return false, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	var applied bool
	err = s.inTx(ctx, "apply provider title", func(q *gen.Queries) error {
		rows, updateErr := q.ApplyConversationTitleToSession(ctx,
			gen.ApplyConversationTitleToSessionParams{
				DisplayName:   title,
				UpdatedAt:     now,
				ID:            session,
				DisplayName_2: conversation.AppliedTitle,
			})
		if updateErr != nil {
			return fmt.Errorf("apply title to session %s: %w", session, updateErr)
		}
		if rows == 0 {
			return nil
		}
		applied = true
		return q.UpdateConversationAppliedTitle(ctx, gen.UpdateConversationAppliedTitleParams{
			AppliedTitle: title,
			UpdatedAt:    now,
			ID:           conversationID,
		})
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

type ConversationSnapshot struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
	AfterSequence                    int64
	BatchedDeliveries                []domain.ConversationBatchedDelivery
}

const DefaultConversationPageSize = 200

const (
	conversationPageMaxActivities = 1000
	conversationPageMaxBytes      = 4 << 20
	conversationPageActivityChunk = 200
)

type conversationPageRow struct {
	sequence int64
	turnID   sql.NullString
	size     int
	message  bool
}

type conversationPageRows[T any] struct {
	rows     []T
	done     bool
	chunk    int64
	sequence func(T) int64
	read     func(before, limit int64) ([]T, error)
}

func (r *conversationPageRows[T]) at(index int, before, visibleAfter int64) (*T, error) {
	for index >= len(r.rows) && !r.done {
		cursor := before
		if n := len(r.rows); n > 0 {
			cursor = r.sequence(r.rows[n-1])
		}
		chunk, err := r.read(cursor, r.chunk)
		if err != nil {
			return nil, err
		}
		r.done = len(chunk) == 0
		for _, row := range chunk {
			if r.sequence(row) <= visibleAfter {
				r.done = true
				break
			}
			r.rows = append(r.rows, row)
		}
	}
	if index < len(r.rows) {
		return &r.rows[index], nil
	}
	return nil, nil
}

type conversationPageWalk struct {
	messages     *conversationPageRows[gen.ConversationMessage]
	activities   *conversationPageRows[gen.ConversationActivity]
	before       int64
	visibleAfter int64
	taken        []conversationPageRow
	messageCount int
	activityRows int
	bytes        int
}

func (w *conversationPageWalk) peek() (*conversationPageRow, error) {
	message, err := w.messages.at(w.messageCount, w.before, w.visibleAfter)
	if err != nil {
		return nil, err
	}
	activity, err := w.activities.at(w.activityRows, w.before, w.visibleAfter)
	if err != nil {
		return nil, err
	}
	switch {
	case message != nil && (activity == nil || message.Sequence > activity.Sequence):
		return &conversationPageRow{
			sequence: message.Sequence, turnID: message.TurnID, message: true,
			size: len(message.Text) + len(message.DeliveryContentJson),
		}, nil
	case activity != nil:
		return &conversationPageRow{
			sequence: activity.Sequence, turnID: activity.TurnID,
			size: len(activity.Summary) + len(activity.DetailJson) + len(activity.CommandOutput) + len(activity.StreamedText),
		}, nil
	}
	return nil, nil
}

func (w *conversationPageWalk) take(row conversationPageRow) {
	w.taken = append(w.taken, row)
	w.bytes += row.size
	if row.message {
		w.messageCount++
	} else {
		w.activityRows++
	}
}

func (w *conversationPageWalk) truncate(n int) {
	for _, row := range w.taken[n:] {
		w.bytes -= row.size
		if row.message {
			w.messageCount--
		} else {
			w.activityRows--
		}
	}
	w.taken = w.taken[:n]
}

func (w *conversationPageWalk) fits(row *conversationPageRow, pages int) bool {
	if !row.message && w.activityRows >= pages*conversationPageMaxActivities {
		return false
	}
	return len(w.taken) == 0 || w.bytes+row.size <= pages*conversationPageMaxBytes
}

func (w *conversationPageWalk) oldest() int64 {
	if len(w.taken) == 0 {
		return w.visibleAfter
	}
	return w.taken[len(w.taken)-1].sequence
}

func (s *Store) conversationPage(
	ctx context.Context,
	conversationID string,
	beforeSequence, visibleAfterSequence, limit int64,
) (messages []gen.ConversationMessage, activities []gen.ConversationActivity, oldest int64, hasMore bool, err error) {
	walk := &conversationPageWalk{
		before:       beforeSequence,
		visibleAfter: visibleAfterSequence,
		messages: &conversationPageRows[gen.ConversationMessage]{
			chunk:    limit + 1,
			sequence: func(row gen.ConversationMessage) int64 { return row.Sequence },
			read: func(before, n int64) ([]gen.ConversationMessage, error) {
				rows, err := s.qr.SelectConversationMessagesPage(ctx, gen.SelectConversationMessagesPageParams{
					ConversationID: conversationID, BeforeSequence: before, PageLimit: n, PageBytes: conversationPageMaxBytes,
				})
				if err != nil {
					return nil, fmt.Errorf("select message page: %w", err)
				}
				return rows, nil
			},
		},
		activities: &conversationPageRows[gen.ConversationActivity]{
			chunk:    conversationPageActivityChunk,
			sequence: func(row gen.ConversationActivity) int64 { return row.Sequence },
			read: func(before, n int64) ([]gen.ConversationActivity, error) {
				rows, err := s.qr.SelectConversationActivitiesPage(ctx, gen.SelectConversationActivitiesPageParams{
					ConversationID: conversationID, BeforeSequence: before, PageLimit: n, PageBytes: conversationPageMaxBytes,
				})
				if err != nil {
					return nil, fmt.Errorf("select activity page: %w", err)
				}
				return rows, nil
			},
		},
	}
	var next *conversationPageRow
	for {
		row, err := walk.peek()
		if err != nil {
			return nil, nil, 0, false, err
		}
		if row == nil {
			break
		}
		if (row.message && int64(walk.messageCount) == limit) || !walk.fits(row, 1) {
			next = row
			break
		}
		walk.take(*row)
	}
	if next != nil {
		if next.turnID.Valid {
			end := len(walk.taken)
			for end > 0 && walk.taken[end-1].turnID == next.turnID {
				end--
			}
			if end > 0 {
				walk.truncate(end)
			}
		}
		if err := s.extendPageToTurnPrompts(ctx, conversationID, walk, limit); err != nil {
			return nil, nil, 0, false, err
		}
		if next, err = walk.peek(); err != nil {
			return nil, nil, 0, false, err
		}
	}
	oldest = walk.oldest()
	return walk.messages.rows, walk.activities.rows, oldest, next != nil && oldest > visibleAfterSequence, nil
}

func (s *Store) extendPageToTurnPrompts(ctx context.Context, conversationID string, walk *conversationPageWalk, limit int64) error {
	kept := len(walk.taken)
	from := 0
	for {
		seen := map[string]bool{}
		var turns []sql.NullString
		for _, row := range walk.taken[from:] {
			if row.turnID.Valid && !seen[row.turnID.String] {
				seen[row.turnID.String] = true
				turns = append(turns, row.turnID)
			}
		}
		from = len(walk.taken)
		if len(turns) == 0 {
			return nil
		}
		target, err := s.qr.SelectConversationEarliestTurnPrompt(ctx, gen.SelectConversationEarliestTurnPromptParams{
			ConversationID: conversationID, TurnIds: turns,
		})
		if err != nil {
			return fmt.Errorf("select earliest turn prompt: %w", err)
		}
		if target <= walk.visibleAfter || target >= walk.oldest() {
			return nil
		}
		for {
			row, err := walk.peek()
			if err != nil {
				return err
			}
			if row == nil || row.sequence < target {
				break
			}
			if (row.message && int64(walk.messageCount) >= 2*limit) || !walk.fits(row, 2) {
				walk.truncate(kept)
				return nil
			}
			walk.take(*row)
		}
	}
}

func (s *Store) LoadConversationSnapshotPage(
	ctx context.Context,
	conversationID string,
	beforeSequence int64,
	limit int64,
) (ConversationSnapshot, error) {
	return s.loadConversationSnapshotRange(ctx, conversationID, beforeSequence, 0, limit, true)
}

func (s *Store) LoadConversationSnapshotRange(
	ctx context.Context,
	conversationID string,
	beforeSequence, afterSequence int64,
	limit int64,
) (ConversationSnapshot, error) {
	return s.loadConversationSnapshotRange(ctx, conversationID, beforeSequence, afterSequence, limit, true)
}

func (s *Store) loadConversationSnapshotRange(
	ctx context.Context,
	conversationID string,
	beforeSequence, afterSequence int64,
	limit int64,
	reconcile bool,
) (ConversationSnapshot, error) {
	conv, err := s.qr.SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationSnapshot{}, ErrConversationNotFound
	}
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	if account := conversationToDomain(conv).Account; reconcile && account != nil && account.ReauthRequiredAt != nil {
		reconciled, reconcileErr := s.ReconcileConversationAuthentication(ctx, conversationID, "", "", time.Now())
		if reconcileErr != nil {
			return ConversationSnapshot{}, reconcileErr
		}
		if reconciled != nil && reconciled.ReauthRequiredAt == nil {
			conv, err = s.qr.SelectConversationByID(ctx, conversationID)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
	}
	if limit <= 0 {
		limit = DefaultConversationPageSize
	}
	if limit > 500 {
		limit = 500
	}
	if beforeSequence <= 0 || beforeSequence > conv.LatestSequence+1 {
		beforeSequence = conv.LatestSequence + 1
	}
	visibleAfterSequence, err := s.conversationVisibleAfterSequence(ctx, conv)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	floor := visibleAfterSequence
	if afterSequence <= 0 || afterSequence < visibleAfterSequence {
		afterSequence = 0
	} else {
		floor = afterSequence
	}
	presentation := s.conversationHistoryPresentation(ctx, conversationID)
	if beforeSequence <= floor {
		snapshot := ConversationSnapshot{
			Conversation:                     conversationToDomain(conv),
			ActiveBranch:                     presentation.activeBranch,
			EditFloorSequence:                presentation.editFloorSequence,
			NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
			BranchPoints:                     presentation.branchPoints,
			BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
			OldestSequence:                   floor,
			HasMoreBefore:                    false,
			AfterSequence:                    afterSequence,
		}
		return snapshot, nil
	}
	messageRows, activityRows, oldest, hasMore, err := s.conversationPage(ctx, conversationID, beforeSequence, floor, limit)
	if err != nil {
		return ConversationSnapshot{}, err
	}

	turnRows, err := s.qr.SelectConversationTurnsPage(ctx, gen.SelectConversationTurnsPageParams{
		ConversationID: conversationID,
		OldestSequence: oldest,
		BeforeSequence: beforeSequence,
	})
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select turn page: %w", err)
	}
	retriedSources, err := s.conversationRetriedSources(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	humanPromptTurns, err := s.conversationHumanPromptTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}

	batchedDeliveries := s.conversationBatchedDeliveriesInRange(ctx, conversationID, max(oldest, floor+1), beforeSequence)

	snapshot := ConversationSnapshot{
		Conversation:                     conversationToDomain(conv),
		ActiveBranch:                     presentation.activeBranch,
		EditFloorSequence:                presentation.editFloorSequence,
		NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
		BranchPoints:                     presentation.branchPoints,
		BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
		OldestSequence:                   oldest,
		HasMoreBefore:                    hasMore,
		AfterSequence:                    afterSequence,
		BatchedDeliveries:                batchedDeliveries,
	}
	for _, row := range turnRows {
		if !conversationTurnVisibleAfterSequence(row, conv, visibleAfterSequence) {
			continue
		}
		turn := turnToDomain(row)
		turn.HasRetryAttempt = retriedSources[turn.ID]
		turn.HasHumanPrompt = humanPromptTurns[turn.ID]
		presentation.filterInactiveProviderTurn(&turn)
		snapshot.Turns = append(snapshot.Turns, turn)
	}
	for i := len(messageRows) - 1; i >= 0; i-- {
		if messageRows[i].Sequence >= oldest && messageRows[i].Sequence > floor {
			snapshot.Messages = append(snapshot.Messages, messageToDomain(messageRows[i]))
		}
	}
	for i := len(activityRows) - 1; i >= 0; i-- {
		if activityRows[i].Sequence >= oldest && activityRows[i].Sequence > floor {
			snapshot.Activities = append(snapshot.Activities, activityToDomain(activityRows[i]))
		}
	}
	return snapshot, nil
}

func (s *Store) conversationVisibleAfterSequence(
	ctx context.Context,
	conv gen.Conversation,
) (int64, error) {
	if conv.Scope == domain.ConversationScopeSession && conv.SessionID != nil {
		raw, err := s.qr.GetSessionImportSource(ctx, *conv.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("select session %s import source: %w", *conv.SessionID, err)
		}
		if source := unmarshalImportSource(raw); source != nil {
			return source.HistoryThrough, nil
		}
		return 0, nil
	}
	if conv.Scope != domain.ConversationScopeProject || conv.CurrentSessionID == nil || *conv.CurrentSessionID == "" {
		return 0, nil
	}
	sequence, err := s.qr.SelectConversationContextResetSequence(ctx, gen.SelectConversationContextResetSequenceParams{
		ConversationID: conv.ID,
		ProviderItemID: domain.ConversationContextResetProviderItemID(*conv.CurrentSessionID),
	})
	if err != nil {
		return 0, fmt.Errorf("select context reset boundary for conversation %s: %w", conv.ID, err)
	}
	if sequence == 0 {
		pending, pendingErr := s.projectConversationResetPending(ctx, conv)
		if pendingErr != nil {
			return 0, pendingErr
		}
		if pending {
			return conv.LatestSequence, nil
		}
	}
	return sequence, nil
}

func (s *Store) projectConversationResetPending(ctx context.Context, conv gen.Conversation) (bool, error) {
	if conv.Scope != domain.ConversationScopeProject ||
		conv.CurrentSessionID == nil ||
		*conv.CurrentSessionID == "" ||
		conv.LatestSequence <= 0 {
		return false, nil
	}
	current, err := s.qr.GetSession(ctx, *conv.CurrentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select current session %s: %w", *conv.CurrentSessionID, err)
	}
	if current.ProviderConversationID != "" {
		return false, nil
	}
	active, err := s.qr.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
		ConversationID: conv.ID,
		BranchID:       conv.ActiveBranchID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select active branch %s: %w", conv.ActiveBranchID, err)
	}
	return active.SessionID.Valid && active.SessionID.String != string(*conv.CurrentSessionID), nil
}

func conversationTurnVisibleAfterSequence(row gen.ConversationTurn, conv gen.Conversation, visibleAfterSequence int64) bool {
	if visibleAfterSequence <= 0 || conv.CurrentSessionID == nil {
		return true
	}
	return row.HandledBySessionID == *conv.CurrentSessionID
}

func (s *Store) LoadConversationSnapshot(
	ctx context.Context,
	conversationID string,
) (ConversationSnapshot, error) {
	conv, err := s.qr.SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationSnapshot{}, ErrConversationNotFound
	}
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	if account := conversationToDomain(conv).Account; account != nil && account.ReauthRequiredAt != nil {
		reconciled, reconcileErr := s.ReconcileConversationAuthentication(ctx, conversationID, "", "", time.Now())
		if reconcileErr != nil {
			return ConversationSnapshot{}, reconcileErr
		}
		if reconciled != nil && reconciled.ReauthRequiredAt == nil {
			conv, err = s.qr.SelectConversationByID(ctx, conversationID)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
	}

	turnRows, err := s.qr.SelectConversationTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select turns: %w", err)
	}
	retriedSources, err := s.conversationRetriedSources(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	humanPromptTurns, err := s.conversationHumanPromptTurns(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	messageRows, err := s.qr.SelectConversationMessages(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select messages: %w", err)
	}
	activityRows, err := s.qr.SelectConversationActivities(ctx, conversationID)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("select activities: %w", err)
	}

	batchedDeliveries := s.conversationBatchedDeliveries(ctx, conversationID)

	presentation := s.conversationHistoryPresentation(ctx, conversationID)
	snapshot := ConversationSnapshot{
		Conversation:                     conversationToDomain(conv),
		ActiveBranch:                     presentation.activeBranch,
		EditFloorSequence:                presentation.editFloorSequence,
		NativeForkAvailableAfterSequence: presentation.nativeForkAvailableAfterSequence,
		BranchPoints:                     presentation.branchPoints,
		BranchedFromEarlierMessage:       presentation.branchedFromEarlierMessage,
		BatchedDeliveries:                batchedDeliveries,
	}
	for _, row := range turnRows {
		turn := turnToDomain(row)
		turn.HasRetryAttempt = retriedSources[turn.ID]
		turn.HasHumanPrompt = humanPromptTurns[turn.ID]
		presentation.filterInactiveProviderTurn(&turn)
		snapshot.Turns = append(snapshot.Turns, turn)
	}
	for _, row := range messageRows {
		snapshot.Messages = append(snapshot.Messages, messageToDomain(row))
	}
	for _, row := range activityRows {
		snapshot.Activities = append(snapshot.Activities, activityToDomain(row))
	}
	return snapshot, nil
}

func (s *Store) conversationRetriedSources(ctx context.Context, conversationID string) (map[string]bool, error) {
	rows, err := s.qr.SelectConversationRetriedSourceTurnIDs(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select retried conversation sources: %w", err)
	}
	sources := make(map[string]bool, len(rows))
	for _, sourceTurnID := range rows {
		sources[sourceTurnID] = true
	}
	return sources, nil
}

func (s *Store) conversationHumanPromptTurns(ctx context.Context, conversationID string) (map[string]bool, error) {
	rows, err := s.qr.SelectConversationHumanPromptTurnIDs(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select conversation human prompts: %w", err)
	}
	turns := make(map[string]bool, len(rows))
	for _, turnID := range rows {
		turns[turnID] = true
	}
	return turns, nil
}

type conversationHistoryPresentation struct {
	activeBranch                     domain.ConversationBranch
	activeProviderScopeID            string
	activeProviderBindingID          string
	editFloorSequence                int64
	nativeForkAvailableAfterSequence int64
	branchProviderScopes             map[string]string
	branchPoints                     []domain.ConversationBranchPoint
	branchedFromEarlierMessage       bool
}

func (p conversationHistoryPresentation) filterInactiveProviderTurn(turn *domain.ConversationTurn) {
	if p.activeProviderScopeID == "" || turn.BranchID == "" {
		return
	}
	providerScopeID, known := p.branchProviderScopes[turn.BranchID]
	if known && providerScopeID != p.activeProviderScopeID {
		turn.ProviderTurnID = ""
	}
}

func (s *Store) conversationHistoryPresentation(
	ctx context.Context,
	conversationID string,
) conversationHistoryPresentation {
	branches, err := s.ConversationBranches(ctx, conversationID)
	if err != nil {
		return conversationHistoryPresentation{}
	}
	presentation := conversationHistoryPresentation{
		branchProviderScopes: make(map[string]string, len(branches)),
	}
	for _, branch := range branches {
		presentation.branchProviderScopes[branch.ID] = branch.ProviderScopeID
		if branch.Active {
			presentation.activeBranch = branch
			presentation.activeProviderScopeID = branch.ProviderScopeID
			presentation.activeProviderBindingID = branch.ProviderBindingID
			presentation.branchedFromEarlierMessage =
				branch.ParentBranchID != "" && branch.ReplacedTurnID != ""
		}
	}
	if sequence, queryErr := s.qr.SelectConversationNativeForkAvailableAfterSequence(ctx, conversationID); queryErr == nil {
		presentation.nativeForkAvailableAfterSequence = sequence
	}
	for _, branch := range branches {
		if branch.ID == presentation.activeProviderBindingID {
			presentation.editFloorSequence = branch.ForkAfterSequence
			break
		}
	}
	presentation.branchPoints = conversationBranchPointsForProviderBinding(
		branches, presentation.activeProviderBindingID)
	return presentation
}

func conversationBranchPointsForProviderBinding(
	branches []domain.ConversationBranch,
	activeProviderBindingID string,
) []domain.ConversationBranchPoint {
	type groupKey struct {
		sourceBranchID string
		replacedTurnID string
	}
	type member struct {
		branchID string
		turnID   string
	}
	groups := make(map[groupKey][]member)
	groupByReplacement := make(map[string]groupKey)
	order := make([]groupKey, 0)
	for _, branch := range branches {
		if branch.ProviderBindingID != activeProviderBindingID ||
			branch.ParentBranchID == "" || branch.ReplacedTurnID == "" || branch.ReplacementTurnID == "" {
			continue
		}
		key := groupKey{sourceBranchID: branch.ParentBranchID, replacedTurnID: branch.ReplacedTurnID}
		if existing, ok := groupByReplacement[branch.ReplacedTurnID]; ok {
			key = existing
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
			groups[key] = []member{{branchID: key.sourceBranchID, turnID: key.replacedTurnID}}
		}
		groups[key] = append(groups[key], member{branchID: branch.ID, turnID: branch.ReplacementTurnID})
		groupByReplacement[branch.ReplacementTurnID] = key
	}

	points := make([]domain.ConversationBranchPoint, 0, len(branches))
	for _, key := range order {
		members := groups[key]
		for index, current := range members {
			point := domain.ConversationBranchPoint{
				TurnID: current.turnID, Position: index + 1, Total: len(members),
			}
			if index > 0 {
				point.PreviousBranchID = members[index-1].branchID
			}
			if index+1 < len(members) {
				point.NextBranchID = members[index+1].branchID
			}
			points = append(points, point)
		}
	}
	return points
}

func (s *Store) turnIDFor(ctx context.Context, conversationID, providerTurnID string) sql.NullString {
	if providerTurnID == "" {
		return sql.NullString{}
	}
	row, err := s.conversationReader(ctx).SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: row.ID, Valid: true}
}

func conversationToDomain(row gen.Conversation) domain.ConversationRecord {
	rec := domain.ConversationRecord{
		ID:             row.ID,
		Scope:          row.Scope,
		ProjectID:      projectIDValue(row.ProjectID),
		ActiveBranchID: row.ActiveBranchID,
		LatestSequence: row.LatestSequence,
		Settings: domain.ConversationSettings{
			Model:           row.Model.String,
			ReasoningEffort: row.ReasoningEffort.String,
			ApprovalMode:    domain.PermissionMode(row.ApprovalMode.String),
			OpenCodeMode:    row.OpencodeMode,

			AutoCompactWindow: row.AutoCompactWindow,
		},
		ProviderTitle: row.ProviderTitle,
		AppliedTitle:  row.AppliedTitle,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.CurrentSessionID != nil {
		rec.SessionID = *row.CurrentSessionID
	}
	rec.Usage = usageFromRow(row)
	rec.RateLimits = rateLimitsFromRow(row)
	if row.CompactedAt.Valid {
		compacted := row.CompactedAt.Time
		rec.CompactedAt = &compacted
	}
	rec.ModelReroute = decodeJSONColumn[domain.ConversationModelReroute](row.ModelRerouteJson)
	rec.Account = decodeJSONColumn[domain.ConversationAccount](row.AccountJson)
	if rec.Account != nil && rec.Account.AuthenticationState == "" {
		rec.Account.AuthenticationState = "unknown"
		if rec.Account.ReauthRequiredAt != nil {
			rec.Account.AuthenticationState = "required"
		}
	}
	rec.ThreadState = decodeJSONColumn[domain.ConversationThreadState](row.ThreadStateJson)
	if servers := decodeJSONColumn[[]domain.ConversationMCPServer](row.McpServersJson); servers != nil {
		rec.MCPServers = *servers
	}
	return rec
}

func conversationBranchToDomain(row gen.SelectConversationBranchRow) domain.ConversationBranch {
	return domain.ConversationBranch{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		SessionID:              domain.SessionID(row.SessionID.String),
		ProviderConversationID: row.ProviderConversationID,
		ParentBranchID:         row.ParentBranchID.String,
		ForkAfterTurnID:        row.ForkAfterTurnID.String,
		ReplacedTurnID:         row.ReplacedTurnID.String,
		ReplacementTurnID:      row.ReplacementTurnID.String,
		ForkAfterSequence:      row.ForkAfterSequence,
		Strategy:               domain.NormalizeConversationBranchStrategy(domain.ConversationBranchStrategy(row.Strategy)),
		ReplayCutoffSequence:   row.ReplayCutoffSequence,
		ReplayTruncated:        row.ReplayTruncated != 0,
		ProviderBindingID:      row.ProviderBindingID,
		ProviderScopeID:        row.EffectiveProviderScopeID,
		ProviderIDsScoped:      row.ProviderIdsScoped != 0,
		Active:                 row.Active,
		CreatedAt:              row.CreatedAt,
		HandoffNote:            row.HandoffNote,
	}
}

func conversationBranchListToDomain(row gen.SelectConversationBranchesRow) domain.ConversationBranch {
	return domain.ConversationBranch{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		SessionID:              domain.SessionID(row.SessionID.String),
		ProviderConversationID: row.ProviderConversationID,
		ParentBranchID:         row.ParentBranchID.String,
		ForkAfterTurnID:        row.ForkAfterTurnID.String,
		ReplacedTurnID:         row.ReplacedTurnID.String,
		ReplacementTurnID:      row.ReplacementTurnID.String,
		ForkAfterSequence:      row.ForkAfterSequence,
		Strategy:               domain.NormalizeConversationBranchStrategy(domain.ConversationBranchStrategy(row.Strategy)),
		ReplayCutoffSequence:   row.ReplayCutoffSequence,
		ReplayTruncated:        row.ReplayTruncated != 0,
		ProviderBindingID:      row.ProviderBindingID,
		ProviderScopeID:        row.EffectiveProviderScopeID,
		ProviderIDsScoped:      row.ProviderIdsScoped != 0,
		Active:                 row.Active,
		CreatedAt:              row.CreatedAt,
		HandoffNote:            row.HandoffNote,
	}
}

func decodeJSONColumn[T any](column sql.NullString) *T {
	if !column.Valid || column.String == "" {
		return nil
	}
	var value T
	if err := json.Unmarshal([]byte(column.String), &value); err != nil {
		return nil
	}
	return &value
}

func usageFromRow(row gen.Conversation) *domain.ConversationUsage {
	if !row.ContextUsed.Valid && !row.UsageTotalTokens.Valid && !row.UsageCost.Valid {
		return nil
	}
	usage := &domain.ConversationUsage{
		ContextUsed:   row.ContextUsed.Int64,
		ContextWindow: row.ContextWindow.Int64,
		InputTokens:   row.UsageInputTokens.Int64,
		OutputTokens:  row.UsageOutputTokens.Int64,
		CachedTokens:  row.UsageCachedTokens.Int64,
		TotalTokens:   row.UsageTotalTokens.Int64,
		Currency:      row.UsageCurrency.String,
	}
	if row.UsageCost.Valid {
		cost := row.UsageCost.Float64
		usage.Cost = &cost
	}
	return usage
}

func nullableCost(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func rateLimitsFromRow(row gen.Conversation) *domain.ConversationRateLimits {
	if !row.RateLimitPrimaryPercent.Valid && !row.RateLimitSecondaryPercent.Valid {
		return nil
	}
	limits := &domain.ConversationRateLimits{
		PrimaryUsedPercent:       -1,
		SecondaryUsedPercent:     -1,
		PrimaryResetsInSeconds:   row.RateLimitPrimaryResetsIn.Int64,
		SecondaryResetsInSeconds: row.RateLimitSecondaryResetsIn.Int64,
		PlanLabel:                row.RateLimitPlan.String,
	}
	if row.RateLimitPrimaryPercent.Valid {
		limits.PrimaryUsedPercent = row.RateLimitPrimaryPercent.Float64
	}
	if row.RateLimitSecondaryPercent.Valid {
		limits.SecondaryUsedPercent = row.RateLimitSecondaryPercent.Float64
	}
	return limits
}

func turnToDomain(row gen.ConversationTurn) domain.ConversationTurn {
	turn := domain.ConversationTurn{
		ID:                 row.ID,
		ConversationID:     row.ConversationID,
		BranchID:           row.BranchID,
		HandledBySessionID: row.HandledBySessionID,
		ProviderTurnID:     row.ProviderTurnID,
		ProviderInputText:  row.ProviderInputText,
		State:              row.State,
		ErrorMessage:       row.ErrorMessage,
		FailureCode:        domain.ConversationFailureCode(row.FailureCode),
		RequestedAt:        row.RequestedAt,
	}
	if row.RetryOfTurnID.Valid {
		turn.RetryOfTurnID = row.RetryOfTurnID.String
	}
	if row.StartedAt.Valid {
		started := row.StartedAt.Time
		turn.StartedAt = &started
	}
	if row.CompletedAt.Valid {
		completed := row.CompletedAt.Time
		turn.CompletedAt = &completed
	}
	if turn.State == domain.TurnStateFailed {
		turn.UsageLimit = domain.ParseConversationUsageLimit(turn.ErrorMessage, turn.CompletedAt)
		if turn.UsageLimit != nil {
			if row.UsageLimitResetsAt.Valid {
				reset := row.UsageLimitResetsAt.Time
				turn.UsageLimit.ResetsAt = &reset
			}
			turn.UsageLimit.AutoResume = row.UsageLimitAutoResume != 0
			turn.UsageLimit.ResumeError = row.UsageLimitResumeError
		}
	}
	if row.DiffJson != "" {
		var diff domain.ConversationTurnDiff
		if err := json.Unmarshal([]byte(row.DiffJson), &diff); err == nil {
			turn.Diff = &diff
		}
	}
	if row.RolledBackAt.Valid {
		rolledBack := row.RolledBackAt.Time
		turn.RolledBackAt = &rolledBack
	}
	if row.ArtifactsJson != "" {
		var artifacts domain.ConversationTurnArtifacts
		if err := json.Unmarshal([]byte(row.ArtifactsJson), &artifacts); err == nil && len(artifacts.Files) > 0 {
			turn.Artifacts = &artifacts
		}
	}
	if row.PlanJson != "" {
		var plan domain.ConversationPlan
		if err := json.Unmarshal([]byte(row.PlanJson), &plan); err == nil {
			turn.Plan = &plan
		}
	}
	return turn
}

func messageToDomain(row gen.ConversationMessage) domain.ConversationMessage {
	msg := domain.ConversationMessage{
		ID:                  row.ID,
		ConversationID:      row.ConversationID,
		Sequence:            row.Sequence,
		Revision:            row.Revision,
		Role:                row.Role,
		Origin:              row.Origin,
		Text:                row.Text,
		Streaming:           row.Streaming != 0,
		ProviderItemID:      row.ProviderItemID,
		ClientMessageID:     row.ClientMessageID,
		ClientPayloadHash:   row.ClientPayloadHash.String,
		DeliveryContentJSON: row.DeliveryContentJson,
		Continuation:        row.Continuation != 0,
		AuthoredByUser:      row.AuthoredByUser != 0,
		SenderSessionID:     row.SenderSessionID,
		SenderProjectID:     row.SenderProjectID,
		SenderDisplayName:   row.SenderDisplayName,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
	if row.TurnID.Valid {
		msg.TurnID = row.TurnID.String
	}
	return msg
}

func activityToDomain(row gen.ConversationActivity) domain.ConversationActivity {
	activity := domain.ConversationActivity{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		Sequence:               row.Sequence,
		Revision:               row.Revision,
		Kind:                   row.Kind,
		Status:                 row.Status,
		Summary:                row.Summary,
		RequestID:              row.RequestID,
		ProviderItemID:         row.ProviderItemID,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
		CommandOutput:          row.CommandOutput,
		CommandOutputTruncated: row.CommandOutputTruncated != 0,
		StreamedText:           row.StreamedText,
		StreamedTextTruncated:  row.StreamedTextTruncated != 0,
	}
	if row.TurnID.Valid {
		activity.TurnID = row.TurnID.String
	}
	if row.DetailJson != "" && json.Valid([]byte(row.DetailJson)) {
		activity.Detail = []byte(row.DetailJson)
	}
	return activity
}

func (s *Store) ProviderEventsSince(
	ctx context.Context,
	conversationID string,
	afterID int64,
	limit int64,
) ([]gen.ConversationProviderEvent, error) {
	rows, err := s.qr.SelectConversationProviderEvents(ctx,
		gen.SelectConversationProviderEventsParams{
			ConversationID: conversationID,
			ID:             afterID,
			PageLimit:      limit,
		})
	if err != nil {
		return nil, fmt.Errorf("select provider events for %s: %w", conversationID, err)
	}
	return rows, nil
}

type steerRetryInput struct {
	Text                       string              `json:"text"`
	Content                    []ports.ChatContent `json:"content"`
	ProviderInputText          string              `json:"providerInputText"`
	ProviderInputContent       []ports.ChatContent `json:"providerInputContent"`
	ProviderInputCaptured      bool                `json:"providerInputCaptured"`
	ProviderInputStoredOnTurn  bool                `json:"providerInputStoredOnTurn"`
	ProviderInputSourceTurnIDs []string            `json:"providerInputSourceTurnIds"`
}

type retryProviderInputItem struct {
	Text         string `json:"text"`
	ContentStart int    `json:"contentStart,omitempty"`
	ContentCount int    `json:"contentCount,omitempty"`
}

func (s *Store) RetryPrompt(ctx context.Context, conversationID, turnID string) (domain.RetryPrompt, error) {
	row, err := s.qr.SelectRetryableConversationPrompt(ctx, gen.SelectRetryableConversationPromptParams{
		ID: turnID, ConversationID: conversationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RetryPrompt{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read retry prompt %s: %w", turnID, err)
	}
	prompt := domain.RetryPrompt{
		Continuation:        row.Continuation != 0,
		Text:                row.Text,
		ProviderInputText:   row.ProviderInputText,
		Origin:              row.Origin,
		DeliveryContentJSON: row.DeliveryContentJson,
		ActiveLineage:       row.ActiveLineage,
	}
	activityDetails, err := s.qr.SelectConversationSteerRetryInputs(ctx, gen.SelectConversationSteerRetryInputsParams{
		ConversationID: conversationID, TurnID: sql.NullString{String: turnID, Valid: true},
	})
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read steer inputs %s: %w", turnID, err)
	}
	if prompt.ProviderInputText == "" && len(activityDetails) == 0 {
		return prompt, nil
	}
	steers := make([]steerRetryInput, 0, len(activityDetails))
	steeredSourceIDs := make(map[string]bool)
	initialText := prompt.Text
	initialWasRendered := prompt.ProviderInputText != ""
	if initialWasRendered {
		initialText = prompt.ProviderInputText
	}
	for _, detail := range activityDetails {
		var steer steerRetryInput
		if err := json.Unmarshal([]byte(detail), &steer); err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("decode steer input %s: %w", turnID, err)
		}
		if steer.ProviderInputStoredOnTurn && steer.ProviderInputText == prompt.ProviderInputText {
			initialText = prompt.Text
			initialWasRendered = false
		}
		if steer.ProviderInputCaptured {
			for _, id := range steer.ProviderInputSourceTurnIDs {
				steeredSourceIDs[id] = true
			}
		}
		steers = append(steers, steer)
	}
	blocks, err := s.qr.SelectConversationProviderInputContent(ctx, gen.SelectConversationProviderInputContentParams{
		ConversationID: conversationID, TurnID: turnID,
	})
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read provider input content %s: %w", turnID, err)
	}
	var content []ports.ChatContent
	for _, block := range blocks {
		if steeredSourceIDs[block.SourceTurnID] || block.DeliveryContentJson == "" {
			continue
		}
		var items []ports.ChatContent
		if err := json.Unmarshal([]byte(block.DeliveryContentJson), &items); err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("decode provider input content %s: %w", turnID, err)
		}
		for _, item := range items {
			if initialWasRendered && ((item.Type == "excerpt" && item.Excerpt != nil) || (item.Type == "text" && item.Internal)) {
				continue
			}
			content = append(content, item)
		}
	}
	if len(steers) > 0 {
		inputs := []retryProviderInputItem{{Text: initialText, ContentCount: len(content)}}
		for _, steer := range steers {
			text := steer.Text
			blocks := steer.Content
			if steer.ProviderInputText != "" {
				text = steer.ProviderInputText
			}
			if steer.ProviderInputCaptured {
				blocks = steer.ProviderInputContent
			}
			inputs = append(inputs, retryProviderInputItem{
				Text: text, ContentStart: len(content), ContentCount: len(blocks),
			})
			content = append(content, blocks...)
		}
		encoded, err := json.Marshal(inputs)
		if err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("encode provider inputs %s: %w", turnID, err)
		}
		prompt.ProviderInputText = "Orchestrator prior provider inputs in order. Each JSON item is one complete input; content blocks follow contentStart and contentCount indices.\n" + string(encoded)
	}
	prompt.DeliveryContentJSON = ""
	if len(content) > 0 {
		encoded, err := json.Marshal(content)
		if err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("encode provider input content %s: %w", turnID, err)
		}
		prompt.DeliveryContentJSON = string(encoded)
	}
	return prompt, nil
}

func (s *Store) RetryTurnIDForSource(ctx context.Context, conversationID, sourceTurnID string) (string, bool, error) {
	row, err := s.qr.SelectConversationRetryTurnIDBySource(ctx, gen.SelectConversationRetryTurnIDBySourceParams{
		ConversationID: conversationID,
		RetryOfTurnID:  sql.NullString{String: sourceTurnID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row, true, nil
}

var ErrQueuedEditDeliveryConflict = errors.New("queued edit delivery key reused")

func (s *Store) QueuedEditDelivery(ctx context.Context, conversationID, clientMessageID string) (string, bool, error) {
	hash, err := s.conversationReader(ctx).SelectConversationQueuedEditDelivery(ctx,
		gen.SelectConversationQueuedEditDeliveryParams{ConversationID: conversationID, ClientMessageID: clientMessageID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func reserveConversationDelivery[Row, Delivery any](
	ctx context.Context,
	s *Store,
	insert func(context.Context) (int64, error),
	load func(context.Context) (Row, error),
	toDomain func(Row) Delivery,
	insertError, loadError string,
) (delivery Delivery, created bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := insert(ctx)
	if err != nil {
		return delivery, false, fmt.Errorf("%s: %w", insertError, err)
	}
	row, err := load(ctx)
	if err != nil {
		return delivery, false, fmt.Errorf("%s: %w", loadError, err)
	}
	return toDomain(row), rows == 1, nil
}

func (s *Store) EditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationEditDelivery, bool, error) {
	row, err := s.conversationReader(ctx).SelectConversationEditDelivery(ctx,
		gen.SelectConversationEditDeliveryParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationEditDelivery{}, false, nil
	}
	if err != nil {
		return domain.ConversationEditDelivery{}, false,
			fmt.Errorf("select edit delivery %s: %w", clientMessageID, err)
	}
	return editDeliveryToDomain(row), true, nil
}

func (s *Store) ReserveEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID, requestJSON string,
	now time.Time,
) (delivery domain.ConversationEditDelivery, created bool, err error) {
	return reserveConversationDelivery(
		ctx,
		s,
		func(ctx context.Context) (int64, error) {
			return s.qw.InsertConversationEditDeliveryReservation(ctx,
				gen.InsertConversationEditDeliveryReservationParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
					RequestJson: requestJSON, CreatedAt: now,
				})
		},
		func(ctx context.Context) (gen.ConversationEditDelivery, error) {
			return s.qw.SelectConversationEditDelivery(ctx,
				gen.SelectConversationEditDeliveryParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
				})
		},
		editDeliveryToDomain,
		"reserve edit delivery "+clientMessageID,
		"read edit delivery reservation "+clientMessageID,
	)
}

func (s *Store) RecoverCompletedEditDelivery(ctx context.Context, conversationID, clientMessageID string, now time.Time) error {
	row, err := s.conversationReader(ctx).SelectCompletedEditReplacement(ctx, gen.SelectCompletedEditReplacementParams{
		ConversationID: conversationID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load completed edit replacement: %w", err)
	}
	turn := turnToDomain(row.ConversationTurn)
	return s.CompleteEditDelivery(ctx, conversationID, clientMessageID, row.ParentBranchID.String, turn.BranchID, turn, now)
}

func (s *Store) BeginEditProviderWork(ctx context.Context, conversationID, clientMessageID, generation string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.BeginConversationEditProviderWork(ctx, gen.BeginConversationEditProviderWorkParams{
		ConversationID: conversationID, ClientMessageID: clientMessageID, Generation: generation,
	})
	if err != nil {
		return fmt.Errorf("begin edit provider work: %w", err)
	}
	if rows != 1 {
		return errors.New("edit provider work already started or controller replaced")
	}
	return nil
}

func (s *Store) CompleteEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID, sourceBranchID, activeBranchID string,
	turn domain.ConversationTurn,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete edit delivery", func(q *gen.Queries) error {
		rows, err := q.UpdateConversationBranchReplacement(ctx,
			gen.UpdateConversationBranchReplacementParams{
				ReplacementTurnID: nullableString(turn.ID), BranchID: activeBranchID,
			})
		if err != nil {
			return fmt.Errorf("update conversation branch %s replacement: %w", activeBranchID, err)
		}
		if rows != 1 {
			return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, activeBranchID)
		}
		rows, err = q.AcceptConversationEditDelivery(ctx,
			gen.AcceptConversationEditDeliveryParams{
				SourceBranchID: sourceBranchID, ActiveBranchID: activeBranchID,
				TurnID: turn.ID, HandledBySessionID: string(turn.HandledBySessionID),
				ProviderTurnID: turn.ProviderTurnID, TurnState: string(turn.State),
				TurnRequestedAt: sql.NullTime{Time: turn.RequestedAt, Valid: !turn.RequestedAt.IsZero()},
				SettledAt:       sql.NullTime{Time: now, Valid: true},
				ConversationID:  conversationID, ClientMessageID: clientMessageID,
			})
		if err != nil {
			return fmt.Errorf("accept edit delivery %s: %w", clientMessageID, err)
		}
		if rows != 1 {
			return fmt.Errorf("accept edit delivery %s: reservation is not active", clientMessageID)
		}
		return nil
	})
}

func (s *Store) RejectEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	kind domain.ConversationEditRejectionKind,
	message string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RejectConversationEditDelivery(ctx,
		gen.RejectConversationEditDeliveryParams{
			RejectionKind: string(kind), RejectionMessage: message,
			SettledAt:      sql.NullTime{Time: now, Valid: true},
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if err != nil {
		return fmt.Errorf("reject edit delivery %s: %w", clientMessageID, err)
	}
	if rows != 1 {
		return fmt.Errorf("reject edit delivery %s: reservation is not active", clientMessageID)
	}
	return nil
}

func editDeliveryToDomain(row gen.ConversationEditDelivery) domain.ConversationEditDelivery {
	delivery := domain.ConversationEditDelivery{
		ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		RequestJSON: row.RequestJson, State: domain.ConversationEditDeliveryState(row.State),
		ProviderWorkStarted: row.ProviderWorkStarted != 0,
		SourceBranchID:      row.SourceBranchID, ActiveBranchID: row.ActiveBranchID,
		Turn: domain.ConversationTurn{
			ID: row.TurnID, ConversationID: row.ConversationID,
			HandledBySessionID: domain.SessionID(row.HandledBySessionID),
			ProviderTurnID:     row.ProviderTurnID, State: domain.TurnState(row.TurnState),
		},
		RejectionKind:    domain.ConversationEditRejectionKind(row.RejectionKind),
		RejectionMessage: row.RejectionMessage, CreatedAt: row.CreatedAt,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		delivery.SettledAt = &settled
	}
	if row.TurnRequestedAt.Valid {
		delivery.Turn.RequestedAt = row.TurnRequestedAt.Time
	}
	return delivery
}

func (s *Store) SteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationSteerDelivery, bool, error) {
	row, err := s.conversationReader(ctx).SelectConversationSteerDelivery(ctx,
		gen.SelectConversationSteerDeliveryParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationSteerDelivery{}, false, nil
	}
	if err != nil {
		return domain.ConversationSteerDelivery{}, false,
			fmt.Errorf("select steer delivery %s: %w", clientMessageID, err)
	}
	return steerDeliveryToDomain(row), true, nil
}

func (s *Store) ReserveSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID, requestJSON string,
	now time.Time,
) (delivery domain.ConversationSteerDelivery, created bool, err error) {
	return reserveConversationDelivery(
		ctx,
		s,
		func(ctx context.Context) (int64, error) {
			return s.qw.InsertConversationSteerDeliveryReservation(ctx,
				gen.InsertConversationSteerDeliveryReservationParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
					RequestJson: requestJSON, CreatedAt: now,
				})
		},
		func(ctx context.Context) (gen.ConversationSteerDelivery, error) {
			return s.qw.SelectConversationSteerDelivery(ctx,
				gen.SelectConversationSteerDeliveryParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
				})
		},
		steerDeliveryToDomain,
		"reserve steer delivery "+clientMessageID,
		"load reserved steer delivery "+clientMessageID,
	)
}

func (s *Store) CompleteSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	return s.CompleteSteerBatchDelivery(ctx, conversationID, clientMessageID, providerTurnID, activity, nil, now)
}

func (s *Store) CompleteSteerBatchDelivery(
	ctx context.Context,
	conversationID, clientMessageID, providerTurnID string,
	activity domain.ConversationActivity,
	turnIDs []string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete steer delivery", func(q *gen.Queries) error {
		if len(turnIDs) > 0 {
			target, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID, ProviderTurnID: providerTurnID,
			})
			if err != nil {
				return err
			}
			for _, id := range turnIDs {
				changed, err := q.CompleteQueuedConversationBatchMember(ctx, gen.CompleteQueuedConversationBatchMemberParams{
					CompletedAt:          sql.NullTime{Time: now, Valid: true},
					PromotedToTurnID:     sql.NullString{String: target.ID, Valid: true},
					BatchDeliveryKind:    batchDeliveryKind(activity.ID),
					BatchDeliverySteerID: activity.ID,
					ID:                   id, ConversationID: conversationID,
				})
				if checkErr := expectBatchRow("complete steered batch turn", id, changed, err); checkErr != nil {
					return checkErr
				}
			}
		}
		txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
		if err := s.UpsertActivity(txCtx, conversationID, providerTurnID, activity, now); err != nil {
			return err
		}
		rows, err := q.AcceptConversationSteerDelivery(ctx,
			gen.AcceptConversationSteerDeliveryParams{
				ProviderTurnID: providerTurnID, ActivityID: activity.ID,
				SettledAt:      sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID, ClientMessageID: clientMessageID,
			})
		if err != nil {
			return fmt.Errorf("accept steer delivery %s: %w", clientMessageID, err)
		}
		if rows != 1 {
			return fmt.Errorf("accept steer delivery %s: reservation is not active", clientMessageID)
		}
		return nil
	})
}

func (s *Store) RejectSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	kind domain.ConversationSteerRejectionKind,
	message string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RejectConversationSteerDelivery(ctx,
		gen.RejectConversationSteerDeliveryParams{
			RejectionKind: string(kind), RejectionMessage: message,
			SettledAt:      sql.NullTime{Time: now, Valid: true},
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if err != nil {
		return fmt.Errorf("reject steer delivery %s: %w", clientMessageID, err)
	}
	if rows != 1 {
		return fmt.Errorf("reject steer delivery %s: reservation is not active", clientMessageID)
	}
	return nil
}

func steerDeliveryToDomain(row gen.ConversationSteerDelivery) domain.ConversationSteerDelivery {
	delivery := domain.ConversationSteerDelivery{
		ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		RequestJSON: row.RequestJson, State: domain.ConversationSteerDeliveryState(row.State),
		ProviderTurnID: row.ProviderTurnID, ActivityID: row.ActivityID,
		RejectionKind:    domain.ConversationSteerRejectionKind(row.RejectionKind),
		RejectionMessage: row.RejectionMessage, CreatedAt: row.CreatedAt,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		delivery.SettledAt = &settled
	}
	return delivery
}

func (s *Store) SettleReviewChatWork(ctx context.Context, reviewID string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "settle reviewer Chat epoch", func(q *gen.Queries) error {
		review, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return err
		}
		conversation, err := q.SelectConversationByReview(ctx, nullableString(reviewID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if _, err := cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversation.ID, review.ControllerGeneration, now); err != nil {
				return err
			}
		}
		_, err = q.ClearReviewChatController(ctx, gen.ClearReviewChatControllerParams{ID: reviewID, UpdatedAt: now})
		return err
	})
}

func (s *Store) RebindUnansweredBranchProvider(ctx context.Context, conversationID, branchID, previous, provider string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `
UPDATE conversation_branches SET provider_conversation_id = ?
WHERE id = ? AND conversation_id = ? AND provider_conversation_id = ?
  AND NOT EXISTS (SELECT 1 FROM conversation_turns WHERE conversation_id = ? AND branch_id = ?)`,
		provider, branchID, conversationID, previous, conversationID, branchID)
	if err != nil {
		return fmt.Errorf("rebind branch %s provider: %w", branchID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rebind branch %s provider: %w", branchID, err)
	}
	if rows != 1 {
		return fmt.Errorf("rebind branch %s provider: %d rows", branchID, rows)
	}
	return nil
}

func batchDeliveryKind(steerActivityID string) string {
	if steerActivityID != "" {
		return "steer"
	}
	return "drain"
}

func (s *Store) conversationBatchedDeliveries(ctx context.Context, conversationID string) []domain.ConversationBatchedDelivery {
	turns, err := s.qr.ListConversationBatchedTurns(ctx, conversationID)
	if err != nil {
		slog.Warn("list batched turns failed", "conversation", conversationID, "error", err)
		return nil
	}
	out := make([]domain.ConversationBatchedDelivery, 0, len(turns))
	for _, turn := range turns {
		out = append(out, batchedDelivery(gen.ListConversationBatchedTurnsInRangeRow(turn)))
	}
	return out
}

func (s *Store) conversationBatchedDeliveriesInRange(ctx context.Context, conversationID string, from, before int64) []domain.ConversationBatchedDelivery {
	turns, err := s.qr.ListConversationBatchedTurnsInRange(ctx, gen.ListConversationBatchedTurnsInRangeParams{
		ConversationID: conversationID, FromSequence: from, BeforeSequence: before,
	})
	if err != nil {
		slog.Warn("list batched turns in range failed", "conversation", conversationID, "error", err)
		return nil
	}
	out := make([]domain.ConversationBatchedDelivery, 0, len(turns))
	for _, turn := range turns {
		out = append(out, batchedDelivery(turn))
	}
	return out
}

func batchedDelivery(row gen.ListConversationBatchedTurnsInRangeRow) domain.ConversationBatchedDelivery {
	return domain.ConversationBatchedDelivery{
		TurnID:              row.ID,
		DeliveredWithTurnID: row.PromotedToTurnID.String,
		SteerActivityID:     row.BatchDeliverySteerID,
	}
}
