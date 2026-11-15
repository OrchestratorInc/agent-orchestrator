package sessionmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (m *Manager) StageAttachments(
	ctx context.Context,
	id domain.SessionID,
	attachments []ports.SpawnAttachment,
) ([]string, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return nil, ports.ErrSessionNotFound
	}
	provisioning := rec.ProvisionState.IsProvisioning()
	if rec.Metadata.WorkspacePath == "" && !provisioning {
		return nil, fmt.Errorf("session %s has no workspace", id)
	}
	if rec.Metadata.WorkspacePath != "" {
		if err := m.workspace.AddExclude(ctx, workspaceInfo(rec), "/"+attachmentsDir+"/"); err != nil {
			return nil, fmt.Errorf("exclude attachments: %w", err)
		}
	}

	refs := make([]string, 0, len(attachments))
	for i, a := range attachments {
		ext := a.Ext
		if ext == "" {
			ext = ".bin"
		}
		var name string
		for attempt := 0; attempt < 8; attempt++ {
			suffix, err := m.attachmentSuffix()
			if err != nil {
				return nil, fmt.Errorf("name attachment %d: %w", i+1, err)
			}
			name = "attachment-" + suffix + ext
			if rec.Metadata.WorkspacePath == "" {
				err = m.attachments.PutCanonical(ctx, id, name, a.Data)
			} else {
				err = m.attachments.Put(ctx, id, rec.Metadata.WorkspacePath, name, a.Data)
			}
			if err == nil {
				break
			}
			if !errors.Is(err, attachmentstore.ErrExists) {
				return nil, fmt.Errorf("write attachment %d: %w", i+1, err)
			}
			name = ""
		}
		if name == "" {
			return nil, fmt.Errorf("write attachment %d: could not allocate a unique name", i+1)
		}
		refs = append(refs, attachmentsDir+"/"+name)
	}

	if rec.Metadata.WorkspacePath == "" {
		latest, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get %s after staging: %w", id, err)
		}
		if !ok || latest.Metadata.WorkspacePath == "" {
			return refs, nil
		}
		rec = latest
		if err := m.workspace.AddExclude(ctx, workspaceInfo(rec), "/"+attachmentsDir+"/"); err != nil {
			return nil, fmt.Errorf("exclude attachments: %w", err)
		}
		if _, err := m.attachments.MaterializeWorkspace(ctx, id, rec.Metadata.WorkspacePath, nil); err != nil {
			return nil, fmt.Errorf("materialize staged attachments: %w", err)
		}
	}
	return refs, nil
}

func randomSuffix() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
