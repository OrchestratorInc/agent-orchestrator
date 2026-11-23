package access

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func mustGrant(t *testing.T, preset string, groups []Group, projects []string, expires time.Time) Grant {
	t.Helper()
	grant, err := NewGrant(preset, groups, projects, expires)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}

func mustAction(t *testing.T, id string) Action {
	t.Helper()
	action, ok := ActionByID(id)
	if !ok {
		t.Fatalf("no action %s", id)
	}
	return action
}

func TestPresetsNest(t *testing.T) {
	read, _ := PresetGroups(PresetRead)
	operate, _ := PresetGroups(PresetOperate)
	manage, _ := PresetGroups(PresetManage)
	for _, group := range read {
		if !contains(operate, group) {
			t.Errorf("operate lacks read group %s", group)
		}
	}
	for _, group := range operate {
		if !contains(manage, group) {
			t.Errorf("manage lacks operate group %s", group)
		}
	}
	if len(manage) != len(Groups()) {
		t.Errorf("manage has %d groups, want every group (%d)", len(manage), len(Groups()))
	}
}

func contains(groups []Group, want Group) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}

func TestCatalogIsConsistent(t *testing.T) {
	ids, routes := map[string]bool{}, map[string]bool{}
	for _, action := range Catalog() {
		if ids[action.ID] {
			t.Errorf("duplicate action ID %s", action.ID)
		}
		ids[action.ID] = true
		route := action.Method + " " + action.Path
		if routes[route] {
			t.Errorf("route %s is in the catalog twice", route)
		}
		routes[route] = true
		if !contains(Groups(), action.Group) {
			t.Errorf("%s has unknown group %s", action.ID, action.Group)
		}
		needsKey := action.Project != ProjectNone && action.Project != ProjectAll
		if needsKey != (action.Key != "") {
			t.Errorf("%s: project source %d and key %q do not agree", action.ID, action.Project, action.Key)
		}
		if (action.Project == ProjectParam || action.Project == ProjectSession) && !strings.Contains(action.Path, "{"+action.Key+"}") {
			t.Errorf("%s: path %s has no {%s}", action.ID, action.Path, action.Key)
		}
		got, ok := ActionForRoute(action.Method, action.Path)
		if !ok || got.ID != action.ID {
			t.Errorf("ActionForRoute(%s) = %v, %v", route, got.ID, ok)
		}
	}
}

func TestNewGrantRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		preset string
		groups []Group
	}{
		"neither":        {},
		"both":           {preset: PresetRead, groups: []Group{SessionsRead}},
		"unknown preset": {preset: "admin"},
		"unknown group":  {groups: []Group{"sessions.everything"}},
	} {
		if _, err := NewGrant(tc.preset, tc.groups, nil, time.Time{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestGrantCheck(t *testing.T) {
	read := mustGrant(t, PresetRead, nil, nil, time.Time{})
	operateAPI := mustGrant(t, PresetOperate, nil, []string{"api"}, time.Time{})
	expired := mustGrant(t, PresetManage, nil, nil, testNow.Add(-time.Minute))
	custom := mustGrant(t, "", []Group{ChatRespond}, nil, time.Time{})

	for name, tc := range map[string]struct {
		grant   Grant
		action  string
		project string
		known   bool
		want    string
	}{
		"read may list":                     {grant: read, action: "session.list"},
		"read may not spawn":                {grant: read, action: "session.spawn", project: "api", known: true, want: ReasonGroup},
		"operate in its project":            {grant: operateAPI, action: "session.spawn", project: "api", known: true},
		"operate in another project":        {grant: operateAPI, action: "session.spawn", project: "web", known: true, want: ReasonProject},
		"limited grant must name a project": {grant: operateAPI, action: "session.list", want: ReasonProject},
		"limited grant on every project":    {grant: operateAPI, action: "project.list", want: ReasonProject},
		"limited grant on machine data":     {grant: operateAPI, action: "settings.get"},
		"operate may not remove a project":  {grant: operateAPI, action: "project.remove", project: "api", known: true, want: ReasonGroup},
		"expired":                           {grant: expired, action: "session.list", want: ReasonEnded},
		"custom group only":                 {grant: custom, action: "chat.input.resolve", project: "api", known: true},
		"custom group lacks reads":          {grant: custom, action: "session.get", project: "api", known: true, want: ReasonGroup},
	} {
		refusal := tc.grant.Check(mustAction(t, tc.action), tc.project, tc.known, testNow)
		got := ""
		if refusal != nil {
			got = refusal.Reason
			if refusal.Message == "" {
				t.Errorf("%s: refusal has no message", name)
			}
		}
		if got != tc.want {
			t.Errorf("%s: reason %q, want %q", name, got, tc.want)
		}
	}
}

func TestRegistryLifecycle(t *testing.T) {
	path := TokensPath(t.TempDir())
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := mustGrant(t, PresetOperate, nil, []string{"api"}, testNow.Add(time.Hour))
	token, secret, err := registry.Create(" grok-bot ", grant, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if token.Name != "grok-bot" || !strings.HasPrefix(secret, TokenPrefix) {
		t.Fatalf("token %+v secret %q", token, secret)
	}
	if _, _, err := registry.Create("GROK-BOT", grant, testNow); err == nil {
		t.Error("a second token with the same name must be refused")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Error("the secret is on disk")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}

	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Match(secret, testNow); !ok || got.ID != token.ID {
		t.Fatalf("Match after reload = %v, %v", got.ID, ok)
	}
	if _, ok := reloaded.Match(secret, testNow.Add(2*time.Hour)); ok {
		t.Error("an expired token must not match")
	}
	if _, ok := reloaded.Match(TokenPrefix+"00", testNow); ok {
		t.Error("an unknown secret matched")
	}

	if err := reloaded.Bind("s-1", token.ID); err != nil {
		t.Fatal(err)
	}
	if _, bound := reloaded.ForSession("s-2"); bound {
		t.Error("a session that no token started must not be bound")
	}
	revoked, found, err := reloaded.Revoke("grok-bot")
	if err != nil || !found || revoked.ID != token.ID {
		t.Fatalf("Revoke = %+v, %v, %v", revoked, found, err)
	}
	if _, ok := reloaded.Match(secret, testNow); ok {
		t.Error("a revoked token matched")
	}
	if len(reloaded.List()) != 0 {
		t.Errorf("want no tokens left, got %+v", reloaded.List())
	}
	if _, found, _ := reloaded.Revoke(token.ID); found {
		t.Error("revoking twice must report not found")
	}
	if _, _, err := reloaded.Create("grok-bot", grant, testNow); err != nil {
		t.Errorf("the name of a revoked token must be free again: %v", err)
	}
	again, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	tokenID, bound := again.ForSession("s-1")
	if _, known := again.Get(tokenID); !bound || tokenID != token.ID || known {
		t.Errorf("the session must stay bound to the revoked token after a reload: %q %v %v", tokenID, bound, known)
	}
}

func TestBindingsOutliveTheTokenFile(t *testing.T) {
	path := TokensPath(t.TempDir())
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := registry.Create("ci", mustGrant(t, PresetRead, nil, nil, time.Time{}), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Bind("api-1", token.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Bind("gone/../1", "tok_gone"); err != nil {
		t.Fatalf("a binding must not need its token, or a plain session id: %v", err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	damaged, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("want an error for a damaged token file")
	}
	for session, want := range map[string]string{"api-1": token.ID, "gone/../1": "tok_gone"} {
		if tokenID, bound := damaged.ForSession(session); !bound || tokenID != want {
			t.Errorf("%s after the token file was damaged: %q %v, want it bound to %s", session, tokenID, bound, want)
		}
	}
	if _, known := damaged.Get(token.ID); known {
		t.Error("the damaged file must not yield tokens")
	}

	sessions := filepath.Join(filepath.Dir(path), "sessions")
	if err := os.RemoveAll(sessions); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessions, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	lost, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("want an error when the bindings cannot be read")
	}
	if _, bound := lost.ForSession("never-bound"); !bound {
		t.Error("with unreadable bindings every session must count as bound")
	}
	if err := lost.Bind("api-2", token.ID); err == nil {
		t.Error("Bind must report a write that failed")
	}

	if err := os.Remove(sessions); err != nil {
		t.Fatal(err)
	}
	other, _ := LoadRegistry(path)
	for _, session := range []string{"api-1", "api-3"} {
		if err := other.Bind(session, token.ID); err != nil {
			t.Fatal(err)
		}
	}
	if tokenID, bound := lost.ForSession("api-1"); !bound || tokenID != token.ID {
		t.Errorf("a binding that can be read again: %q %v, want it bound to %s", tokenID, bound, token.ID)
	}
	if _, bound := lost.ForSession("never-bound"); bound {
		t.Error("once the bindings can be read again, a session with none must not count as bound")
	}
	if err := lost.SessionCreated(context.Background(), "api-3"); err != nil {
		t.Fatal(err)
	}
	if _, bound := lost.ForSession("api-3"); bound {
		t.Error("a new session of a caller with no grant must lose a leftover binding the registry had not read")
	}
}

func TestSessionCreatedBindsOrClears(t *testing.T) {
	path := TokensPath(t.TempDir())
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := mustGrant(t, PresetOperate, nil, []string{"api"}, time.Time{})
	token, _, err := registry.Create("ci", grant, testNow)
	if err != nil {
		t.Fatal(err)
	}
	granted := WithCaller(context.Background(), Caller{Kind: CallerToken, Grant: &grant, TokenID: token.ID})

	if err := registry.SessionCreated(context.Background(), "api-1"); err != nil {
		t.Fatal(err)
	}
	if _, bound := registry.ForSession("api-1"); bound {
		t.Error("a session created by a caller with no grant must not be bound")
	}
	if err := registry.SessionCreated(granted, "api-2"); err != nil {
		t.Fatal(err)
	}
	if tokenID, bound := registry.ForSession("api-2"); !bound || tokenID != token.ID {
		t.Fatalf("a session created by a caller with a grant: %q %v, want it bound to %s", tokenID, bound, token.ID)
	}

	if err := registry.SessionCreated(context.Background(), "api-2"); err != nil {
		t.Fatal(err)
	}
	if _, bound := registry.ForSession("api-2"); bound {
		t.Error("a new session that reuses the ID of a deleted bound session must start with no binding")
	}
	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, bound := reloaded.ForSession("api-2"); bound {
		t.Error("the cleared binding came back after a reload")
	}
}

func TestRevokePrefersAnExactID(t *testing.T) {
	registry, err := LoadRegistry(TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant := mustGrant(t, PresetRead, nil, nil, time.Time{})
	first, _, err := registry.Create("first", grant, testNow)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		registry, err = LoadRegistry(registry.path)
		if err != nil {
			t.Fatal(err)
		}
		impostor, _, err := registry.Create(first.ID, grant, testNow)
		if err != nil {
			t.Fatal(err)
		}
		revoked, found, err := registry.Revoke(first.ID)
		if err != nil || !found || revoked.ID != first.ID {
			t.Fatalf("round %d: Revoke(%s) removed %s (%q), want the token with that ID", i, first.ID, revoked.ID, revoked.Name)
		}
		if revoked, found, err = registry.Revoke(first.ID); err != nil || !found || revoked.ID != impostor.ID {
			t.Fatalf("round %d: the name must still revoke once no token has that ID: %s %v %v", i, revoked.ID, found, err)
		}
		if first, _, err = registry.Create("first", grant, testNow); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadRegistryMovesDamagedFileAside(t *testing.T) {
	path := TokensPath(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("want an error for a damaged file")
	}
	if _, statErr := os.Stat(path + ".bad"); statErr != nil {
		t.Errorf("the damaged file was not moved aside: %v", statErr)
	}
	if _, _, err := registry.Create("ci", mustGrant(t, PresetRead, nil, nil, time.Time{}), testNow); err != nil {
		t.Errorf("the registry must work after a damaged file is moved aside: %v", err)
	}
}

func TestLoadRegistryKeepsAFileItCannotRead(t *testing.T) {
	path := TokensPath(t.TempDir())
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("want an error for a file that cannot be read")
	}
	if _, statErr := os.Stat(path + ".bad"); statErr == nil {
		t.Error("a file that could not be read was moved aside")
	}
	if _, _, err := registry.Create("ci", mustGrant(t, PresetRead, nil, nil, time.Time{}), testNow); err == nil {
		t.Error("the registry must refuse writes over a file it could not read")
	}
}

func TestCatalogLeavesOutAutomationWrites(t *testing.T) {
	for _, route := range [][2]string{
		{http.MethodPost, "/api/v1/automations"},
		{http.MethodPatch, "/api/v1/automations/{automationId}"},
	} {
		if action, ok := ActionForRoute(route[0], route[1]); ok {
			t.Errorf("%s is in the catalog, but the sessions an automation starts carry no grant", action.ID)
		}
	}
}

func TestCreateCountsNameCharactersAndMarksAFailedSave(t *testing.T) {
	path := TokensPath(t.TempDir())
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := mustGrant(t, PresetRead, nil, nil, time.Time{})
	if _, _, err := registry.Create(strings.Repeat("बॉट", 20), grant, testNow); err != nil {
		t.Errorf("a 60-character name in a non-Latin script: %v", err)
	}
	if _, _, err := registry.Create(strings.Repeat("ब", 65), grant, testNow); err == nil || errors.Is(err, ErrNotSaved) {
		t.Errorf("a 65-character name must be refused as bad input: %v", err)
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Create("ci", grant, testNow); !errors.Is(err, ErrNotSaved) {
		t.Errorf("a token that cannot be written: %v, want ErrNotSaved", err)
	}
}
