package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type modelCatalogKey struct {
	user, org, agent, session string
}

type modelCatalogResult struct {
	response      worker.ChatModelsResponse
	err           error
	status        int
	code, message string
}

type modelCatalogEntry struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	finished bool
	expires  time.Time
	result   modelCatalogResult
}

func (s *Server) discoverModelCatalog(w http.ResponseWriter, r *http.Request, orgID, sessionID, agent string) (worker.ChatModelsResponse, bool) {
	key := modelCatalogKey{principalFrom(r).UserID, orgID, agent, sessionID}
	s.modelCatalogMu.Lock()
	if s.modelCatalogs == nil {
		s.modelCatalogs = make(map[modelCatalogKey]*modelCatalogEntry)
	}
	for key, entry := range s.modelCatalogs {
		if entry.finished && !time.Now().Before(entry.expires) {
			delete(s.modelCatalogs, key)
		}
	}
	entry := s.modelCatalogs[key]
	if entry == nil {
		if len(s.modelCatalogs) >= 128 {
			var oldestKey modelCatalogKey
			var oldest *modelCatalogEntry
			for candidate, item := range s.modelCatalogs {
				if item.finished && (oldest == nil || item.expires.Before(oldest.expires)) {
					oldestKey, oldest = candidate, item
				}
			}
			if oldest == nil {
				s.modelCatalogMu.Unlock()
				writeError(w, r, http.StatusTooManyRequests, "MODEL_CATALOG_BUSY", "Model discovery is busy. Use the agent default or retry shortly.")
				return worker.ChatModelsResponse{}, false
			}
			delete(s.modelCatalogs, oldestKey)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
		entry = &modelCatalogEntry{done: make(chan struct{}), cancel: cancel}
		s.modelCatalogs[key] = entry
		go func() {
			defer cancel()
			result := s.fetchModelCatalog(r.WithContext(ctx), orgID, sessionID)
			s.modelCatalogMu.Lock()
			defer s.modelCatalogMu.Unlock()
			entry.result = result
			entry.finished = true
			ttl := 5 * time.Minute
			if result.err != nil || result.status != 0 {
				ttl = 5 * time.Second
			}
			entry.expires = time.Now().Add(ttl)
			close(entry.done)
		}()
	}
	entry.waiters++
	s.modelCatalogMu.Unlock()
	defer func() {
		s.modelCatalogMu.Lock()
		defer s.modelCatalogMu.Unlock()
		entry.waiters--
		if entry.waiters == 0 && !entry.finished {
			delete(s.modelCatalogs, key)
			entry.cancel()
		}
	}()
	select {
	case <-r.Context().Done():
		return worker.ChatModelsResponse{}, false
	case <-entry.done:
		result := entry.result
		if result.err != nil {
			s.writeWorkspaceStoreError(w, r, result.err)
			return worker.ChatModelsResponse{}, false
		}
		if result.status != 0 {
			writeError(w, r, result.status, result.code, result.message)
			return worker.ChatModelsResponse{}, false
		}
		return result.response, true
	}
}

func (s *Server) fetchModelCatalog(r *http.Request, orgID, sessionID string) modelCatalogResult {
	principal := principalFrom(r)
	request, err := s.store.CreateWorkspaceRequest(r.Context(), principal, orgID, sessionID, "chat.models", json.RawMessage(`{}`), 30*time.Second)
	if err != nil {
		return modelCatalogResult{err: err}
	}
	complete := false
	defer func() {
		if !complete {
			s.cancelWorkspaceRequest(r, principal, orgID, sessionID, request.ID)
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return modelCatalogResult{status: http.StatusGatewayTimeout, code: "WORKER_TIMEOUT", message: "The worker did not complete model discovery in time."}
		case <-ticker.C:
			current, err := s.store.GetWorkspaceRequest(r.Context(), principal, orgID, sessionID, request.ID)
			if err != nil {
				return modelCatalogResult{err: err}
			}
			switch current.Status {
			case "succeeded":
				complete = true
				return validateModelCatalog(current.Response)
			case "failed":
				complete = true
				status := http.StatusUnprocessableEntity
				if current.ErrorCode == "TRANSPORT_TIMEOUT" {
					status = http.StatusGatewayTimeout
				}
				return modelCatalogResult{status: status, code: current.ErrorCode, message: current.ErrorMessage}
			case "cancelled":
				complete = true
				return modelCatalogResult{status: http.StatusRequestTimeout, code: "REQUEST_CANCELLED", message: "The worker request was cancelled."}
			}
		}
	}
}

func validateModelCatalog(raw json.RawMessage) modelCatalogResult {
	invalid := modelCatalogResult{status: http.StatusBadGateway, code: "INVALID_WORKER_RESPONSE", message: "The worker returned an invalid model catalog."}
	var response worker.ChatModelsResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.Models == nil {
		return invalid
	}
	if len(response.Models) == 0 {
		return modelCatalogResult{status: http.StatusConflict, code: "MODEL_CATALOG_UNAVAILABLE", message: "The worker did not report model choices. Use the agent default or enter a model ID."}
	}
	seen := make(map[string]bool, len(response.Models))
	for _, model := range response.Models {
		if strings.TrimSpace(model.ID) == "" || strings.TrimSpace(model.DisplayName) == "" || seen[model.ID] {
			return invalid
		}
		seen[model.ID] = true
	}
	return modelCatalogResult{response: response}
}
