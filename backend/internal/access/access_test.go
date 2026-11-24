package access

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func mustGrant(t *testing.T, level string, projects []string, expires time.Time) Grant {
	t.Helper()
	grant, err := NewGrant(level, projects, expires)
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

func TestLevelsNest(t *testing.T) {
	for _, action := range Catalog() {
		need := slices.Index(Levels(), action.Level)
		for have, level := range Levels() {
			if refused := (Grant{Level: level}).Check(action, "", false) != nil; refused != (have < need) {
				t.Errorf("%s needs %s: a %s grant refused=%v", action.ID, action.Level, level, refused)
			}
		}
		if (Grant{Level: "admin"}).Check(action, "", false) == nil {
			t.Errorf("a grant with an unknown level reached %s", action.ID)
		}
	}
	if (Grant{Level: LevelManage}).Check(Action{ID: "x", Level: "root"}, "", false) == nil {
		t.Error("an action with an unknown level must be refused, not open to every grant")
	}
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
		if !slices.Contains(Levels(), action.Level) {
			t.Errorf("%s has unknown level %q", action.ID, action.Level)
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
	for _, level := range []string{"", "admin", "Read"} {
		if _, err := NewGrant(level, nil, time.Time{}); err == nil {
			t.Errorf("level %q: want an error", level)
		}
	}
	for _, projects := range [][]string{{""}, {"api", " "}} {
		if _, err := NewGrant(LevelOperate, projects, time.Time{}); err == nil {
			t.Errorf("projects %q: a blank project must be refused, not dropped: it would lift the limit", projects)
		}
	}
}

func TestGrantCheck(t *testing.T) {
	read := mustGrant(t, LevelRead, nil, time.Time{})
	operateAPI := mustGrant(t, LevelOperate, []string{"api"}, time.Time{})
	manageAPI := mustGrant(t, LevelManage, []string{"api"}, time.Time{})
	manage := mustGrant(t, LevelManage, nil, time.Time{})

	for name, tc := range map[string]struct {
		grant   Grant
		action  string
		project string
		known   bool
		want    string
	}{
		"read may list":                      {grant: read, action: "session.list"},
		"read may not spawn":                 {grant: read, action: "session.spawn", project: "api", known: true, want: ReasonLevel},
		"operate in its project":             {grant: operateAPI, action: "session.spawn", project: "api", known: true},
		"operate in another project":         {grant: operateAPI, action: "session.spawn", project: "web", known: true, want: ReasonProject},
		"limited grant must name a project":  {grant: operateAPI, action: "session.list", want: ReasonProject},
		"limited grant on every project":     {grant: operateAPI, action: "project.list", want: ReasonProject},
		"limited grant on machine data":      {grant: operateAPI, action: "settings.get"},
		"limited grant may refresh agents":   {grant: operateAPI, action: "agent.refresh"},
		"limited grant on machine settings":  {grant: manageAPI, action: "settings.session-retention", want: ReasonProject},
		"limited grant on default account":   {grant: manageAPI, action: "account.set-default", want: ReasonProject},
		"unlimited manage on settings":       {grant: manage, action: "settings.session-retention"},
		"operate may not remove a project":   {grant: operateAPI, action: "project.remove", project: "api", known: true, want: ReasonLevel},
		"operate may start an orchestrator":  {grant: operateAPI, action: "orchestrator.spawn", project: "api", known: true},
		"operate may not make an automation": {grant: operateAPI, action: "automation.create", want: ReasonLevel},
		"manage may make an automation":      {grant: manage, action: "automation.create"},
	} {
		refusal := tc.grant.Check(mustAction(t, tc.action), tc.project, tc.known)
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
	grant := mustGrant(t, LevelOperate, []string{"api"}, testNow.Add(time.Hour))
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
	renewed, _, err := reloaded.Create("grok-bot", grant, testNow)
	if err != nil {
		t.Fatalf("the name of a revoked token must be free again: %v", err)
	}
	if revoked, found, err := reloaded.Revoke(renewed.ID); err != nil || !found || revoked.Name != "grok-bot" {
		t.Errorf("Revoke by id = %+v, %v, %v", revoked, found, err)
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
	if _, _, err := registry.Create("ci", mustGrant(t, LevelRead, nil, time.Time{}), testNow); err != nil {
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
	if registry.Unreadable() == nil {
		t.Error("the registry must say that its token file could not be read, so a list is not shown as empty")
	}
	if _, _, err := registry.Create("ci", mustGrant(t, LevelRead, nil, time.Time{}), testNow); err == nil {
		t.Error("the registry must refuse writes over a file it could not read")
	}
}

func TestCreateCountsNameCharactersAndMarksAFailedSave(t *testing.T) {
	path := TokensPath(t.TempDir())
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := mustGrant(t, LevelRead, nil, time.Time{})
	if _, _, err := registry.Create(strings.Repeat("बॉट", 20), grant, testNow); err != nil {
		t.Errorf("a 60-character name in a non-Latin script: %v", err)
	}
	if _, _, err := registry.Create(strings.Repeat("ब", 65), grant, testNow); err == nil || errors.Is(err, ErrNotSaved) {
		t.Errorf("a 65-character name must be refused as bad input: %v", err)
	}
	for _, name := range []string{"tok_0123456789abcdef", "TOK_0123456789ABCDEF"} {
		if _, _, err := registry.Create(name, grant, testNow); err == nil {
			t.Errorf("%s looks like a token id and must be refused: revoke takes an id or a name", name)
		}
	}
	short := mustGrant(t, LevelRead, nil, testNow.Add(time.Hour))
	if _, _, err := registry.Create("nightly", short, testNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Create(strings.Repeat("बॉट", 20), grant, testNow.Add(25*time.Hour)); err == nil {
		t.Fatal("want a create refused for a name that is taken")
	}
	if len(registry.List()) != 2 {
		t.Errorf("a refused create must not drop an expired token from the list: %d tokens, want 2", len(registry.List()))
	}
	if _, _, err := registry.Create("nightly", mustGrant(t, LevelRead, nil, testNow.Add(26*time.Hour)), testNow.Add(25*time.Hour)); err != nil {
		t.Errorf("an expired token must not keep its name or its place: %v", err)
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed := len(registry.List())
	if _, _, err := registry.Create("ci", grant, testNow.Add(27*time.Hour)); !errors.Is(err, ErrNotSaved) {
		t.Errorf("a token that cannot be written: %v, want ErrNotSaved", err)
	}
	if len(registry.List()) != listed {
		t.Errorf("a create that could not be saved must leave the list as it was: %d tokens, want %d", len(registry.List()), listed)
	}
}
