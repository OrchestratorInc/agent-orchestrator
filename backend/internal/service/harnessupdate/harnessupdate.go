// Package harnessupdate answers "what is the latest published version of
// this harness?" for the install methods that have a public, unauthenticated
// registry: npm, Homebrew (core formulae/casks only), and PyPI. It never
// blocks a caller on a network round trip: a lookup returns whatever is
// cached, and — when that answer is missing or stale — kicks off a bounded
// background refresh so the *next* lookup is current.
package harnessupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a cached latest-version answer is trusted before a
// lookup triggers a background refresh. Registries publish new releases at
// most a few times a day; there is no reason to ask more often than this.
const DefaultTTL = 6 * time.Hour

// defaultRequestTimeout bounds a single upstream registry request so a slow
// or hanging registry can never pin a background goroutine open.
const defaultRequestTimeout = 10 * time.Second

// Latest is one cached answer to "what is the newest published version?".
type Latest struct {
	Version   string
	CheckedAt time.Time
}

// Record is the durable form of Latest, keyed by registry+name.
type Record struct {
	Registry  string
	Name      string
	Version   string
	CheckedAt time.Time
}

// Store persists the latest-known version per registry+name so a restart
// does not lose every cached answer and force a synchronous re-fetch.
type Store interface {
	GetHarnessLatestVersion(ctx context.Context, registry, name string) (Record, bool, error)
	UpsertHarnessLatestVersion(ctx context.Context, record Record) error
}

// httpDoer is satisfied by *http.Client; accepting the interface keeps tests
// free of a real network dependency.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Service resolves and caches latest-version answers per registry+name.
type Service struct {
	http  httpDoer
	store Store
	ttl   time.Duration
	now   func() time.Time

	// registryEndpoint builds the URL to GET for registry+name. Overridden in
	// tests to point at an httptest.Server instead of the real registries.
	registryEndpoint func(registry, name string) (string, error)

	mu       sync.Mutex
	inFlight map[string]bool
}

// New builds a Service. store is required. A nil client defaults to a
// bounded *http.Client shared across every default-constructed Service.
func New(client httpDoer, store Store) *Service {
	if client == nil {
		client = &defaultClient
	}
	return &Service{
		http: client, store: store, ttl: DefaultTTL, now: time.Now,
		registryEndpoint: registryEndpoint,
		inFlight:         make(map[string]bool),
	}
}

// defaultClient is the shared client used when the caller does not supply
// one, so every default-constructed Service reuses one connection pool.
var defaultClient = http.Client{Timeout: defaultRequestTimeout}

// Latest returns the cached latest-version answer for registry+name, if any.
// ok is false when nothing has ever been cached — either the cache is simply
// still empty, or registry is one AO has no queryable source for. A stale or
// missing cache triggers a non-blocking background refresh; call again later
// to observe its result. Callers should pass exactly what
// Plan.PackageRegistry/PackageName carry, and skip the call entirely when
// PackageRegistry is empty (no queryable source for that install method).
func (s *Service) Latest(ctx context.Context, registry, name string) (Latest, bool) {
	if registry == "" || name == "" || s.store == nil {
		return Latest{}, false
	}
	record, found, err := s.store.GetHarnessLatestVersion(ctx, registry, name)
	fresh := found && err == nil && s.now().Sub(record.CheckedAt) < s.ttl
	if !fresh {
		s.refreshInBackground(registry, name)
	}
	if !found || err != nil {
		return Latest{}, false
	}
	return Latest{Version: record.Version, CheckedAt: record.CheckedAt}, true
}

// refreshInBackground starts at most one in-flight fetch per registry+name so
// a burst of catalog reads (Settings polling every harness) does not fan out
// into a burst of identical outbound requests.
func (s *Service) refreshInBackground(registry, name string) {
	key := registry + "\x00" + name
	s.mu.Lock()
	if s.inFlight[key] {
		s.mu.Unlock()
		return
	}
	s.inFlight[key] = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inFlight, key)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
		defer cancel()
		version, err := s.fetch(ctx, registry, name)
		if err != nil {
			return
		}
		_ = s.store.UpsertHarnessLatestVersion(ctx, Record{
			Registry: registry, Name: name, Version: version, CheckedAt: s.now(),
		})
	}()
}

// fetch resolves registry+name's endpoint, requests it, and parses the
// response into a version string.
func (s *Service) fetch(ctx context.Context, registry, name string) (string, error) {
	endpoint, err := s.registryEndpoint(registry, name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-orchestrator (harness update check)")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("harnessupdate: %s returned %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	version, err := parseVersion(registry, body)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("harnessupdate: %s did not report a version", endpoint)
	}
	return version, nil
}

// registryEndpoint is the real, production URL builder for each recognized
// registry. An unrecognized registry is an error, not a panic —
// Plan.PackageRegistry is server-owned but this keeps the function total.
func registryEndpoint(registry, name string) (string, error) {
	escaped := url.PathEscape(name)
	switch registry {
	case "npm":
		return "https://registry.npmjs.org/" + escaped + "/latest", nil
	case "pypi":
		return "https://pypi.org/pypi/" + escaped + "/json", nil
	case "homebrew-formula":
		return "https://formulae.brew.sh/api/formula/" + escaped + ".json", nil
	case "homebrew-cask":
		return "https://formulae.brew.sh/api/cask/" + escaped + ".json", nil
	case "github-release":
		owner, repo, ok := strings.Cut(name, "/")
		if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
			return "", fmt.Errorf("harnessupdate: github-release name %q is not owner/repo", name)
		}
		return "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest", nil
	default:
		return "", fmt.Errorf("harnessupdate: unrecognized registry %q", registry)
	}
}

// parseVersion extracts the published version from registry's response
// shape. Each registry reports it at a different JSON path.
func parseVersion(registry string, body []byte) (string, error) {
	switch registry {
	case "npm":
		var payload struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		return payload.Version, nil
	case "pypi":
		var payload struct {
			Info struct {
				Version string `json:"version"`
			} `json:"info"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		return payload.Info.Version, nil
	case "homebrew-formula":
		var payload struct {
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		return payload.Versions.Stable, nil
	case "homebrew-cask":
		var payload struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		return payload.Version, nil
	case "github-release":
		var payload struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		return strings.TrimPrefix(payload.TagName, "v"), nil
	default:
		return "", fmt.Errorf("harnessupdate: unrecognized registry %q", registry)
	}
}
