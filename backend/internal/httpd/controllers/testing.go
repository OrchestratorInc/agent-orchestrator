package controllers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
)

// TestingService is the controller-facing contract for `ao testing` verbs.
type TestingService interface {
	Start(ctx context.Context, req litmus.StartRequest) (litmus.Started, error)
	Intake(ctx context.Context, sessionID, prURL string) (litmus.Run, error)
	Prepare(ctx context.Context, sessionID string) (litmus.Run, error)
	Launch(ctx context.Context, sessionID, leg string) (litmus.Run, error)
	Relaunch(ctx context.Context, sessionID, leg string) (litmus.Run, error)
	Status(ctx context.Context, sessionID string) (litmus.RunStatus, error)
	Submit(ctx context.Context, sessionID, verdictPath string) (litmus.Verdict, error)
	Cleanup(ctx context.Context, sessionID string) (litmus.Run, error)
	Drive(ctx context.Context, sessionID, action string, args []string) (map[string]any, error)
	RefuseTester(sessionID, verb string) error
	Prune(ctx context.Context, keepRuns, keepCheckouts int) (litmus.PruneReport, error)
	PruneDryRun(ctx context.Context, keepRuns, keepCheckouts int) (litmus.PruneReport, error)
	Runs(ctx context.Context, projectID string, limit int) (litmus.RunList, error)
	RunDetail(ctx context.Context, id string) (litmus.RunDetail, error)
	Post(ctx context.Context, runID string) (litmus.Run, error)
	Discard(ctx context.Context, runID string) (litmus.Run, error)
	RecipeValidate(ctx context.Context, req litmus.RecipeValidateRequest) (litmus.Recipe, error)
	ProjectSettings(ctx context.Context, projectID string) (litmus.ProjectSettings, error)
	SetProjectSettings(ctx context.Context, projectID string, ps litmus.ProjectSettings) (litmus.ProjectSettings, error)
}

// TestingController owns the /testing routes. The router mounts it only when a
// service is wired.
type TestingController struct{ Svc TestingService }

// Register mounts the testing routes on r.
func (c *TestingController) Register(r chi.Router) {
	s := c.Svc
	r.Get("/testing/status", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.Status(r.Context(), strings.TrimSpace(r.URL.Query().Get("sessionId")))
		writeTesting(w, r, out, err)
	})
	r.Get("/testing/runs", func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "TESTING_LIMIT_INVALID", "limit must be a positive integer", nil)
				return
			}
			limit = n
		}
		out, err := s.Runs(r.Context(), r.URL.Query().Get("projectId"), limit)
		writeTesting(w, r, out, err)
	})
	r.Get("/testing/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.RunDetail(r.Context(), chi.URLParam(r, "id"))
		writeTesting(w, r, out, err)
	})
	r.Post("/testing/runs/{id}/post", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		if err := s.RefuseTester(in.SessionID, "post"); err != nil {
			return litmus.Run{}, err
		}
		return s.Post(r.Context(), chi.URLParam(r, "id"))
	}))
	r.Post("/testing/runs/{id}/discard", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		if err := s.RefuseTester(in.SessionID, "discard"); err != nil {
			return litmus.Run{}, err
		}
		return s.Discard(r.Context(), chi.URLParam(r, "id"))
	}))
	r.Get("/testing/projects/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.ProjectSettings(r.Context(), chi.URLParam(r, "id"))
		writeTesting(w, r, out, err)
	})
	r.Put("/testing/projects/{id}/settings", c.setProjectSettings)
	r.Post("/testing/start", testingPost(func(r *http.Request, in TestingRequest) (litmus.Started, error) {
		if err := s.RefuseTester(in.SessionID, "start"); err != nil {
			return litmus.Started{}, err
		}
		return s.Start(r.Context(), litmus.StartRequest{ProjectID: in.ProjectID, Harness: in.Harness, PRURL: in.PRURL, CandidateURL: in.CandidateURL, NoComment: in.NoComment, NoEvidence: in.NoEvidence})
	}))
	r.Post("/testing/intake", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		return s.Intake(r.Context(), in.SessionID, in.PRURL)
	}))
	r.Post("/testing/prepare", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		return s.Prepare(r.Context(), in.SessionID)
	}))
	r.Post("/testing/launch", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		if in.Relaunch {
			return s.Relaunch(r.Context(), in.SessionID, in.Leg)
		}
		return s.Launch(r.Context(), in.SessionID, in.Leg)
	}))
	r.Post("/testing/submit", testingPost(func(r *http.Request, in TestingRequest) (litmus.Verdict, error) {
		return s.Submit(r.Context(), in.SessionID, in.VerdictPath)
	}))
	r.Post("/testing/cleanup", testingPost(func(r *http.Request, in TestingRequest) (litmus.Run, error) {
		return s.Cleanup(r.Context(), in.SessionID)
	}))
	r.Post("/testing/drive", testingPost(func(r *http.Request, in TestingRequest) (map[string]any, error) {
		return s.Drive(r.Context(), in.SessionID, strings.ToLower(strings.TrimSpace(in.Action)), in.Args)
	}))
	r.Post("/testing/recipe/validate", testingPost(func(r *http.Request, in TestingRequest) (litmus.Recipe, error) {
		if err := s.RefuseTester(in.SessionID, "recipe validate"); err != nil {
			return litmus.Recipe{}, err
		}
		return s.RecipeValidate(r.Context(), litmus.RecipeValidateRequest{Path: in.Path, SHA: in.SHA, ProjectID: in.ProjectID})
	}))
	r.Post("/testing/prune", testingPost(func(r *http.Request, in TestingRequest) (litmus.PruneReport, error) {
		if err := s.RefuseTester(in.SessionID, "prune"); err != nil {
			return litmus.PruneReport{}, err
		}
		keepRuns, keepCheckouts := litmus.DefaultKeepRuns, litmus.DefaultKeepCheckouts
		if in.KeepRuns != nil {
			keepRuns = *in.KeepRuns
		}
		if in.KeepCheckouts != nil {
			keepCheckouts = *in.KeepCheckouts
		}
		if in.DryRun {
			return s.PruneDryRun(r.Context(), keepRuns, keepCheckouts)
		}
		return s.Prune(r.Context(), keepRuns, keepCheckouts)
	}))
}

func (c *TestingController) setProjectSettings(w http.ResponseWriter, r *http.Request) {
	var in UpdateTestingProjectSettingsRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Svc.RefuseTester(strings.TrimSpace(in.SessionID), "settings"); err != nil {
		writeTesting(w, r, nil, err)
		return
	}
	projectID := chi.URLParam(r, "id")
	out, err := c.Svc.SetProjectSettings(r.Context(), projectID, litmus.ProjectSettings{
		AskBeforePosting: in.AskBeforePosting,
		Harness:          in.Harness,
		Model:            in.Model,
		Effort:           in.Effort,
	})
	writeTesting(w, r, out, err)
}

// testingPost decodes a strict JSON body, calls the verb, and renders the result or error.
func testingPost[T any](call func(*http.Request, TestingRequest) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in TestingRequest
		if err := decodeJSONStrict(r, &in); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
		in.SessionID = strings.TrimSpace(in.SessionID) // the status route trims too
		out, err := call(r, in)
		writeTesting(w, r, out, err)
	}
}

func writeTesting(w http.ResponseWriter, r *http.Request, out any, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
