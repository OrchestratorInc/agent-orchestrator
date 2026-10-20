package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
)

const (
	reposTestOrgID     = "00000000-0000-0000-0000-0000000000aa"
	reposTestProjectID = "00000000-0000-0000-0000-0000000000bb"
	reposTestMainURL   = "https://github.com/acme/alice"
)

// projectReposFakeStore records project creates and settings patches and
// answers the GitHub App grant lookup for a project's repositories.
type projectReposFakeStore struct {
	Store
	access          domain.GitHubRepositoryAccess
	accessCalls     int
	createdGitHub   *domain.CreateGitHubProject
	created         *domain.CreateProject
	createdKeys     []string
	project         domain.Project
	settingsPatched int
	adminErr        error
}

func (s *projectReposFakeStore) GitHubRepositoryAccess(_ context.Context, _ domain.Principal, _ string, _ int64, _ []string) (domain.GitHubRepositoryAccess, error) {
	s.accessCalls++
	return s.access, nil
}

func (s *projectReposFakeStore) CreateGitHubProject(_ context.Context, _ domain.Principal, _, _ string, input domain.CreateGitHubProject) (domain.Project, error) {
	s.createdGitHub = &input
	return domain.Project{ID: reposTestProjectID, RepositoryURL: s.access.MainURL, Config: input.Config}, nil
}

func (s *projectReposFakeStore) CreateProject(_ context.Context, _ domain.Principal, _, key string, input domain.CreateProject) (domain.Project, error) {
	s.created = &input
	s.createdKeys = append(s.createdKeys, key)
	return domain.Project{ID: reposTestProjectID, RepositoryURL: input.RepositoryURL, Config: input.Config}, nil
}

func (s *projectReposFakeStore) GetProject(context.Context, domain.Principal, string, string) (domain.Project, error) {
	return s.project, nil
}

func (s *projectReposFakeStore) UpdateProjectSettings(_ context.Context, _ domain.Principal, _, _ string, _ domain.ProjectSettingsPatch) (domain.Project, error) {
	s.settingsPatched++
	return s.project, nil
}

func (s *projectReposFakeStore) RequireOrgAdmin(context.Context, domain.Principal, string) error {
	return s.adminErr
}

func grantedAccess(installations map[string]string) domain.GitHubRepositoryAccess {
	return domain.GitHubRepositoryAccess{MainURL: reposTestMainURL, MainInstallation: "inst-1", Installations: installations}
}

func reposTestRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "key-1")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("orgId", reposTestOrgID)
	routeContext.URLParams.Add("projectId", reposTestProjectID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: "00000000-0000-0000-0000-000000000001"})
	return request.WithContext(ctx)
}

func reposTestServer(store *projectReposFakeStore) *Server {
	return New(Options{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func TestCreateGitHubProjectValidatesItsAdditionalRepositories(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		access       domain.GitHubRepositoryAccess
		status       int
		code         string
		wantConfig   string
	}{
		{
			name:   "granted in the same installation",
			config: `{"repos":[{"url":"https://www.github.com/acme/web.git"}],"postCreate":[{"command":"npm ci","repository":"https://github.com/acme/web"}]}`,
			access: grantedAccess(map[string]string{"acme/web": "inst-1"}),
			status: http.StatusCreated,
			// Stored as a plain command, which releases that predate
			// per-repository steps still keep and run.
			wantConfig: `{"postCreate":["(\ncd -- '../web' || exit 1\nnpm ci\n)"],"repos":[{"url":"https://github.com/acme/web"}]}`,
		},
		{
			name:       "legacy coder list becomes repos",
			config:     `{"coder":{"extraRepos":[{"url":"https://github.com/acme/web"}]}}`,
			access:     grantedAccess(map[string]string{"acme/web": "inst-1"}),
			status:     http.StatusCreated,
			wantConfig: `{"coder":{},"repos":[{"url":"https://github.com/acme/web"}]}`,
		},
		{
			name:   "not granted to the App",
			config: `{"repos":[{"url":"https://github.com/acme/web"}]}`,
			access: grantedAccess(map[string]string{}),
			status: http.StatusUnprocessableEntity, code: "repository_not_accessible",
		},
		{
			name:   "another installation",
			config: `{"repos":[{"url":"https://github.com/other/web"}]}`,
			access: grantedAccess(map[string]string{"other/web": "inst-2"}),
			status: http.StatusUnprocessableEntity, code: "repository_installation_mismatch",
		},
		{
			name:   "the main repository again",
			config: `{"repos":[{"url":"https://github.com/acme/alice"}]}`,
			access: grantedAccess(map[string]string{"acme/alice": "inst-1"}),
			status: http.StatusUnprocessableEntity, code: "validation_error",
		},
		{
			name:   "a step in an unknown repository",
			config: `{"postCreate":[{"command":"x","repository":"https://github.com/acme/web"}]}`,
			access: grantedAccess(map[string]string{}),
			status: http.StatusUnprocessableEntity, code: "validation_error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &projectReposFakeStore{access: tc.access}
			recorder := httptest.NewRecorder()
			reposTestServer(store).createGitHubProject(recorder, reposTestRequest(t, http.MethodPost, "/github/projects",
				`{"githubRepositoryId":"42","config":`+tc.config+`}`))
			if recorder.Code != tc.status || (tc.code != "" && !strings.Contains(recorder.Body.String(), tc.code)) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if tc.wantConfig != "" && (store.createdGitHub == nil || string(store.createdGitHub.Config) != tc.wantConfig) {
				t.Fatalf("stored config = %+v", store.createdGitHub)
			}
		})
	}
}

// A project created with no multi-repo settings stores its config unchanged.
func TestCreateGitHubProjectKeepsASingleRepoConfig(t *testing.T) {
	store := &projectReposFakeStore{access: grantedAccess(map[string]string{})}
	recorder := httptest.NewRecorder()
	reposTestServer(store).createGitHubProject(recorder, reposTestRequest(t, http.MethodPost, "/github/projects",
		`{"githubRepositoryId":"42","config":{"worker":{"agent":"codex"},"postCreate":["npm ci"]}}`))
	if recorder.Code != http.StatusCreated || string(store.createdGitHub.Config) != `{"postCreate":["npm ci"],"worker":{"agent":"codex"}}` {
		t.Fatalf("status=%d config=%s", recorder.Code, store.createdGitHub.Config)
	}
}

func TestCreateStandaloneProject(t *testing.T) {
	store := &projectReposFakeStore{}
	server := reposTestServer(store)
	recorder := httptest.NewRecorder()
	server.createStandaloneProject(recorder, reposTestRequest(t, http.MethodPost, "/projects/standalone",
		`{"displayName":" Scratch ","config":{"postCreate":["make up"]}}`))
	if recorder.Code != http.StatusCreated || store.created == nil {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := store.created.RepositoryURL
	if !domain.IsStandaloneRepositoryURL(first) || store.created.DefaultBranch != "main" || store.created.DisplayName != "Scratch" ||
		!strings.Contains(string(store.created.Config), `"source":"standalone"`) {
		t.Fatalf("created = %+v", store.created)
	}
	var response struct {
		Project projectResponse `json:"project"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Project.Standalone {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	// A retry with the same idempotency key gets the same placeholder.
	server.createStandaloneProject(httptest.NewRecorder(), reposTestRequest(t, http.MethodPost, "/projects/standalone", `{"displayName":"Scratch"}`))
	if store.created.RepositoryURL != first {
		t.Fatalf("retry placeholder %q, first %q", store.created.RepositoryURL, first)
	}

	for name, body := range map[string]string{
		"repos":              `{"displayName":"S","config":{"repos":[{"url":"https://github.com/acme/web"}]}}`,
		"legacy repos":       `{"displayName":"S","config":{"coder":{"extraRepos":[{"url":"https://github.com/acme/web"}]}}}`,
		"a step in a repo":   `{"displayName":"S","config":{"postCreate":[{"command":"x","repository":"https://github.com/acme/web"}]}}`,
		"no name":            `{"displayName":" "}`,
		"bad start location": `{"displayName":"S","config":{"agentStartsIn":"home"}}`,
	} {
		store.created = nil
		recorder := httptest.NewRecorder()
		server.createStandaloneProject(recorder, reposTestRequest(t, http.MethodPost, "/projects/standalone", body))
		if recorder.Code != http.StatusUnprocessableEntity || store.created != nil {
			t.Errorf("%s: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProjectSettingsPatchChecksAdditionalRepositoryGrants(t *testing.T) {
	repositoryID := int64(42)
	project := domain.Project{ID: reposTestProjectID, RepositoryURL: reposTestMainURL, GitHubRepositoryID: &repositoryID}
	for _, tc := range []struct {
		name, body string
		access     domain.GitHubRepositoryAccess
		status     int
	}{
		{"granted", `{"config":{"repos":[{"url":"https://github.com/acme/web"}]}}`, grantedAccess(map[string]string{"acme/web": "inst-1"}), http.StatusOK},
		{"cleared", `{"config":{"repos":[]}}`, grantedAccess(nil), http.StatusOK},
		{"not granted", `{"config":{"repos":[{"url":"https://github.com/acme/web"}]}}`, grantedAccess(map[string]string{}), http.StatusUnprocessableEntity},
		{"main repository", `{"config":{"repos":[{"url":"https://github.com/acme/alice"}]}}`, grantedAccess(nil), http.StatusUnprocessableEntity},
		{"rules", `{"config":{"agentRules":"Read AGENTS.md","orchestratorRules":"Split work per repo"}}`, grantedAccess(nil), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &projectReposFakeStore{access: tc.access, project: project}
			recorder := httptest.NewRecorder()
			reposTestServer(store).updateProjectSettings(recorder, reposTestRequest(t, http.MethodPatch, "/settings", tc.body))
			if recorder.Code != tc.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if (tc.status == http.StatusOK) != (store.settingsPatched == 1) {
				t.Fatalf("patched %d times", store.settingsPatched)
			}
		})
	}
}

func TestLaunchContextCarriesEveryProjectRepository(t *testing.T) {
	launch, err := launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", ProjectID: "project-1", Kind: "worker", Harness: "claude-code",
		RepositoryURL: reposTestMainURL, DefaultBranch: "main",
		ProjectConfig: json.RawMessage(`{"sandboxProvider":"freestyle","agentStartsIn":"parent",
			"repos":[{"url":"https://github.com/acme/web","branch":"develop"}],
			"postCreate":["make up",{"command":"npm ci","repository":"https://github.com/acme/web"}]}`),
	}, launchCapabilities{resolvesWorkspacePath: true, multiRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(launch.ExtraRepos) != 1 || launch.ExtraRepos[0] != (worker.RepoRef{URL: "https://github.com/acme/web", Branch: "develop"}) {
		t.Fatalf("extra repos = %+v", launch.ExtraRepos)
	}
	if launch.AgentStartsIn != domain.ProjectAgentStartsInParent {
		t.Fatalf("agent starts in %q", launch.AgentStartsIn)
	}
	if len(launch.PostCreate) != 2 || launch.PostCreate[0] != "make up" || launch.PostCreate[1] != "(\ncd -- '../web' || exit 1\nnpm ci\n)" {
		t.Fatalf("postCreate = %q", launch.PostCreate)
	}
	for _, want := range []string{"https://github.com/acme/web (folder web, cloned from branch develop)", "starts in the folder that holds every repository's checkout"} {
		if !strings.Contains(launch.SystemPrompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, launch.SystemPrompt)
		}
	}

	// A worker without the multi-repo capability gets the earlier wording, no
	// start folder and no per-repository pull request instructions.
	launch, err = launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code",
		RepositoryURL: reposTestMainURL, DefaultBranch: "main",
		ProjectConfig: json.RawMessage(`{"agentStartsIn":"parent","repos":[{"url":"https://github.com/acme/web"}]}`),
	}, launchCapabilities{resolvesWorkspacePath: true})
	if err != nil {
		t.Fatal(err)
	}
	if launch.AgentStartsIn != "" || len(launch.ExtraRepos) != 1 ||
		!strings.Contains(launch.SystemPrompt, "- Additional repositories (part of this project, checked out alongside the primary):\n  - https://github.com/acme/web") ||
		strings.Contains(launch.SystemPrompt, `"repository"`) || strings.Contains(launch.SystemPrompt, "session branch") {
		t.Fatalf("legacy worker launch = %q\n%s", launch.AgentStartsIn, launch.SystemPrompt)
	}

	// A single-repo project launches exactly as before.
	launch, err = launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code", RepositoryURL: reposTestMainURL,
		ProjectConfig: json.RawMessage(`{"postCreate":["npm ci"]}`),
	}, launchCapabilities{resolvesWorkspacePath: true, multiRepo: true})
	if err != nil || launch.ExtraRepos != nil || launch.AgentStartsIn != "" || strings.Contains(launch.SystemPrompt, "Repositories (") {
		t.Fatalf("single repo launch = %+v %v", launch, err)
	}
}

// workerExtraReposFakeStore answers the session's declared additional repositories.
type workerExtraReposFakeStore struct {
	Store
	extras []domain.RepoRef
}

func (s *workerExtraReposFakeStore) WorkerSessionExtraRepos(context.Context, string, string) ([]domain.RepoRef, error) {
	return s.extras, nil
}

func TestPATPullRequestCloneURLTargetsADeclaredRepository(t *testing.T) {
	server := &Server{store: &workerExtraReposFakeStore{extras: []domain.RepoRef{{URL: "https://github.com/acme/web"}}}}
	claims := worker.Claims{OrgID: reposTestOrgID, SessionID: "session-1"}
	primary := "https://github.com/acme/alice.git"
	for requested, want := range map[string]string{
		"":           primary,
		"ACME/alice": primary,
		"acme/Web":   "https://github.com/acme/web.git",
	} {
		got, err := server.patPullRequestCloneURL(context.Background(), claims, primary, requested)
		if err != nil || got != want {
			t.Errorf("%q = %q %v, want %q", requested, got, err, want)
		}
	}
	if _, err := server.patPullRequestCloneURL(context.Background(), claims, primary, "acme/other"); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("undeclared repository err = %v", err)
	}
	if _, err := server.patPullRequestCloneURL(context.Background(), claims, domain.StandaloneRepositoryURL("p"), ""); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("standalone err = %v", err)
	}
}

// Only an admin may create a project with additional repositories (or the
// other admin settings), on every create path, legacy coder input included.
func TestCreatingAProjectWithAdminSettingsRequiresAnAdmin(t *testing.T) {
	for _, tc := range []struct {
		name, target, body string
		create             func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"github repos", "/github/projects", `{"githubRepositoryId":"42","config":{"repos":[{"url":"https://github.com/acme/web"}]}}`,
			(*Server).createGitHubProject},
		{"github legacy extraRepos", "/github/projects", `{"githubRepositoryId":"42","config":{"coder":{"extraRepos":[{"url":"https://github.com/acme/web"}]}}}`,
			(*Server).createGitHubProject},
		{"github rules", "/github/projects", `{"githubRepositoryId":"42","config":{"agentRules":"x"}}`,
			(*Server).createGitHubProject},
		{"standalone start folder", "/projects/standalone", `{"displayName":"Box","config":{"workingDirectory":"~/work"}}`,
			(*Server).createStandaloneProject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &projectReposFakeStore{access: grantedAccess(map[string]string{"acme/web": "inst-1"}), adminErr: postgres.ErrForbidden}
			recorder := httptest.NewRecorder()
			tc.create(reposTestServer(store), recorder, reposTestRequest(t, http.MethodPost, tc.target, tc.body))
			if recorder.Code != http.StatusForbidden || store.createdGitHub != nil || store.created != nil {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	// A member may still create a single-repository project.
	store := &projectReposFakeStore{access: grantedAccess(nil), adminErr: postgres.ErrForbidden}
	recorder := httptest.NewRecorder()
	reposTestServer(store).createGitHubProject(recorder, reposTestRequest(t, http.MethodPost, "/github/projects", `{"githubRepositoryId":"42","config":{}}`))
	if recorder.Code != http.StatusCreated || store.createdGitHub == nil {
		t.Fatalf("member single-repo create: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// Without an active App grant on the main repository, additional repositories
// cannot be confirmed to share its installation, so they are refused.
func TestAdditionalReposFailClosedWithoutAMainGrant(t *testing.T) {
	store := &projectReposFakeStore{access: domain.GitHubRepositoryAccess{Installations: map[string]string{"acme/web": "inst-1"}}}
	recorder := httptest.NewRecorder()
	reposTestServer(store).createGitHubProject(recorder, reposTestRequest(t, http.MethodPost, "/github/projects",
		`{"githubRepositoryId":"42","config":{"repos":[{"url":"https://github.com/acme/web"}]}}`))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "repository_not_accessible") {
		t.Fatalf("create: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	mainID := int64(42)
	store = &projectReposFakeStore{
		access:  domain.GitHubRepositoryAccess{MainURL: reposTestMainURL, Installations: map[string]string{"acme/web": "inst-1"}},
		project: domain.Project{ID: reposTestProjectID, RepositoryURL: reposTestMainURL, GitHubRepositoryID: &mainID},
	}
	recorder = httptest.NewRecorder()
	reposTestServer(store).updateProjectSettings(recorder, reposTestRequest(t, http.MethodPatch, "/settings",
		`{"config":{"repos":[{"url":"https://github.com/acme/web"}]}}`))
	if recorder.Code != http.StatusUnprocessableEntity || store.settingsPatched != 0 {
		t.Fatalf("patch: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
