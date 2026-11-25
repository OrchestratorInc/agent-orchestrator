package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/apptheme"
)

// CustomThemeService is the controller-facing custom theme contract
// (internal/service/apptheme).
type CustomThemeService interface {
	Get(ctx context.Context) (apptheme.Library, error)
	Import(ctx context.Context, file map[string]any, opts apptheme.ImportOptions) (apptheme.Imported, error)
	Delete(ctx context.Context, id string) (apptheme.Library, error)
	Use(ctx context.Context, sw apptheme.Switch) (apptheme.Library, error)
}

// maxCustomThemeBody fits the largest theme CSS (2 MiB, mostly an embedded
// font or image) plus its colors after JSON escaping. Go's encoder, which the
// CLI sends with, writes each of < > & as six bytes, and an inline SVG is
// mostly those.
const maxCustomThemeBody = 16 << 20

// CustomThemeController owns /settings/themes: the theme files the user
// imports or their agents make through `ao theme`, and the switch requests
// that show them.
type CustomThemeController struct {
	Svc CustomThemeService
}

// Register mounts the custom theme routes.
func (c *CustomThemeController) Register(r chi.Router) {
	r.Get("/settings/themes", c.list)
	r.Post("/settings/themes", c.importFile)
	r.Delete("/settings/themes/{themeId}", c.delete)
	r.Put("/settings/theme-switch", c.use)
}

func (c *CustomThemeController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/themes")
		return
	}
	writeCustomThemes(w, r)(c.Svc.Get(r.Context()))
}

func (c *CustomThemeController) importFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/settings/themes")
		return
	}
	var req ImportCustomThemeRequest
	if !decodeConversationBodyLimit(w, r, &req, maxCustomThemeBody) {
		return
	}
	var sw *apptheme.Switch
	if req.Use {
		sw = &apptheme.Switch{Mode: req.Mode}
	}
	imported, err := c.Svc.Import(r.Context(), req.File, apptheme.ImportOptions{ID: req.ID, KeepExisting: req.KeepExisting, IfUpdatedAt: req.IfUpdatedAt, Switch: sw})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	warnings := imported.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	library := customThemesResponse(imported.Library)
	envelope.WriteJSON(w, http.StatusOK, ImportCustomThemeResponse{
		Themes:   library.Themes,
		Switch:   library.Switch,
		Theme:    customThemeResponse(imported.Theme),
		Warnings: warnings,
	})
}

func (c *CustomThemeController) delete(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/settings/themes/{themeId}")
		return
	}
	writeCustomThemes(w, r)(c.Svc.Delete(r.Context(), chi.URLParam(r, "themeId")))
}

func (c *CustomThemeController) use(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/theme-switch")
		return
	}
	var req ThemeSwitchRequest
	if !decodeConversationBodyLimit(w, r, &req, 4<<10) {
		return
	}
	writeCustomThemes(w, r)(c.Svc.Use(r.Context(), apptheme.Switch{ThemeID: req.ThemeID, Mode: req.Mode}))
}

func writeCustomThemes(w http.ResponseWriter, r *http.Request) func(apptheme.Library, error) {
	return func(library apptheme.Library, err error) {
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, customThemesResponse(library))
	}
}

func customThemeResponse(theme apptheme.Theme) CustomThemeResponse {
	colors, variants := theme.Colors, theme.Variants
	if colors == nil {
		colors = map[string]string{}
	}
	if variants == nil {
		variants = map[string]map[string]string{}
	}
	return CustomThemeResponse{
		ID: theme.ID, Name: theme.Name, Appearance: theme.Appearance,
		Colors: colors, Variants: variants, CSS: theme.CSS, UpdatedAt: theme.UpdatedAt,
	}
}

func customThemesResponse(library apptheme.Library) CustomThemesResponse {
	themes := make([]CustomThemeResponse, 0, len(library.Themes))
	for _, theme := range library.Themes {
		themes = append(themes, customThemeResponse(theme))
	}
	return CustomThemesResponse{
		Themes: themes,
		Switch: ThemeSwitchResponse{ThemeID: library.Switch.ThemeID, Mode: library.Switch.Mode, ID: library.Switch.ID},
	}
}
