package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/terminalview"
)

var errInvalidTerminalViewer = errors.New("invalid terminal viewer frame")

type terminalViewerState struct {
	mu     sync.RWMutex
	viewer terminalview.Viewer
}

func (s *terminalViewerState) set(viewer terminalview.Viewer) {
	s.mu.Lock()
	s.viewer = viewer
	s.mu.Unlock()
}

func (s *terminalViewerState) get() terminalview.Viewer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.viewer
}

func retryableTerminalViewerError(err error) bool {
	return errors.Is(err, postgres.ErrWorkerUnavailable) || errors.Is(err, postgres.ErrConflict)
}

func (s *Server) refreshTerminalViewer(
	ctx context.Context, terminal domain.TerminalSession, viewerID string, state *terminalViewerState,
) {
	ticker := time.NewTicker(terminalViewerRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.store.UpsertTerminalViewer(
				ctx, terminal, viewerID, state.get(), terminalViewerLease,
			); err != nil && !errors.Is(err, context.Canceled) && s.logger != nil {
				s.logger.Debug("refresh terminal viewer", "terminal_id", terminal.ID, "error", err)
			}
		}
	}
}

func parseTerminalViewerFrame(data []byte) (terminalview.Viewer, error) {
	var frame struct {
		Type    string  `json:"type"`
		Role    string  `json:"role"`
		Visible *bool   `json:"visible"`
		Columns *uint16 `json:"columns"`
		Rows    *uint16 `json:"rows"`
	}
	if err := json.Unmarshal(data, &frame); err != nil || frame.Type != "viewer" ||
		(frame.Role != "primary" && frame.Role != "secondary") ||
		frame.Visible == nil || frame.Columns == nil || frame.Rows == nil {
		return terminalview.Viewer{}, errInvalidTerminalViewer
	}
	return terminalview.Viewer{
		Role: frame.Role, Visible: *frame.Visible,
		Columns: *frame.Columns, Rows: *frame.Rows,
	}, nil
}
