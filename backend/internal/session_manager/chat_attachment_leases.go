package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ChatAttachmentLeaseTTL is how long a file staged for an unsent draft is kept
// without its draft being opened again. A composer renews its draft's leases
// whenever it restores the draft, so only a draft nobody has opened for this
// long loses its files; an abandoned stage (a renderer that crashed or dropped
// the result) is reclaimed after it.
const ChatAttachmentLeaseTTL = 7 * 24 * time.Hour

// chatAttachmentLeaseStore is the ownership record for draft-staged files.
//
// The model: staging a draft file leases it; accepting any message that names
// it commits it (the lease row is deleted in the same write that accepts the
// message, so there is no window in which an accepted file is still leased);
// removing its chip or discarding its draft releases the lease; a lease that
// is not renewed within ChatAttachmentLeaseTTL expires into released. Only a
// released lease's files are deleted, and only a live lease can be released,
// so a file named by accepted history is never deleted before its session.
type chatAttachmentLeaseStore interface {
	LeaseChatAttachment(ctx context.Context, id domain.SessionID, name string, now time.Time) error
	ReleaseChatAttachmentLeases(ctx context.Context, id domain.SessionID, names []string, now time.Time) error
	RenewChatAttachmentLeases(ctx context.Context, id domain.SessionID, names []string, now time.Time) ([]string, error)
	ExpireChatAttachmentLeases(ctx context.Context, id domain.SessionID, cutoff, now time.Time) error
	ReleasedChatAttachmentLeases(ctx context.Context, id domain.SessionID) (map[domain.SessionID][]string, error)
	ForgetChatAttachmentLease(ctx context.Context, id domain.SessionID, name string) error
}

func (m *Manager) chatAttachmentLeases() (chatAttachmentLeaseStore, bool) {
	leases, ok := m.store.(chatAttachmentLeaseStore)
	return leases, ok && m.attachments != nil
}

// StageDraftAttachments is StageAttachments for files picked into an unsent
// Chat draft: each file is leased to the draft, so removing it or discarding
// the draft before sending reclaims both stored copies. A store without lease
// support stages exactly as StageAttachments does.
func (m *Manager) StageDraftAttachments(
	ctx context.Context,
	id domain.SessionID,
	attachments []ports.SpawnAttachment,
) ([]string, error) {
	refs, err := m.stageAttachments(ctx, id, attachments, true)
	if err != nil {
		return nil, err
	}
	// Staging is a natural moment to reclaim this session's abandoned drafts:
	// it is exactly when stored attachments grow.
	if reclaimErr := m.reclaimChatAttachments(ctx, id); reclaimErr != nil {
		m.logger.Warn("reclaim staged chat attachments failed", "sessionID", id, "error", reclaimErr)
	}
	return refs, nil
}

// UpdateAttachmentLeases releases and renews draft leases by worktree path
// and answers which of the renewed paths no longer have a stored file. A path
// that is not a staged attachment, or that has no live lease (sent, staged
// without a lease, or already released), is ignored by release: the caller
// can only give up files it still owns.
func (m *Manager) UpdateAttachmentLeases(ctx context.Context, id domain.SessionID, release, renew []string) ([]string, error) {
	if _, ok, err := m.store.GetSession(ctx, id); err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if !ok {
		return nil, ports.ErrSessionNotFound
	}
	leases, ok := m.chatAttachmentLeases()
	if !ok {
		return []string{}, nil
	}
	now := m.clock()
	releaseNames, _ := attachmentNames(release)
	if err := leases.ReleaseChatAttachmentLeases(ctx, id, releaseNames, now); err != nil {
		return nil, err
	}
	renewNames, renewPaths := attachmentNames(renew)
	released, err := leases.RenewChatAttachmentLeases(ctx, id, renewNames, now)
	if err != nil {
		return nil, err
	}
	gone := map[string]bool{}
	for _, name := range released {
		gone[name] = true
	}
	missing := []string{}
	for i, name := range renewNames {
		if gone[name] || !m.attachments.Exists(ctx, id, name) {
			missing = append(missing, renewPaths[i])
		}
	}
	if err := m.reclaimChatAttachments(ctx, id); err != nil {
		m.logger.Warn("reclaim released chat attachments failed", "sessionID", id, "error", err)
	}
	return missing, nil
}

// ReclaimChatAttachments expires stale draft leases and deletes the files of
// every released lease. It is idempotent: a file already gone counts as
// reclaimed, and a lease whose files could not be deleted stays released for
// the next pass (startup, the next stage, or the next release).
func (m *Manager) ReclaimChatAttachments(ctx context.Context) error {
	return m.reclaimChatAttachments(ctx, "")
}

// reclaimChatAttachments is ReclaimChatAttachments for one session, or every
// session when id is empty.
func (m *Manager) reclaimChatAttachments(ctx context.Context, id domain.SessionID) error {
	leases, ok := m.chatAttachmentLeases()
	if !ok {
		return nil
	}
	now := m.clock()
	if err := leases.ExpireChatAttachmentLeases(ctx, id, now.Add(-ChatAttachmentLeaseTTL), now); err != nil {
		return err
	}
	released, err := leases.ReleasedChatAttachmentLeases(ctx, id)
	if err != nil {
		return err
	}
	var errs []error
	for session, names := range released {
		rec, found, err := m.store.GetSession(ctx, session)
		if err != nil {
			errs = append(errs, fmt.Errorf("get %s: %w", session, err))
			continue
		}
		workspacePath := ""
		if found {
			workspacePath = rec.Metadata.WorkspacePath
		}
		for _, name := range names {
			if err := m.attachments.Remove(ctx, session, workspacePath, name); err != nil {
				errs = append(errs, fmt.Errorf("remove %s/%s: %w", session, name, err))
				continue
			}
			if err := leases.ForgetChatAttachmentLease(ctx, session, name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// attachmentNames keeps the staged-attachment paths among paths, deduplicated,
// with their stored names. Anything else (an AO Cloud reference, a path the
// renderer did not get from staging) is dropped.
func attachmentNames(paths []string) (names, kept []string) {
	seen := map[string]bool{}
	for _, path := range paths {
		name, ok := attachmentstore.NameFromWorkspacePath(path)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		kept = append(kept, path)
	}
	return names, kept
}
