package litmus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Registered is an AO project as the repo resolver sees it: its checkout and the
// remote URLs it is known by (its Repo and CanonicalRepoURL; empty ones are skipped).
type Registered struct {
	Path string
	URLs []string
}

// NewRepoResolver maps PR repo names to a registered project's checkout. It first matches
// any name against the projects' URLs. Failing that it normalises each GitHub URL once with
// `gh repo view <url> --json nameWithOwner`, which follows renames, and matches again.
// Answers are cached for the resolver's lifetime; a gh failure skips that URL and is not cached.
func NewRepoResolver(list func(context.Context) ([]Registered, error)) RepoResolver {
	return newRepoResolver(execGH{}, list)
}

func newRepoResolver(gh GH, list func(context.Context) ([]Registered, error)) RepoResolver {
	var mu sync.Mutex
	canonical := map[string]string{} // gh argument -> lowercase owner/repo
	normalise := func(ctx context.Context, arg string) string {
		mu.Lock()
		got, ok := canonical[arg]
		mu.Unlock()
		if ok {
			return got
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out, err := gh.Run(ctx, "repo", "view", arg, "--json", "nameWithOwner")
		var v struct{ NameWithOwner string }
		if err != nil || json.Unmarshal(out, &v) != nil || !strings.Contains(v.NameWithOwner, "/") {
			return ""
		}
		got = strings.ToLower(v.NameWithOwner)
		mu.Lock()
		canonical[arg] = got
		mu.Unlock()
		return got
	}
	return func(ctx context.Context, names ...string) (string, error) {
		var wanted []string
		for _, n := range names {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !slices.Contains(wanted, n) {
				wanted = append(wanted, n)
			}
		}
		projects, err := list(ctx)
		if err != nil {
			return "", fmt.Errorf("cannot list the registered AO projects: %w", err)
		}
		match := func() (path string, ok bool) {
			for _, p := range projects {
				for _, u := range p.URLs {
					for _, n := range wanted {
						if owner, repo, _ := strings.Cut(n, "/"); MatchRepo(u, owner, repo) {
							return p.Path, true
						}
					}
				}
			}
			return "", false
		}
		if path, ok := match(); ok {
			return path, nil
		}
		// No URL names the repo: ask GitHub what each wanted name is called now, then what each project is.
		looked := slices.Clone(wanted)
		for _, n := range looked {
			if c := normalise(ctx, n); c != "" && !slices.Contains(wanted, c) {
				wanted = append(wanted, c)
			}
		}
		if path, ok := match(); ok {
			return path, nil
		}
		var registered []string
		for _, p := range projects {
			for _, u := range p.URLs {
				id, err := domain.ParseRepositoryIdentity(u)
				if err != nil || id.Provider != "github" {
					continue
				}
				name := id.Namespace + "/" + id.Name
				if c := normalise(ctx, u); slices.Contains(wanted, c) {
					return p.Path, nil
				} else if c != "" && !strings.EqualFold(c, name) {
					name += " (now " + c + ")"
				}
				if !slices.Contains(registered, name) {
					registered = append(registered, name)
				}
			}
		}
		if len(registered) == 0 {
			registered = []string{"none"}
		}
		return "", fmt.Errorf("no registered AO project matches %s (registered: %s); add the checkout with `ao project add`", strings.Join(looked, " or "), strings.Join(registered, ", "))
	}
}

// RepoSlug is the lowercase owner/repo of a GitHub repository URL (https, ssh or git@), or "" for anything else.
func RepoSlug(url string) string {
	id, err := domain.ParseRepositoryIdentity(url)
	if err != nil || id.Provider != "github" {
		return ""
	}
	return strings.ToLower(id.Namespace + "/" + id.Name)
}
