package workertransport

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// A session of a project with additional repositories has one checkout per
// repository beside the main one. Every workspace request (file browser,
// diff, review) may name the checkout it is about with a top-level
// "repository" field in its payload: the checkout's folder name or the
// repository's GitHub owner/name. Without it the request is about the main
// checkout, exactly as before.

// errUnknownRepository is a repository selector that names none of the
// session's checkouts, or one that is not on disk.
var errUnknownRepository = errors.New("repository is not one of this session's checkouts")

// repositorySelector is the optional field every workspace request carries.
type repositorySelector struct {
	Repository string `json:"repository"`
}

// repositoryWorkspaces opens additional checkouts on first use, each confined
// with its own os.Root like the main workspace.
type repositoryWorkspaces struct {
	mu   sync.Mutex
	open map[string]*workspace
}

func (r *repositoryWorkspaces) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ws := range r.open {
		_ = ws.Close()
	}
	r.open = nil
}

// workspaceFor returns the workspace a request is about and the checkout's
// folder name (empty for the main checkout).
func (s *Supervisor) workspaceFor(main *workspace, payload any) (*workspace, string, error) {
	var selector repositorySelector
	if err := decodePayload(payload, &selector); err != nil || strings.TrimSpace(selector.Repository) == "" {
		return main, "", nil
	}
	checkouts := append([]worker.RepoCheckout{s.mainCheckout()}, s.Repositories...)
	checkout, err := worker.ResolveRepoCheckout(checkouts, selector.Repository)
	if err != nil {
		return nil, "", errUnknownRepository
	}
	if checkout.Main {
		return main, "", nil
	}
	s.repositories.mu.Lock()
	defer s.repositories.mu.Unlock()
	if ws, ok := s.repositories.open[checkout.Path]; ok {
		return ws, checkout.Name, nil
	}
	if info, err := os.Stat(checkout.Path); err != nil || !info.IsDir() {
		return nil, "", errUnknownRepository
	}
	ws, err := openWorkspace(checkout.Path)
	if err != nil {
		return nil, "", err
	}
	ws.compareBase = "origin/HEAD"
	if checkout.Branch != "" {
		ws.compareBase = "origin/" + checkout.Branch
	}
	if s.repositories.open == nil {
		s.repositories.open = map[string]*workspace{}
	}
	s.repositories.open[checkout.Path] = ws
	return ws, checkout.Name, nil
}

func mainCheckoutName(path string) string {
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	return parts[len(parts)-1]
}

// mainCheckout is the main repository's checkout: always the workspace
// folder, with the repository's identity when the worker knows its URL.
func (s *Supervisor) mainCheckout() worker.RepoCheckout {
	main := s.MainRepository
	main.Name, main.Path, main.Main = mainCheckoutName(s.Workspace), s.Workspace, true
	return main
}

// workspaceRepositories lists the session's checkouts for the
// "workspace.repositories" request and for the main review summary, so the
// review panel can offer a repository switcher. A single-repository session
// lists nothing.
func (s *Supervisor) workspaceRepositories() []worker.WorkspaceRepository {
	if len(s.Repositories) == 0 {
		return nil
	}
	main := s.mainCheckout()
	// The main checkout's folder is a provider's fixed name ("repository" on
	// Freestyle and Coder), so it is labeled with the repository's own name.
	label := ""
	if strings.TrimSpace(main.URL) != "" {
		label = worker.ExtraRepoDirName(main.URL)
	}
	repositories := []worker.WorkspaceRepository{{
		Name: main.Name, Label: label, Repository: main.FullName, Main: true, Present: true,
	}}
	for _, checkout := range s.Repositories {
		info, err := os.Stat(checkout.Path)
		repositories = append(repositories, worker.WorkspaceRepository{
			Name: checkout.Name, Repository: checkout.FullName, Branch: checkout.Branch,
			Present: err == nil && info.IsDir(),
		})
	}
	return repositories
}
