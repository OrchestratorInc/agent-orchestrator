package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type themeRequest struct {
	method, path string
	body         map[string]any
}

// themeDaemon fakes /api/v1/settings/themes and /theme-switch closely enough
// for the CLI: POST /themes stores the posted file as a theme, DELETE removes
// one, PUT /theme-switch records the switch.
type themeDaemon struct {
	mu       sync.Mutex
	library  customThemesDTO
	requests []themeRequest
	warnings []string
	reject   string
	// conflicts makes the next imports fail as if another writer had changed
	// the theme since the CLI read it; onConflict is that other writer.
	conflicts  int
	onConflict func(*themeDaemon)
}

func newThemeDaemon(t *testing.T, initial customThemesDTO) (*themeDaemon, *httptest.Server) {
	t.Helper()
	d := &themeDaemon{library: initial}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		req := themeRequest{method: r.Method, path: r.URL.Path}
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&req.body); err != nil {
				t.Errorf("decode %s: %v", r.Method, err)
			}
		}
		d.requests = append(d.requests, req)
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/"+customThemesPath+"/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/"+customThemesPath:
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/"+themeSwitchPath:
			d.library.Switch = themeSwitchDTO{ThemeID: str(req.body["themeId"]), Mode: str(req.body["mode"]), ID: "s1"}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/"+customThemesPath:
			if d.reject != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "CUSTOM_THEME_INVALID", "message": d.reject})
				return
			}
			if d.conflicts > 0 {
				d.conflicts--
				if d.onConflict != nil {
					d.onConflict(d)
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "CUSTOM_THEME_CHANGED", "message": "changed since it was read"})
				return
			}
			var theme customThemeDTO
			raw, _ := json.Marshal(req.body["file"])
			_ = json.Unmarshal(raw, &theme)
			if override := str(req.body["id"]); override != "" {
				theme.ID = override
			}
			replaced := false
			for i := range d.library.Themes {
				if d.library.Themes[i].ID == theme.ID {
					d.library.Themes[i], replaced = theme, true
				}
			}
			if !replaced {
				d.library.Themes = append(d.library.Themes, theme)
			}
			if use, _ := req.body["use"].(bool); use {
				d.library.Switch = themeSwitchDTO{ThemeID: theme.ID, Mode: str(req.body["mode"]), ID: "s2"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"themes": d.library.Themes, "switch": d.library.Switch, "theme": theme, "warnings": d.warnings,
			})
			return
		case r.Method == http.MethodDelete && id != r.URL.Path:
			kept := d.library.Themes[:0]
			for _, theme := range d.library.Themes {
				if theme.ID != id {
					kept = append(kept, theme)
				}
			}
			d.library.Themes = kept
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(d.library)
	}))
	t.Cleanup(srv.Close)
	return d, srv
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (d *themeDaemon) posts() []themeRequest {
	var out []themeRequest
	for _, req := range d.requests {
		if req.method == http.MethodPost {
			out = append(out, req)
		}
	}
	return out
}

type themeRun struct {
	out, errOut string
	daemon      *themeDaemon
	err         error
}

func runThemeCLI(t *testing.T, initial customThemesDTO, configure func(*themeDaemon), args ...string) themeRun {
	t.Helper()
	cfg := setConfigEnv(t)
	d, srv := newThemeDaemon(t, initial)
	if configure != nil {
		configure(d)
	}
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append([]string{"theme"}, args...)...)
	return themeRun{out: out, errOut: errOut, daemon: d, err: err}
}

func sumiLibrary() customThemesDTO {
	return customThemesDTO{Themes: []customThemeDTO{{
		ID: "sumi", Name: "Sumi", Appearance: "dark",
		Colors:   map[string]string{"canvas": "#101012", "accent": "#ffffff"},
		Variants: map[string]map[string]string{"light": {"canvas": "#fafafa"}},
		CSS:      ".keep {}",
	}}}
}

func postedFile(t *testing.T, run themeRun) map[string]any {
	t.Helper()
	posts := run.daemon.posts()
	if len(posts) != 1 || posts[0].path != "/api/v1/settings/themes" {
		t.Fatalf("posts = %+v, want one POST /api/v1/settings/themes", posts)
	}
	file, ok := posts[0].body["file"].(map[string]any)
	if !ok {
		t.Fatalf("POST body has no file: %+v", posts[0].body)
	}
	return file
}

func TestThemeListsCustomAndBuiltInThemes(t *testing.T) {
	run := runThemeCLI(t, sumiLibrary(), nil)
	if run.err != nil {
		t.Fatalf("theme: %v", run.err)
	}
	if len(run.daemon.requests) != 1 || run.daemon.requests[0].method != http.MethodGet {
		t.Fatalf("requests = %v, want one GET", run.daemon.requests)
	}
	for _, want := range []string{`sumi  "Sumi", dark and light`, "Built-in themes: orchestrate"} {
		if !strings.Contains(run.out, want) {
			t.Errorf("output missing %q:\n%s", want, run.out)
		}
	}
	empty := runThemeCLI(t, customThemesDTO{}, nil)
	if empty.err != nil || !strings.Contains(empty.out, "Custom themes: none") {
		t.Fatalf("empty: err=%v out=%s", empty.err, empty.out)
	}
}

func TestThemeShowPrintsTheShareableFile(t *testing.T) {
	run := runThemeCLI(t, sumiLibrary(), nil, "show", "sumi")
	if run.err != nil {
		t.Fatalf("theme show: %v", run.err)
	}
	var file map[string]any
	if err := json.Unmarshal([]byte(run.out), &file); err != nil {
		t.Fatalf("show output is not JSON: %v\n%s", err, run.out)
	}
	if file["version"] != float64(1) || file["id"] != "sumi" || file["name"] != "Sumi" || file["appearance"] != "dark" || file["css"] != ".keep {}" {
		t.Fatalf("file = %+v", file)
	}
	if _, has := file["updatedAt"]; has {
		t.Fatal("the file must not carry updatedAt")
	}
	if missing := runThemeCLI(t, sumiLibrary(), nil, "show", "missing"); missing.err == nil {
		t.Fatal("show of a missing theme should fail")
	}
}

func TestThemeImportPostsTheFileAndPrintsWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one.json")
	file := `{"version":1,"name":"One Pro","appearance":"dark","colors":{"canvas":"oklch(0.13 0.003 246)","sidebarBg":"#000"}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	run := runThemeCLI(t, customThemesDTO{}, func(d *themeDaemon) { d.warnings = []string{`left out "sidebarBg": not a color role`} },
		"import", path, "--id", "one-pro", "--use", "--mode", "dark")
	if run.err != nil {
		t.Fatalf("theme import: %v", run.err)
	}
	posted := postedFile(t, run)
	if posted["name"] != "One Pro" {
		t.Fatalf("posted file = %+v", posted)
	}
	body := run.daemon.posts()[0].body
	if body["id"] != "one-pro" || body["use"] != true || body["mode"] != "dark" {
		t.Fatalf("POST body = %+v", body)
	}
	if !strings.Contains(run.errOut, `warning: left out "sidebarBg"`) {
		t.Errorf("stderr missing the warning:\n%s", run.errOut)
	}
	if !strings.Contains(run.out, "Every window switched to it, dark.") {
		t.Errorf("output:\n%s", run.out)
	}
}

func TestThemeImportRejectsNonJSONAndModeWithoutUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := runThemeCLI(t, customThemesDTO{}, nil, "import", path)
	if run.err == nil || len(run.daemon.posts()) != 0 {
		t.Fatalf("non-JSON import: err=%v posts=%v", run.err, run.daemon.posts())
	}
	run = runThemeCLI(t, customThemesDTO{}, nil, "import", path, "--mode", "dark")
	if run.err == nil || !strings.Contains(run.err.Error(), "--use") {
		t.Fatalf("--mode without --use: err=%v", run.err)
	}
}

func TestThemeSetMakesAThemeFromFlags(t *testing.T) {
	run := runThemeCLI(t, customThemesDTO{}, nil, "set", "Slack", "--name", "Slack",
		"--set", "sidebar=#3f0e40", "--dark", "canvas=#1a1d21", "--light", "canvas=#ffffff", "--use")
	if run.err != nil {
		t.Fatalf("theme set: %v", run.err)
	}
	file := postedFile(t, run)
	if file["id"] != "slack" || file["name"] != "Slack" || file["appearance"] != "dark" {
		t.Fatalf("file = %+v", file)
	}
	colors, _ := file["colors"].(map[string]any)
	if colors["sidebar"] != "#3f0e40" || colors["canvas"] != "#1a1d21" {
		t.Fatalf("dark colors = %+v", colors)
	}
	variants, _ := file["variants"].(map[string]any)
	light, _ := variants["light"].(map[string]any)
	if light["sidebar"] != "#3f0e40" || light["canvas"] != "#ffffff" {
		t.Fatalf("light colors = %+v", light)
	}
}

func TestThemeSetLightOnlyMakesALightTheme(t *testing.T) {
	run := runThemeCLI(t, customThemesDTO{}, nil, "set", "paper", "--light", "canvas=#fbf7ef")
	if run.err != nil {
		t.Fatalf("theme set: %v", run.err)
	}
	file := postedFile(t, run)
	if file["appearance"] != "light" {
		t.Fatalf("appearance = %v, want light", file["appearance"])
	}
	if _, has := file["variants"]; has {
		t.Fatalf("a light-only theme has no variants: %+v", file)
	}
}

func TestThemeSetChangesPartAndKeepsTheRest(t *testing.T) {
	run := runThemeCLI(t, sumiLibrary(), nil, "set", "sumi", "--dark", "accent=#e0a84f", "--dark", "canvas=", "--light", "text=#111111")
	if run.err != nil {
		t.Fatalf("theme set: %v", run.err)
	}
	file := postedFile(t, run)
	colors, _ := file["colors"].(map[string]any)
	if colors["accent"] != "#e0a84f" {
		t.Fatalf("dark colors = %+v", colors)
	}
	if _, has := colors["canvas"]; has {
		t.Fatalf("an empty value removes the role: %+v", colors)
	}
	light, _ := file["variants"].(map[string]any)["light"].(map[string]any)
	if light["canvas"] != "#fafafa" || light["text"] != "#111111" {
		t.Fatalf("light colors = %+v", light)
	}
	if file["css"] != ".keep {}" || file["name"] != "Sumi" {
		t.Fatalf("name and CSS must survive: %+v", file)
	}
}

func TestThemeSetReadsCSSAndRefusesAnAppearanceChange(t *testing.T) {
	css := filepath.Join(t.TempDir(), "sumi.css")
	if err := os.WriteFile(css, []byte(":root { --radius: 0; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := runThemeCLI(t, sumiLibrary(), nil, "set", "sumi", "--css", css)
	if run.err != nil {
		t.Fatalf("theme set --css: %v", run.err)
	}
	if file := postedFile(t, run); file["css"] != ":root { --radius: 0; }" {
		t.Fatalf("css = %v", file["css"])
	}
	run = runThemeCLI(t, sumiLibrary(), nil, "set", "sumi", "--clear-css")
	if file := postedFile(t, run); run.err != nil || file["css"] != nil {
		t.Fatalf("--clear-css: err=%v file=%+v", run.err, file)
	}
	run = runThemeCLI(t, sumiLibrary(), nil, "set", "sumi", "--appearance", "light")
	if run.err == nil || len(run.daemon.posts()) != 0 {
		t.Fatalf("appearance change: err=%v posts=%v", run.err, run.daemon.posts())
	}
	run = runThemeCLI(t, sumiLibrary(), nil, "set", "sumi", "--dark", "canvas")
	if run.err == nil {
		t.Fatal("a pair without = should fail")
	}
}

func TestThemeSetSurfacesTheDaemonsRefusal(t *testing.T) {
	run := runThemeCLI(t, customThemesDTO{}, func(d *themeDaemon) { d.reject = "dark canvas must be a literal color" },
		"set", "x", "--dark", "canvas=var(--y)")
	if run.err == nil || !strings.Contains(run.err.Error(), "literal color") {
		t.Fatalf("err = %v", run.err)
	}
}

func TestThemeUseModeAndDelete(t *testing.T) {
	run := runThemeCLI(t, sumiLibrary(), nil, "use", "nord", "--mode", "dark")
	if run.err != nil || !strings.Contains(run.out, "Every window switched to nord, dark.") {
		t.Fatalf("use: err=%v out=%s", run.err, run.out)
	}
	last := run.daemon.requests[len(run.daemon.requests)-1]
	if last.method != http.MethodPut || last.path != "/api/v1/settings/theme-switch" || last.body["themeId"] != "nord" || last.body["mode"] != "dark" {
		t.Fatalf("use request = %+v", last)
	}

	run = runThemeCLI(t, sumiLibrary(), nil, "mode", "light")
	if run.err != nil || run.daemon.requests[0].body["mode"] != "light" {
		t.Fatalf("mode: err=%v requests=%+v", run.err, run.daemon.requests)
	}
	if bad := runThemeCLI(t, sumiLibrary(), nil, "mode", "dim"); bad.err == nil {
		t.Fatal("mode dim should fail")
	}

	run = runThemeCLI(t, sumiLibrary(), nil, "delete", "sumi")
	if run.err != nil || !strings.Contains(run.out, "Deleted sumi.") {
		t.Fatalf("delete: err=%v out=%s", run.err, run.out)
	}
	last = run.daemon.requests[len(run.daemon.requests)-1]
	if last.method != http.MethodDelete || last.path != "/api/v1/settings/themes/sumi" {
		t.Fatalf("delete request = %+v", last)
	}
}

func TestThemeSetStartsOverWhenTheThemeChangedUnderIt(t *testing.T) {
	// Another writer sets the dark sidebar between this command's read and its
	// write. The command must reapply its own change on top, not write back
	// the copy it read.
	run := runThemeCLI(t, sumiLibrary(), func(d *themeDaemon) {
		d.conflicts = 1
		d.onConflict = func(d *themeDaemon) {
			d.library.Themes[0].Colors["sidebar"] = "#3f0e40"
			d.library.Themes[0].UpdatedAt = d.library.Themes[0].UpdatedAt.Add(time.Second)
		}
	}, "set", "sumi", "--dark", "accent=#e0a84f")
	if run.err != nil {
		t.Fatalf("theme set: %v", run.err)
	}
	posts := run.daemon.posts()
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want the refused write and the retry", len(posts))
	}
	if first, second := posts[0].body["ifUpdatedAt"], posts[1].body["ifUpdatedAt"]; first == nil || second == nil || first == second {
		t.Fatalf("each write must carry the revision it read: %v then %v", first, second)
	}
	colors, _ := posts[1].body["file"].(map[string]any)["colors"].(map[string]any)
	if colors["sidebar"] != "#3f0e40" || colors["accent"] != "#e0a84f" {
		t.Fatalf("retry lost a change: %+v", colors)
	}
}

func TestThemeSetGivesUpAfterRepeatedConflicts(t *testing.T) {
	run := runThemeCLI(t, sumiLibrary(), func(d *themeDaemon) { d.conflicts = 99 }, "set", "sumi", "--dark", "accent=#e0a84f")
	if run.err == nil {
		t.Fatal("a theme that keeps changing should fail the command")
	}
	if got := len(run.daemon.posts()); got != themeSetAttempts {
		t.Fatalf("posts = %d, want %d attempts", got, themeSetAttempts)
	}
}

func TestThemeSetSaysANewThemeMustNotExist(t *testing.T) {
	run := runThemeCLI(t, customThemesDTO{}, nil, "set", "fresh", "--dark", "canvas=#000000")
	if run.err != nil {
		t.Fatalf("theme set: %v", run.err)
	}
	if got, present := run.daemon.posts()[0].body["ifUpdatedAt"]; !present || got != "" {
		t.Fatalf("ifUpdatedAt = %v (present %v), want the empty string for a new theme", got, present)
	}
}

func TestThemeShowKeepsAnInlineSVGReadable(t *testing.T) {
	library := sumiLibrary()
	library.Themes[0].CSS = `a{background:url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'><rect fill='red'/></svg>")}&`
	run := runThemeCLI(t, library, nil, "show", "sumi")
	if run.err != nil {
		t.Fatalf("theme show: %v", run.err)
	}
	if !strings.Contains(run.out, "<svg xmlns=") || strings.Contains(run.out, `\u003c`) {
		t.Fatalf("the file must not escape < > &:\n%s", run.out)
	}
	var file map[string]any
	if err := json.Unmarshal([]byte(run.out), &file); err != nil || file["css"] != library.Themes[0].CSS {
		t.Fatalf("the file must round-trip: %v", err)
	}
}
