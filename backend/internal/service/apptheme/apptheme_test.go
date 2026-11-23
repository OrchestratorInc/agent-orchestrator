package apptheme

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeStore struct {
	mu    sync.Mutex
	value string
	sets  int
}

func (f *fakeStore) GetCustomThemes(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value, nil
}

func (f *fakeStore) SetCustomThemes(_ context.Context, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value = value
	f.sets++
	return nil
}

var fixedNow = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func newService() (*Service, *fakeStore) {
	store := &fakeStore{value: "{}"}
	return New(store, func() time.Time { return fixedNow }), store
}

// file decodes a theme file the way the daemon receives one.
func file(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("test file is not JSON: %v", err)
	}
	return out
}

func requireInvalid(t *testing.T, err error, want string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "CUSTOM_THEME_INVALID" {
		t.Fatalf("err = %v, want CUSTOM_THEME_INVALID", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to mention %q", err, want)
	}
}

func TestGetEmptyLibrary(t *testing.T) {
	svc, _ := newService()
	got, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Themes == nil || len(got.Themes) != 0 || got.Switch != (Switch{}) {
		t.Fatalf("Get = %#v, want no themes and no switch", got)
	}
}

func TestImportStoresASharedThemeFile(t *testing.T) {
	svc, store := newService()
	got, err := svc.Import(context.Background(), file(t, `{
		"version": 1,
		"name": " One Pro Night / Day ",
		"appearance": "dark",
		"colors": {
			"canvas": "oklch(0.133499 0.003187 246.06)",
			"sidebarRowSelected": " #1164A3 ",
			"toolbar": "rgb(20 20 22 / 90%)"
		},
		"variants": {"light": {"canvas": "oklch(0.9850 0.0038 246.06)"}},
		"css": "[data-workspace-area=\"conversation\"]{background:url(data:image/webp;base64,AAAA)}",
		"collection": {"id": "one", "label": "One"},
		"managed": true
	}`), ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	theme := got.Theme
	if theme.ID != "one-pro-night-day" || theme.Name != "One Pro Night / Day" || theme.Appearance != "dark" {
		t.Fatalf("theme = %+v", theme)
	}
	if theme.Colors["canvas"] != "oklch(0.133499 0.003187 246.06)" || theme.Colors["sidebarRowSelected"] != "#1164A3" || theme.Colors["toolbar"] == "" {
		t.Fatalf("colors = %+v", theme.Colors)
	}
	if theme.Variants["light"]["canvas"] == "" || !strings.Contains(theme.CSS, "data:image/webp") {
		t.Fatalf("variants/css lost: %+v", theme)
	}
	// `toolbar` is a role this app keeps but has no surface for.
	if !theme.UpdatedAt.Equal(fixedNow) || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "no surface for: toolbar") || store.sets != 1 {
		t.Fatalf("updatedAt=%v warnings=%v sets=%d", theme.UpdatedAt, got.Warnings, store.sets)
	}
	again, err := svc.Get(context.Background())
	if err != nil || len(again.Themes) != 1 || again.Themes[0].ID != theme.ID {
		t.Fatalf("round trip: %+v %v", again, err)
	}
}

func TestImportLeavesOutUnknownRolesWithAWarning(t *testing.T) {
	svc, _ := newService()
	got, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"Sumi","appearance":"light",
		"colors":{"canvas":"#fafafa","sidebarBg":"#000"},"variants":{"dark":{"bogus":"#111","text":"white"}}}`), ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, kept := got.Theme.Colors["sidebarBg"]; kept || got.Theme.Variants["dark"]["text"] != "white" {
		t.Fatalf("theme = %+v", got.Theme)
	}
	if len(got.Warnings) != 2 || !strings.Contains(got.Warnings[0], `"bogus"`) || !strings.Contains(got.Warnings[1], `"sidebarBg"`) {
		t.Fatalf("warnings = %v", got.Warnings)
	}
}

func TestImportReplacesByIDAndAnOverrideWins(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	if _, err := svc.Import(ctx, file(t, `{"version":1,"id":"sumi","name":"Sumi","appearance":"dark","colors":{"canvas":"#101012"}}`), ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Import(ctx, file(t, `{"version":1,"id":"other","name":"Sumi 2","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{ID: " Sumi "})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(got.Library.Themes) != 1 || got.Theme.ID != "sumi" || got.Theme.Name != "Sumi 2" || got.Theme.Colors["canvas"] != "#000" {
		t.Fatalf("library = %+v", got.Library)
	}
}

func TestImportKeepExistingRefusesToReplace(t *testing.T) {
	svc, store := newService()
	ctx := context.Background()
	first := `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#101012"}}`
	if _, err := svc.Import(ctx, file(t, first), ImportOptions{KeepExisting: true}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	_, err := svc.Import(ctx, file(t, `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{KeepExisting: true})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindConflict || apiErr.Code != "CUSTOM_THEME_EXISTS" {
		t.Fatalf("err = %v, want 409 CUSTOM_THEME_EXISTS", err)
	}
	got, _ := svc.Get(ctx)
	if store.sets != 1 || len(got.Themes) != 1 || got.Themes[0].Colors["canvas"] != "#101012" {
		t.Fatalf("the existing theme must be untouched: sets=%d themes=%+v", store.sets, got.Themes)
	}
}

// Each change is one read-modify-write of the stored library; without the
// service's lock two saves at once would each write back a copy missing the
// other's theme.
func TestConcurrentImportsKeepEveryTheme(t *testing.T) {
	svc, _ := newService()
	const count = 20
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"n","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{ID: "t" + strconv.Itoa(i)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
	}
	got, err := svc.Get(context.Background())
	if err != nil || len(got.Themes) != count {
		t.Fatalf("themes = %d, want %d (err %v)", len(got.Themes), count, err)
	}
}

func TestImportWithSwitch(t *testing.T) {
	svc, _ := newService()
	got, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#101012"}}`), ImportOptions{Switch: &Switch{Mode: "dark"}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if sw := got.Library.Switch; sw.ThemeID != "sumi" || sw.Mode != "dark" || sw.ID == "" {
		t.Fatalf("switch = %+v", sw)
	}
}

func TestImportRefusesBadFiles(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"no version", `{"name":"x","appearance":"dark","colors":{"canvas":"#000"}}`, `"version": 1`},
		{"newer version", `{"version":2,"name":"x","appearance":"dark","colors":{"canvas":"#000"}}`, `"version": 1`},
		{"no name", `{"version":1,"appearance":"dark","colors":{"canvas":"#000"}}`, `"name"`},
		{"long name", `{"version":1,"name":"` + strings.Repeat("n", 49) + `","appearance":"dark","colors":{"canvas":"#000"}}`, `"name"`},
		{"appearance", `{"version":1,"name":"x","appearance":"dim","colors":{"canvas":"#000"}}`, `"appearance"`},
		{"no colors", `{"version":1,"name":"x","appearance":"dark","colors":{}}`, `"colors"`},
		{"only unknown roles", `{"version":1,"name":"x","appearance":"dark","colors":{"bogus":"#000"}}`, `"colors"`},
		{"variable", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"var(--background)"}}`, "literal color"},
		{"color-mix", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"color-mix(in srgb, red, blue)"}}`, "literal color"},
		{"url color", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"url(https://evil.example/a)"}}`, "literal color"},
		{"number color", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":7}}`, "literal color"},
		{"same variant", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"variants":{"dark":{"canvas":"#111"}}}`, `"variants"`},
		{"built-in id", `{"version":1,"id":"nord","name":"x","appearance":"dark","colors":{"canvas":"#000"}}`, "built-in"},
		{"built-in name", `{"version":1,"name":"Nord","appearance":"dark","colors":{"canvas":"#000"}}`, "built-in"},
		{"bad id", `{"version":1,"id":"Not OK","name":"x","appearance":"dark","colors":{"canvas":"#000"}}`, "theme id"},
		{"import", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"css":"@import url(https://evil.example/a.css);"}`, "@import"},
		{"remote url", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"css":"a{background:url(https://evil.example/a.png)}"}`, "cannot load"},
		{"image-set", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"css":"a{background:image-set(\"data:x\" 1x)}"}`, "image-set"},
		{"css type", `{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"css":7}`, `"css"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newService()
			_, err := svc.Import(context.Background(), file(t, tc.file), ImportOptions{})
			requireInvalid(t, err, tc.want)
			if store.sets != 0 {
				t.Fatal("a refused file must not be stored")
			}
		})
	}
}

func TestImportAllowsEmbeddedFilesAndCommentedURLs(t *testing.T) {
	svc, _ := newService()
	css := `/* see https://example.com url(https://example.com/x) */ @font-face{font-family:X;src:url("data:font/woff2;base64,AAAA")} a{fill:url(#gradient)}`
	raw, _ := json.Marshal(map[string]any{"version": 1, "name": "x", "appearance": "dark", "colors": map[string]string{"canvas": "#000"}, "css": css})
	if _, err := svc.Import(context.Background(), file(t, string(raw)), ImportOptions{}); err != nil {
		t.Fatalf("Import: %v", err)
	}
}

func TestImportRefusesOversizedCSSAndTooManyThemes(t *testing.T) {
	svc, _ := newService()
	raw, _ := json.Marshal(map[string]any{"version": 1, "name": "x", "appearance": "dark", "colors": map[string]string{"canvas": "#000"}, "css": strings.Repeat("a", MaxCSSBytes+1)})
	_, err := svc.Import(context.Background(), file(t, string(raw)), ImportOptions{})
	requireInvalid(t, err, "at most")

	svc, _ = newService()
	for i := 0; i < MaxThemes; i++ {
		id := "t" + strconv.Itoa(i)
		if _, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"n","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{ID: id}); err != nil {
			t.Fatalf("Import %d: %v", i, err)
		}
	}
	_, err = svc.Import(context.Background(), file(t, `{"version":1,"name":"one more","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{})
	requireInvalid(t, err, "delete one first")
}

func TestFileRoundTripsThroughParseFile(t *testing.T) {
	theme := Theme{
		ID: "sumi", Name: "Sumi", Appearance: "light",
		Colors:   map[string]string{"canvas": "#fafafa"},
		Variants: map[string]map[string]string{"dark": {"canvas": "#101012"}},
		CSS:      ":root { --radius: 0; }",
	}
	raw, err := json.Marshal(File(theme))
	if err != nil {
		t.Fatal(err)
	}
	got, warnings, err := ParseFile(file(t, string(raw)), "")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("ParseFile: %v %v", err, warnings)
	}
	if got.ID != theme.ID || got.Name != theme.Name || got.Appearance != theme.Appearance || got.Colors["canvas"] != "#fafafa" || got.Variants["dark"]["canvas"] != "#101012" || got.CSS != theme.CSS {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestIDFromName(t *testing.T) {
	for name, want := range map[string]string{
		"One Pro Night / Day": "one-pro-night-day",
		"  Sumi  ":            "sumi",
		"--x--":               "x",
	} {
		if got := IDFromName(name); got != want {
			t.Errorf("IDFromName(%q) = %q, want %q", name, got, want)
		}
	}
	first, second := IDFromName("墨"), IDFromName("雪")
	if !strings.HasPrefix(first, "theme-") || first == second || first != IDFromName("墨") {
		t.Errorf("non-ASCII names need stable, distinct ids: %q %q", first, second)
	}
}

func TestUseAndDelete(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	if _, err := svc.Import(ctx, file(t, `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#101012"}}`), ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Use(ctx, Switch{ThemeID: " Nord ", Mode: "Dark"})
	if err != nil || got.Switch.ThemeID != "nord" || got.Switch.Mode != "dark" || got.Switch.ID == "" {
		t.Fatalf("Use built-in: %+v %v", got.Switch, err)
	}
	if got, err = svc.Use(ctx, Switch{ThemeID: "sumi"}); err != nil || got.Switch.ThemeID != "sumi" {
		t.Fatalf("Use custom: %+v %v", got.Switch, err)
	}
	_, err = svc.Use(ctx, Switch{ThemeID: "missing"})
	requireInvalid(t, err, "there is no theme")
	_, err = svc.Use(ctx, Switch{Mode: "dim"})
	requireInvalid(t, err, "mode must be")
	_, err = svc.Use(ctx, Switch{})
	requireInvalid(t, err, "name a theme")

	got, err = svc.Delete(ctx, "sumi")
	if err != nil || len(got.Themes) != 0 || got.Switch != (Switch{}) {
		t.Fatalf("Delete: %+v %v", got, err)
	}
	var apiErr *apierr.Error
	if _, err = svc.Delete(ctx, "sumi"); !errors.As(err, &apiErr) || apiErr.Code != "CUSTOM_THEME_NOT_FOUND" {
		t.Fatalf("second Delete err = %v", err)
	}
}

// The same vectors hold the renderer's scan (theme-css-guard.test.ts): a
// stylesheet one side refuses must not be one the other applies.
func TestCSSGuardVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/css_guard_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Refuse []string `json:"refuse"`
		Accept []string `json:"accept"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Refuse) < 30 || len(vectors.Accept) < 15 {
		t.Fatalf("vectors look truncated: %d refuse, %d accept", len(vectors.Refuse), len(vectors.Accept))
	}
	for _, css := range vectors.Refuse {
		if cssLoadsFile(css) == "" {
			t.Errorf("accepted CSS that loads a file: %q", css)
		}
	}
	for _, css := range vectors.Accept {
		if problem := cssLoadsFile(css); problem != "" {
			t.Errorf("refused harmless CSS %q: %s", css, problem)
		}
	}
}

// Whatever the input, the scan ends; it reads untrusted text.
func TestCSSGuardTerminatesOnBrokenInput(t *testing.T) {
	for _, css := range []string{"\\", "url(", "url(\"", "@", "#", "-", "--", "/*", "\"\\", "a{b:url(data:x", "\\\n", "@import", "u\\", string([]byte{0xff, 0xfe, '(', 0x80})} {
		_ = cssLoadsFile(css)
	}
}

func TestImportIfUpdatedAtGuardsAReadModifyWrite(t *testing.T) {
	now := fixedNow
	store := &fakeStore{value: "{}"}
	svc := New(store, func() time.Time { now = now.Add(time.Second); return now })
	ctx := context.Background()
	sumi := `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#101012"}}`
	absent := ""

	// "" means the caller saw no such theme: fine the first time.
	first, err := svc.Import(ctx, file(t, sumi), ImportOptions{IfUpdatedAt: &absent})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	// ...and a conflict once one exists.
	_, err = svc.Import(ctx, file(t, sumi), ImportOptions{IfUpdatedAt: &absent})
	requireConflict(t, err, "CUSTOM_THEME_CHANGED")

	// The revision just read lets the write through, once.
	revision := Revision(first.Theme)
	second, err := svc.Import(ctx, file(t, `{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#000"}}`), ImportOptions{IfUpdatedAt: &revision})
	if err != nil {
		t.Fatalf("import at the read revision: %v", err)
	}
	_, err = svc.Import(ctx, file(t, sumi), ImportOptions{IfUpdatedAt: &revision})
	requireConflict(t, err, "CUSTOM_THEME_CHANGED")
	if Revision(second.Theme) == revision {
		t.Fatal("a save must change the revision")
	}

	// A stale writer must not bring back a theme deleted in between.
	if _, err := svc.Delete(ctx, "sumi"); err != nil {
		t.Fatal(err)
	}
	stale := Revision(second.Theme)
	_, err = svc.Import(ctx, file(t, sumi), ImportOptions{IfUpdatedAt: &stale})
	requireConflict(t, err, "CUSTOM_THEME_CHANGED")
	if got, _ := svc.Get(ctx); len(got.Themes) != 0 {
		t.Fatalf("the deleted theme came back: %+v", got.Themes)
	}
}

func requireConflict(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindConflict || apiErr.Code != code {
		t.Fatalf("err = %v, want 409 %s", err, code)
	}
}

func TestImportNotesRolesThisAppDoesNotPaint(t *testing.T) {
	svc, _ := newService()
	got, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"Shared","appearance":"dark",
		"colors":{"canvas":"#000","toolbar":"#111","update":"#222","iconMuted":"#333","messageAction":"#444","searchMatchForeground":"#555"},
		"variants":{"light":{"terminalScrollbar":"#eee"}}}`), ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// Kept, so the file exports as it came in.
	if got.Theme.Colors["toolbar"] != "#111" || got.Theme.Variants["light"]["terminalScrollbar"] != "#eee" {
		t.Fatalf("unpainted roles must be kept: %+v", got.Theme)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "no surface for: toolbar, iconMuted, update, messageAction and 2 more") {
		t.Fatalf("warnings = %v", got.Warnings)
	}
	// A theme of painted roles only has nothing to note.
	plain, err := svc.Import(context.Background(), file(t, `{"version":1,"name":"Plain","appearance":"dark","colors":{"canvas":"#000","sidebar":"#111"}}`), ImportOptions{})
	if err != nil || len(plain.Warnings) != 0 {
		t.Fatalf("plain: %v %v", plain.Warnings, err)
	}
}

func TestUnpaintedRolesAreAllColorRoles(t *testing.T) {
	for _, role := range UnpaintedRoles {
		if !slices.Contains(ColorRoles, role) {
			t.Errorf("UnpaintedRoles names %q, which is not a color role", role)
		}
	}
}
