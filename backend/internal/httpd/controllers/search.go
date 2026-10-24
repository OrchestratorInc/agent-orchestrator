package controllers

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	searchsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/search"
)

// SearchService answers command-palette searches over workspace files and
// chat messages.
type SearchService interface {
	Search(ctx context.Context, req searchsvc.Request) (searchsvc.Response, error)
}

// SearchController owns GET /search.
type SearchController struct {
	Svc SearchService
}

// Register mounts the search route.
func (c *SearchController) Register(r chi.Router) {
	r.Get("/search", c.search)
}

func (c *SearchController) search(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/search")
		return
	}
	query := r.URL.Query()
	kinds, ok := parseSearchKinds(query.Get("kinds"))
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SEARCH_KINDS", "kinds must be a comma-separated subset of fileNames, fileContent, messages", nil)
		return
	}
	res, err := c.Svc.Search(r.Context(), searchsvc.Request{
		Query:     query.Get("q"),
		SessionID: domain.SessionID(strings.TrimSpace(query.Get("sessionId"))),
		Kinds:     kinds,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, searchResponse(res))
}

func parseSearchKinds(raw string) (searchsvc.Kinds, bool) {
	if strings.TrimSpace(raw) == "" {
		return searchsvc.Kinds{FileNames: true, FileContent: true, Messages: true}, true
	}
	var k searchsvc.Kinds
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "fileNames":
			k.FileNames = true
		case "fileContent":
			k.FileContent = true
		case "messages":
			k.Messages = true
		default:
			return searchsvc.Kinds{}, false
		}
	}
	return k, true
}

func searchResponse(res searchsvc.Response) SearchResponse {
	out := SearchResponse{
		FileNames:   make([]SearchFileNameHit, 0, len(res.FileNames)),
		FileContent: make([]SearchFileContentHit, 0, len(res.FileContent)),
		Messages:    make([]SearchMessageHit, 0, len(res.Messages)),
		Files:       string(res.Files),
	}
	for _, h := range res.FileNames {
		out.FileNames = append(out.FileNames, SearchFileNameHit{Path: h.Path, Score: h.Score, ExactName: h.ExactName})
	}
	for _, h := range res.FileContent {
		out.FileContent = append(out.FileContent, SearchFileContentHit{Path: h.Path, Snippet: searchSnippet(h.Snippet), Score: h.Score, Decisive: h.Decisive})
	}
	for _, h := range res.Messages {
		out.Messages = append(out.Messages, SearchMessageHit{
			MessageID:         h.MessageID,
			SessionID:         h.SessionID,
			ProjectID:         h.ProjectID,
			ReviewID:          h.ReviewID,
			ConversationTitle: h.ConversationTitle,
			Snippet:           searchSnippet(h.Snippet),
			Score:             h.Score,
			Decisive:          h.Decisive,
			OlderVersion:      h.OlderVersion,
		})
	}
	return out
}

// searchSnippet converts byte-offset highlights to UTF-16 code units so a
// JavaScript client can slice the text directly.
func searchSnippet(s searchsvc.Snippet) SearchSnippet {
	out := SearchSnippet{Text: s.Text, Line: s.Line, Highlights: make([]SearchHighlight, 0, len(s.Highlights))}
	for _, h := range s.Highlights {
		out.Highlights = append(out.Highlights, SearchHighlight{Start: utf16Len(s.Text[:h[0]]), End: utf16Len(s.Text[:h[1]])})
	}
	return out
}

func utf16Len(s string) int {
	n := 0
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		s = s[size:]
	}
	return n
}
