package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/apptheme"
)

type memoryCustomThemeStore struct{ value string }

func (m *memoryCustomThemeStore) GetCustomThemes(context.Context) (string, error) {
	return m.value, nil
}

func (m *memoryCustomThemeStore) SetCustomThemes(_ context.Context, value string) error {
	m.value = value
	return nil
}

func customThemeRouter(store *memoryCustomThemeStore) http.Handler {
	r := chi.NewRouter()
	(&CustomThemeController{Svc: apptheme.New(store, nil)}).Register(r)
	return r
}

func serveCustomThemes(t *testing.T, h http.Handler, method, path, body string, into any) int {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code == http.StatusOK && into != nil {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatalf("%s %s: decode %s: %v", method, path, w.Body, err)
		}
	}
	return w.Code
}

func TestCustomThemesImportListSwitchDelete(t *testing.T) {
	store := &memoryCustomThemeStore{value: "{}"}
	h := customThemeRouter(store)

	var list CustomThemesResponse
	if code := serveCustomThemes(t, h, http.MethodGet, "/settings/themes", "", &list); code != http.StatusOK || list.Themes == nil || len(list.Themes) != 0 {
		t.Fatalf("empty GET status=%d got=%+v", code, list)
	}

	var imported ImportCustomThemeResponse
	code := serveCustomThemes(t, h, http.MethodPost, "/settings/themes",
		`{"file":{"version":1,"name":"Sumi","appearance":"dark","colors":{"canvas":"#101012","nope":"#000"}},"use":true,"mode":"dark"}`, &imported)
	if code != http.StatusOK {
		t.Fatalf("POST status=%d", code)
	}
	theme := imported.Theme
	if theme.ID != "sumi" || theme.Name != "Sumi" || theme.Appearance != "dark" || theme.Colors["canvas"] != "#101012" || theme.Variants == nil {
		t.Fatalf("theme = %+v", theme)
	}
	if len(imported.Themes) != 1 || imported.Switch.ThemeID != "sumi" || imported.Switch.Mode != "dark" || imported.Switch.ID == "" {
		t.Fatalf("library = %+v", imported)
	}
	if len(imported.Warnings) != 1 || !strings.Contains(imported.Warnings[0], `"nope"`) {
		t.Fatalf("warnings = %v", imported.Warnings)
	}

	// The id in the request wins over the file's, and a clean file has an
	// empty (not null) warnings list.
	imported = ImportCustomThemeResponse{}
	code = serveCustomThemes(t, h, http.MethodPost, "/settings/themes",
		`{"file":{"version":1,"id":"other","name":"Paper","appearance":"light","colors":{"canvas":"#fbf7ef"}},"id":"paper"}`, &imported)
	if code != http.StatusOK || imported.Theme.ID != "paper" || imported.Warnings == nil || len(imported.Themes) != 2 {
		t.Fatalf("second POST status=%d got=%+v", code, imported)
	}
	if imported.Switch.ThemeID != "sumi" {
		t.Fatalf("an import without use must keep the last switch: %+v", imported.Switch)
	}

	// Settings imports with keepExisting, so a shared file cannot silently
	// replace a theme that has the same id.
	if code = serveCustomThemes(t, h, http.MethodPost, "/settings/themes",
		`{"file":{"version":1,"name":"Paper","appearance":"light","colors":{"canvas":"#ffffff"}},"keepExisting":true}`, nil); code != http.StatusConflict {
		t.Fatalf("keepExisting on an existing id: status=%d, want 409", code)
	}

	// A caller that read no "paper" theme must not overwrite the one there is.
	if code = serveCustomThemes(t, h, http.MethodPost, "/settings/themes",
		`{"file":{"version":1,"name":"Paper","appearance":"light","colors":{"canvas":"#ffffff"}},"ifUpdatedAt":""}`, nil); code != http.StatusConflict {
		t.Fatalf("ifUpdatedAt for an absent theme: status=%d, want 409", code)
	}

	if code = serveCustomThemes(t, h, http.MethodPut, "/settings/theme-switch", `{"themeId":"nord","mode":"light"}`, &list); code != http.StatusOK || list.Switch.ThemeID != "nord" || list.Switch.Mode != "light" {
		t.Fatalf("switch status=%d got=%+v", code, list.Switch)
	}
	if code = serveCustomThemes(t, h, http.MethodPut, "/settings/theme-switch", `{"themeId":"missing"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown theme switch status=%d", code)
	}

	if code = serveCustomThemes(t, h, http.MethodDelete, "/settings/themes/sumi", "", &list); code != http.StatusOK || len(list.Themes) != 1 || list.Themes[0].ID != "paper" {
		t.Fatalf("DELETE status=%d got=%+v", code, list)
	}
	if code = serveCustomThemes(t, h, http.MethodDelete, "/settings/themes/sumi", "", nil); code != http.StatusNotFound {
		t.Fatalf("second DELETE status=%d", code)
	}
}

func TestCustomThemesImportRefusesBadFiles(t *testing.T) {
	h := customThemeRouter(&memoryCustomThemeStore{value: "{}"})
	for name, body := range map[string]string{
		"no file":     `{}`,
		"remote css":  `{"file":{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"},"css":"a{background:url(https://evil.example/a.png)}"}}`,
		"not a color": `{"file":{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"var(--x)"}}}`,
		"bad mode":    `{"file":{"version":1,"name":"x","appearance":"dark","colors":{"canvas":"#000"}},"use":true,"mode":"dim"}`,
	} {
		if code := serveCustomThemes(t, h, http.MethodPost, "/settings/themes", body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", name, code)
		}
	}
}

func TestCustomThemesWithoutServiceIsNotImplemented(t *testing.T) {
	r := chi.NewRouter()
	(&CustomThemeController{}).Register(r)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/settings/themes"},
		{http.MethodPost, "/settings/themes"},
		{http.MethodDelete, "/settings/themes/sumi"},
		{http.MethodPut, "/settings/theme-switch"},
	} {
		if code := serveCustomThemes(t, r, tc.method, tc.path, "{}", nil); code != http.StatusNotImplemented {
			t.Errorf("%s %s: status=%d, want 501", tc.method, tc.path, code)
		}
	}
}
