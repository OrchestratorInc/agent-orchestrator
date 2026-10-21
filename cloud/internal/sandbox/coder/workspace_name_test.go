package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

const storedNameSessionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"

// workspaceNameServer is a Coder fake holding at most one workspace, served by
// name lookup, by id and on create, recording every name AO asked for.
type workspaceNameServer struct {
	mu        sync.Mutex
	existing  *workspace
	requested []string
	// clockOffset is how far the fake Coder server's clock is off; it sends
	// that clock in its Date header.
	clockOffset time.Duration
}

func (f *workspaceNameServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if f.clockOffset != 0 {
			writer.Header().Set("Date", time.Now().Add(f.clockOffset).UTC().Format(http.TimeFormat))
		}
		const lookup = "/api/v2/users/planned-owner/workspace/"
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v2/users/planned-owner/workspaces":
			var body createWorkspaceRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create: %v", err)
			}
			f.requested = append(f.requested, "create:"+body.Name)
			if f.existing != nil && f.existing.Name == body.Name {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(`{"message":"Workspace already exists."}`))
				return
			}
			view := healthyWorkspace()
			view.Name, view.OwnerName, view.TemplateID, view.CreatedAt = body.Name, "planned-owner", body.TemplateID, time.Now()
			f.existing = &view
			_ = json.NewEncoder(writer).Encode(view)
		case request.Method == http.MethodGet && len(request.URL.Path) > len(lookup) && request.URL.Path[:len(lookup)] == lookup:
			name := request.URL.Path[len(lookup):]
			f.requested = append(f.requested, "find:"+name)
			if f.existing == nil || f.existing.Name != name {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(writer).Encode(f.existing)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID:
			if f.existing == nil {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(writer).Encode(f.existing)
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func storedNameProvider(t *testing.T, baseURL, coderProfile string) sandbox.Provider {
	t.Helper()
	base := newTestClient(t, baseURL, nil)
	provider, err := base.ForSandbox(domain.Sandbox{
		SessionID: storedNameSessionID,
		ResourceProfile: json.RawMessage(fmt.Sprintf(
			`{"coder":{"baseUrl":%q,"owner":"planned-owner","templateId":%q,"durableRoot":"$HOME"%s}}`,
			baseURL, testTemplateID, coderProfile)),
	})
	if err != nil {
		t.Fatalf("scope client: %v", err)
	}
	return provider
}

// A session created with a stored name creates, finds and re-reads its
// workspace under that name, never the computed one.
func TestScopedClientUsesStoredWorkspaceName(t *testing.T) {
	t.Parallel()
	fake := &workspaceNameServer{}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	issuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	provider := storedNameProvider(t, server.URL,
		`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f","workspaceNameIssuedAt":"`+issuedAt+`"`)

	ctx := context.Background()
	if _, found, err := provider.FindBySession(ctx, storedNameSessionID); err != nil || found {
		t.Fatalf("find before create = %v, %v; want absent", found, err)
	}
	environment, err := provider.Create(ctx, sandbox.Spec{SessionID: storedNameSessionID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, found, err := provider.FindBySession(ctx, storedNameSessionID); err != nil || !found {
		t.Fatalf("find after create = %v, %v; want the created workspace", found, err)
	}
	if _, err := provider.Get(ctx, environment.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	want := []string{"find:acme-w-fix-1a2b3c4d5e6f", "create:acme-w-fix-1a2b3c4d5e6f", "find:acme-w-fix-1a2b3c4d5e6f"}
	if fmt.Sprint(fake.requested) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", fake.requested, want)
	}
	// The client stays bound to its own session: another id is a mismatch.
	if _, err := provider.Create(ctx, sandbox.Spec{SessionID: "9f8e7d6c-5b4a-4321-8fed-cba987654321"}); err == nil {
		t.Fatal("create for another session id succeeded")
	}
}

// A session created before names were stored keeps the computed name, so its
// existing workspace is still found and validated.
func TestScopedClientFallsBackToComputedWorkspaceName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, profile, want string }{
		{"prefixed", `,"workspaceNamePrefix":"elevenx"`, WorkspaceNameWithPrefix("elevenx", storedNameSessionID)},
		{"default", ``, WorkspaceName(storedNameSessionID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			legacy := healthyWorkspace()
			legacy.Name, legacy.OwnerName = tc.want, "planned-owner"
			legacy.CreatedAt = time.Now().Add(-90 * 24 * time.Hour)
			fake := &workspaceNameServer{existing: &legacy}
			server := httptest.NewServer(fake.handler(t))
			defer server.Close()
			provider := storedNameProvider(t, server.URL, tc.profile)

			ctx := context.Background()
			environment, found, err := provider.FindBySession(ctx, storedNameSessionID)
			if err != nil || !found {
				t.Fatalf("find legacy workspace = %v, %v; want found", found, err)
			}
			if _, err := provider.Get(ctx, environment.ID); err != nil {
				t.Fatalf("get legacy workspace: %v", err)
			}
			if fmt.Sprint(fake.requested) != fmt.Sprint([]string{"find:" + tc.want}) {
				t.Fatalf("requests = %v, want a lookup of %s", fake.requested, tc.want)
			}
		})
	}
}

// A workspace that held the stored name well before the session chose it is
// another session's (an id collision): it is reported absent so AO neither
// adopts nor deletes it, and the create then fails on Coder's conflict.
func TestStoredWorkspaceNameNeverAdoptsAnOlderWorkspace(t *testing.T) {
	t.Parallel()
	issuedAt := time.Now().UTC()
	for _, tc := range []struct {
		name      string
		createdAt time.Time
		adopt     bool
	}{
		{"created long before the name was chosen", issuedAt.Add(-time.Hour), false},
		{"created within clock skew", issuedAt.Add(-time.Minute), true},
		{"created after", issuedAt.Add(time.Minute), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			other := healthyWorkspace()
			other.Name, other.OwnerName, other.CreatedAt = "acme-w-fix-1a2b3c4d5e6f", "planned-owner", tc.createdAt
			fake := &workspaceNameServer{existing: &other}
			server := httptest.NewServer(fake.handler(t))
			defer server.Close()
			provider := storedNameProvider(t, server.URL,
				`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f","workspaceNameIssuedAt":"`+issuedAt.Format(time.RFC3339Nano)+`"`)

			ctx := context.Background()
			_, found, err := provider.FindBySession(ctx, storedNameSessionID)
			if err != nil || found != tc.adopt {
				t.Fatalf("find = %v, %v; want found=%v", found, err, tc.adopt)
			}
			if !tc.adopt {
				if _, err := provider.Create(ctx, sandbox.Spec{SessionID: storedNameSessionID}); err == nil {
					t.Fatal("create over another session's workspace succeeded")
				}
			}
		})
	}
}

// A bring-your-own Coder whose clock runs well behind the control plane's
// reports its own workspace's created_at minutes "before" the session chose
// the name; read by the Coder server's own clock (its Date header) it is the
// session's workspace and is adopted. A workspace that really predates the
// name is still refused, and so is one on a Coder whose clock runs ahead.
func TestStoredWorkspaceNameReadsCreatedAtByCodersClock(t *testing.T) {
	t.Parallel()
	issuedAt := time.Now().UTC().Add(-time.Minute)
	for _, tc := range []struct {
		name   string
		offset time.Duration
		// createdAfterIssue is when the workspace was created, by the control
		// plane's clock, relative to the name's issue time.
		createdAfterIssue time.Duration
		adopt             bool
	}{
		{"coder 30 minutes behind, its own workspace", -30 * time.Minute, 20 * time.Second, true},
		{"coder 30 minutes behind, an older workspace", -30 * time.Minute, -time.Hour, false},
		{"coder 30 minutes ahead, an older workspace", 30 * time.Minute, -time.Hour, false},
		{"coder 30 minutes ahead, its own workspace", 30 * time.Minute, 20 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			own := healthyWorkspace()
			// created_at as the Coder server's clock records it.
			own.Name, own.OwnerName, own.CreatedAt = "acme-w-fix-1a2b3c4d5e6f", "planned-owner", issuedAt.Add(tc.createdAfterIssue).Add(tc.offset)
			fake := &workspaceNameServer{existing: &own, clockOffset: tc.offset}
			server := httptest.NewServer(fake.handler(t))
			defer server.Close()
			provider := storedNameProvider(t, server.URL,
				`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f","workspaceNameIssuedAt":"`+issuedAt.Format(time.RFC3339Nano)+`"`)
			_, found, err := provider.FindBySession(context.Background(), storedNameSessionID)
			if err != nil || found != tc.adopt {
				t.Fatalf("find = %v, %v; want found=%v", found, err, tc.adopt)
			}
		})
	}
}

func TestSessionWorkspaceNameAndURL(t *testing.T) {
	t.Parallel()
	if got := SessionWorkspaceName(" acme-o-1a2b3c4d5e6f ", "acme", storedNameSessionID); got != "acme-o-1a2b3c4d5e6f" {
		t.Fatalf("stored name = %q", got)
	}
	if got := SessionWorkspaceName("", "acme", storedNameSessionID); got != WorkspaceNameWithPrefix("acme", storedNameSessionID) {
		t.Fatalf("legacy prefixed name = %q", got)
	}
	if got := SessionWorkspaceName("", "", storedNameSessionID); got != WorkspaceName(storedNameSessionID) {
		t.Fatalf("legacy default name = %q", got)
	}
	if got := WorkspaceURL("https://coder.example.com/", "dev", "acme-o-1a2b3c4d5e6f"); got != "https://coder.example.com/@dev/acme-o-1a2b3c4d5e6f" {
		t.Fatalf("workspace URL = %q", got)
	}
	for _, args := range [][3]string{
		{"https://coder.example.com", "", "acme-o-1a2b3c4d5e6f"},
		{"https://coder.example.com", "dev", ""},
		{"ftp://coder.example.com", "dev", "acme-o-1a2b3c4d5e6f"},
	} {
		if got := WorkspaceURL(args[0], args[1], args[2]); got != "" {
			t.Errorf("WorkspaceURL%v = %q, want empty", args, got)
		}
	}
}

// A create refused because another workspace holds the stored name reports
// the session's retry name, and never adopts the other workspace.
func TestCreateReportsTheRetryNameWhenTheNameIsTaken(t *testing.T) {
	t.Parallel()
	issuedAt := time.Now().UTC()
	other := healthyWorkspace()
	other.Name, other.OwnerName, other.CreatedAt = "acme-w-fix-1a2b3c4d5e6f", "planned-owner", issuedAt.Add(-24*time.Hour)
	fake := &workspaceNameServer{existing: &other}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	provider := storedNameProvider(t, server.URL,
		`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f","workspaceNameRetry":"acme-w-fix-1a2b3c4d5e6f4a7b8c9d0","workspaceNameIssuedAt":"`+issuedAt.Format(time.RFC3339Nano)+`"`)

	ctx := context.Background()
	if _, found, err := provider.FindBySession(ctx, storedNameSessionID); err != nil || found {
		t.Fatalf("find = %v, %v; want the older workspace reported absent", found, err)
	}
	_, err := provider.Create(ctx, sandbox.Spec{SessionID: storedNameSessionID})
	var taken *sandbox.WorkspaceNameTakenError
	if !errors.As(err, &taken) || taken.Name != "acme-w-fix-1a2b3c4d5e6f" || taken.Retry != "acme-w-fix-1a2b3c4d5e6f4a7b8c9d0" {
		t.Fatalf("create err = %v, want a name-taken error naming the retry", err)
	}

	// Once the retry name is stored (and the retry used up), the next create
	// uses it.
	retried := storedNameProvider(t, server.URL,
		`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f4a7b8c9d0","workspaceNameIssuedAt":"`+issuedAt.Format(time.RFC3339Nano)+`"`)
	if _, found, err := retried.FindBySession(ctx, storedNameSessionID); err != nil || found {
		t.Fatalf("find retry name = %v, %v; want absent", found, err)
	}
	if _, err := retried.Create(ctx, sandbox.Spec{SessionID: storedNameSessionID}); err != nil {
		t.Fatalf("create under the retry name: %v", err)
	}
	if fake.existing.Name != "acme-w-fix-1a2b3c4d5e6f4a7b8c9d0" {
		t.Fatalf("created %q, want the retry name", fake.existing.Name)
	}
}

// With no retry left (or for a session whose name is computed), a name
// conflict is an ordinary create failure.
func TestCreateConflictWithoutARetryNameFails(t *testing.T) {
	t.Parallel()
	issuedAt := time.Now().UTC()
	other := healthyWorkspace()
	other.Name, other.OwnerName, other.CreatedAt = "acme-w-fix-1a2b3c4d5e6f4a7b8c9d0", "planned-owner", issuedAt.Add(-24*time.Hour)
	fake := &workspaceNameServer{existing: &other}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	provider := storedNameProvider(t, server.URL,
		`,"workspaceNamePrefix":"acme","workspaceName":"acme-w-fix-1a2b3c4d5e6f4a7b8c9d0","workspaceNameIssuedAt":"`+issuedAt.Format(time.RFC3339Nano)+`"`)
	_, err := provider.Create(context.Background(), sandbox.Spec{SessionID: storedNameSessionID})
	var taken *sandbox.WorkspaceNameTakenError
	if err == nil || errors.As(err, &taken) || !isConflict(err) {
		t.Fatalf("create err = %v, want a plain conflict", err)
	}
}

// A legacy <prefix>-<12> workspace can spell the same name as a new session's
// readable name (a legacy prefix ending in "-o" with the same 12 id
// characters). The new session looks up only its stored name, sees the older
// workspace as another session's, and moves to its retry name; the legacy
// session still finds its own workspace by its computed name. (Both sessions
// use one id here; only its first 12 characters matter to the names.)
func TestReadableNamesAreNotConfusedWithLegacyNames(t *testing.T) {
	t.Parallel()
	legacyName := WorkspaceNameWithPrefix("acme-o", storedNameSessionID)
	if legacyName != "acme-o-1a2b3c4d5e6f" {
		t.Fatalf("legacy name = %q; the scenario below is stale", legacyName)
	}
	legacy := healthyWorkspace()
	legacy.Name, legacy.OwnerName, legacy.CreatedAt = legacyName, "planned-owner", time.Now().Add(-30*24*time.Hour)
	fake := &workspaceNameServer{existing: &legacy}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	ctx := context.Background()

	legacySession := storedNameProvider(t, server.URL, `,"workspaceNamePrefix":"acme-o"`)
	if _, found, err := legacySession.FindBySession(ctx, storedNameSessionID); err != nil || !found {
		t.Fatalf("legacy session find = %v, %v; want its own workspace", found, err)
	}

	issuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	newSession := storedNameProvider(t, server.URL,
		`,"workspaceNamePrefix":"acme","workspaceName":"acme-o-1a2b3c4d5e6f","workspaceNameRetry":"acme-o-1a2b3c4d5e6f4a7b8c9d0e1f2","workspaceNameIssuedAt":"`+issuedAt+`"`)
	if _, found, err := newSession.FindBySession(ctx, storedNameSessionID); err != nil || found {
		t.Fatalf("new session find = %v, %v; want the legacy workspace reported absent", found, err)
	}
	var taken *sandbox.WorkspaceNameTakenError
	if _, err := newSession.Create(ctx, sandbox.Spec{SessionID: storedNameSessionID}); !errors.As(err, &taken) {
		t.Fatalf("new session create err = %v, want a name-taken error", err)
	}
}
