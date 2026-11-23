// Package apptheme owns the desktop app's custom themes: theme files the user
// imports in Settings or their agents make through `ao theme`, listed in
// Settings > Appearance next to the built-in themes.
//
// A theme is one JSON document, the thing people share:
//
//	{"version": 1, "name": "Sumi", "appearance": "dark",
//	 "colors": {"canvas": "#101012", "sidebar": "#3f0e40"},
//	 "variants": {"light": {"canvas": "#fafafa"}},
//	 "css": "..."}
//
// `colors` paints the theme's own appearance and `variants` the other one.
// The renderer turns roles into the app's tokens
// (frontend/src/renderer/lib/custom-theme.ts).
//
// The daemon keeps themes (not the renderer) so an agent can make one from any
// session and every window lists the same themes. Which theme is showing, and
// light or dark, stay renderer preferences: an agent can ask for a switch
// (Switch, with a new ID), which each window applies once, and the user's
// later picks in Settings stand.
//
// Theme CSS never loads a file: fonts and images travel inside it as data:
// URLs, so an imported or prompt-injected theme cannot send what is on screen
// to another host. The renderer enforces this again on the parsed stylesheet.
package apptheme

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// FileVersion is the theme file version this build reads and writes.
const FileVersion = 1

// BuiltInThemes are the renderer's named themes
// (packages/shared/theme/palettes.ts). A custom theme cannot take their ids.
var BuiltInThemes = []string{
	"orchestrate", "vercel", "github", "one", "catppuccin", "tokyo-night",
	"dracula", "nord", "gruvbox", "rose-pine", "solarized", "kanagawa",
	"vscode", "monokai", "material", "ayu", "night-owl", "everforest",
	"vitesse", "flexoki",
}

// ColorRoles are the names a theme file may color. Keep in step with
// THEME_COLOR_ROLES in frontend/src/renderer/lib/theme-file.ts.
var ColorRoles = []string{
	"canvas", "chrome", "toolbar", "toolbarForeground", "toolbarBorder",
	"toolbarControl", "toolbarControlForeground", "toolbarControlHover",
	"surface", "surfaceRaised", "surfaceOverlay",
	"text", "textMuted", "border", "input", "focus",
	"accent", "accentForeground", "secondary", "secondaryForeground",
	"muted", "mutedForeground", "placeholder", "secondaryLabel", "iconMuted",
	"error", "errorForeground", "errorSurface",
	"warning", "warningForeground", "warningSurface",
	"update", "updateForeground", "updateSurface",
	"accentSurface", "accentSurfaceForeground",
	"messageSurface", "messageForeground", "messageAction",
	"messageActionForeground", "messageActionHover",
	"codeBackground", "codeForeground",
	"searchMatchBackground", "searchMatchForeground",
	"searchMatchActiveBackground", "searchMatchActiveForeground",
	"sidebar", "sidebarForeground", "sidebarMutedForeground",
	"sidebarControlSurface", "sidebarRowHover", "sidebarRowActive",
	"sidebarRowSelected", "sidebarBorder",
	"terminalBackground", "terminalForeground", "terminalCursor",
	"terminalSelection", "terminalScrollbar", "terminalScrollbarHover",
	"link", "red", "orange", "yellow", "green", "blue", "purple", "cyan",
}

// UnpaintedRoles are accepted and kept, so a shared file that sets them
// imports whole and exports unchanged, but the desktop app has no surface for
// them.
// Keep in step with UNPAINTED_THEME_ROLES in
// frontend/src/renderer/lib/theme-file.ts.
var UnpaintedRoles = []string{
	"toolbar", "toolbarForeground", "toolbarBorder", "toolbarControl",
	"toolbarControlForeground", "toolbarControlHover",
	"secondaryLabel", "iconMuted",
	"errorForeground", "errorSurface", "warningForeground", "warningSurface",
	"update", "updateForeground", "updateSurface",
	"accentSurface", "accentSurfaceForeground",
	"messageAction", "messageActionForeground", "messageActionHover",
	"searchMatchBackground", "searchMatchForeground",
	"searchMatchActiveBackground", "searchMatchActiveForeground",
	"terminalScrollbar", "terminalScrollbarHover",
}

// Theme is one custom theme, in the shape of its file.
type Theme struct {
	// ID names the theme in commands ("sumi"); never a built-in id.
	ID string `json:"id"`
	// Name is shown in the theme picker.
	Name string `json:"name"`
	// Appearance is the one Colors paints: dark or light.
	Appearance string `json:"appearance"`
	// Colors maps roles ("canvas") to literal CSS colors.
	Colors map[string]string `json:"colors"`
	// Variants holds the other appearance's colors, keyed by its name.
	Variants map[string]map[string]string `json:"variants,omitempty"`
	// CSS applies while the theme is showing, in both appearances.
	CSS string `json:"css,omitempty"`
	// UpdatedAt changes on every save.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Switch is the latest theme or appearance an agent asked every window to
// show.
type Switch struct {
	// ThemeID is a built-in or custom theme id; empty keeps the current one.
	ThemeID string `json:"themeId,omitempty"`
	// Mode is light, dark or system; empty keeps the current one.
	Mode string `json:"mode,omitempty"`
	// ID changes with every request, so each window applies it once.
	ID string `json:"id,omitempty"`
}

// Library is everything stored: the custom themes and the latest switch.
type Library struct {
	Themes []Theme `json:"themes"`
	Switch Switch  `json:"switch"`
}

// Limits keep a theme a stylesheet: room for an embedded font or image, not an
// asset store.
const (
	MaxThemes       = 50
	MaxCSSBytes     = 2 << 20
	MaxLibraryBytes = 12 << 20
	maxNameRunes    = 48
	maxColorBytes   = 96
)

var (
	themeIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	switchIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// A literal color only: no var(), color-mix() or url(). The renderer
	// parses it for real and skips what it cannot resolve.
	colorPattern = regexp.MustCompile(`^(?:#(?:[0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})|(?:rgba?|hsla?|oklch|oklab)\([0-9a-z.,%/ +-]{1,80}\)|black|white|transparent)$`)
	nonIDRun     = regexp.MustCompile(`[^a-z0-9]+`)
)

// Store is the durable surface: the library as JSON, "{}" for none.
type Store interface {
	GetCustomThemes(ctx context.Context) (string, error)
	SetCustomThemes(ctx context.Context, value string) error
}

// Service reads and writes custom themes.
type Service struct {
	store Store
	now   func() time.Time
	// mu makes each change one read-modify-write of the stored library, so an
	// agent's save and a save from Settings cannot drop each other's theme.
	mu sync.Mutex
}

// New builds the service.
func New(store Store, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, now: now}
}

// Get returns the custom themes and the latest switch.
func (s *Service) Get(ctx context.Context) (Library, error) {
	raw, err := s.store.GetCustomThemes(ctx)
	if err != nil {
		return Library{}, err
	}
	library := Library{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &library); err != nil {
			return Library{}, fmt.Errorf("decode custom themes: %w", err)
		}
	}
	if library.Themes == nil {
		library.Themes = []Theme{}
	}
	return library, nil
}

// Imported is the result of importing a theme file.
type Imported struct {
	Library Library
	// Theme is the theme as stored.
	Theme Theme
	// Warnings name parts of the file that were left out, such as a color
	// role this build does not know.
	Warnings []string
}

// ImportOptions change how Import stores a file.
type ImportOptions struct {
	// ID overrides the file's own; with neither, the id comes from the name.
	ID string
	// KeepExisting refuses the import when a custom theme already has the id,
	// instead of replacing it. Settings imports this way, so a shared file
	// cannot silently overwrite a theme that happens to share its name.
	KeepExisting bool
	// IfUpdatedAt, when set, makes the import conditional on what the caller
	// last read: "" means no theme has the id yet, anything else is that
	// theme's UpdatedAt (RFC 3339 with nanoseconds). A caller that reads a
	// theme, changes part of it and writes it back (`ao theme set`) passes it,
	// so it cannot undo an edit or a delete made in between.
	IfUpdatedAt *string
	// Switch also asks every window to switch (ThemeID empty means this
	// theme, ID empty gets a new id).
	Switch *Switch
}

// Revision is the value IfUpdatedAt is compared with for a stored theme.
func Revision(theme Theme) string {
	return theme.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// Import stores a theme file. It replaces the custom theme with the same id
// unless opts.KeepExisting is set.
func (s *Service) Import(ctx context.Context, file map[string]any, opts ImportOptions) (Imported, error) {
	theme, warnings, err := ParseFile(file, opts.ID)
	if err != nil {
		return Imported{}, err
	}
	sw := opts.Switch
	s.mu.Lock()
	defer s.mu.Unlock()
	library, err := s.Get(ctx)
	if err != nil {
		return Imported{}, err
	}
	theme.UpdatedAt = s.now()
	index := slices.IndexFunc(library.Themes, func(t Theme) bool { return t.ID == theme.ID })
	if opts.IfUpdatedAt != nil {
		current := ""
		if index >= 0 {
			current = Revision(library.Themes[index])
		}
		if current != *opts.IfUpdatedAt {
			return Imported{}, apierr.Conflict("CUSTOM_THEME_CHANGED", fmt.Sprintf("the custom theme %q changed since it was read; read it again and reapply the change", theme.ID), map[string]any{"themeId": theme.ID})
		}
	}
	if index >= 0 {
		if opts.KeepExisting {
			return Imported{}, apierr.Conflict("CUSTOM_THEME_EXISTS", fmt.Sprintf("there is already a custom theme %q (%s); importing this file would replace it", library.Themes[index].Name, theme.ID), map[string]any{"themeId": theme.ID})
		}
		library.Themes[index] = theme
	} else {
		if len(library.Themes) >= MaxThemes {
			return Imported{}, invalid(fmt.Sprintf("there are already %d custom themes; delete one first", MaxThemes))
		}
		library.Themes = append(library.Themes, theme)
	}
	if sw != nil {
		next := *sw
		if next.ThemeID == "" {
			next.ThemeID = theme.ID
		}
		if next.ID == "" {
			next.ID = s.newSwitchID()
		}
		if library.Switch, err = normalizeSwitch(next, library.Themes); err != nil {
			return Imported{}, err
		}
	}
	if library, err = s.write(ctx, library); err != nil {
		return Imported{}, err
	}
	return Imported{Library: library, Theme: theme, Warnings: warnings}, nil
}

// Delete removes a custom theme. Windows showing it fall back to the default
// theme.
func (s *Service) Delete(ctx context.Context, id string) (Library, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	library, err := s.Get(ctx)
	if err != nil {
		return Library{}, err
	}
	index := slices.IndexFunc(library.Themes, func(t Theme) bool { return t.ID == id })
	if index < 0 {
		return Library{}, apierr.NotFound("CUSTOM_THEME_NOT_FOUND", fmt.Sprintf("there is no custom theme %q", id))
	}
	library.Themes = slices.Delete(library.Themes, index, index+1)
	if library.Switch.ThemeID == id {
		library.Switch = Switch{}
	}
	return s.write(ctx, library)
}

// Use asks every window to show a theme (built-in or custom) and/or an
// appearance.
func (s *Service) Use(ctx context.Context, sw Switch) (Library, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	library, err := s.Get(ctx)
	if err != nil {
		return Library{}, err
	}
	if sw.ID == "" {
		sw.ID = s.newSwitchID()
	}
	if library.Switch, err = normalizeSwitch(sw, library.Themes); err != nil {
		return Library{}, err
	}
	return s.write(ctx, library)
}

// newSwitchID is unique per request, so a window that applied the last one
// applies this one too.
func (s *Service) newSwitchID() string {
	return strconv.FormatInt(s.now().UnixNano(), 36)
}

func (s *Service) write(ctx context.Context, library Library) (Library, error) {
	data, err := json.Marshal(library)
	if err != nil {
		return Library{}, fmt.Errorf("encode custom themes: %w", err)
	}
	if len(data) > MaxLibraryBytes {
		return Library{}, invalid(fmt.Sprintf("custom themes would take %d bytes; at most %d are allowed, so delete a theme or embed smaller files", len(data), MaxLibraryBytes))
	}
	if err := s.store.SetCustomThemes(ctx, string(data)); err != nil {
		return Library{}, err
	}
	return library, nil
}

// File is the theme as its shareable file: the stored theme plus the version,
// without the save time.
func File(theme Theme) map[string]any {
	file := map[string]any{
		"version":    FileVersion,
		"id":         theme.ID,
		"name":       theme.Name,
		"appearance": theme.Appearance,
		"colors":     theme.Colors,
	}
	if len(theme.Variants) > 0 {
		file["variants"] = theme.Variants
	}
	if theme.CSS != "" {
		file["css"] = theme.CSS
	}
	return file
}

// ParseFile validates a decoded theme file. id overrides the file's own; with
// neither, the id comes from the name. Keys it does not know are ignored, and
// color roles it does not know are left out and named in the warnings.
func ParseFile(file map[string]any, id string) (Theme, []string, error) {
	if file == nil {
		return Theme{}, nil, invalid("a theme file is a JSON object")
	}
	if version, ok := file["version"].(float64); !ok || version != FileVersion {
		return Theme{}, nil, invalid(fmt.Sprintf(`a theme file needs "version": %d`, FileVersion))
	}
	name, _ := file["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "\n\r\x00") || len([]rune(name)) > maxNameRunes {
		return Theme{}, nil, invalid(fmt.Sprintf(`a theme file needs a "name" of one line, at most %d characters`, maxNameRunes))
	}
	if strings.TrimSpace(id) == "" {
		id, _ = file["id"].(string)
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		id = IDFromName(name)
	}
	if !themeIDPattern.MatchString(id) {
		return Theme{}, nil, invalid(fmt.Sprintf("%q is not a theme id; use lowercase letters, digits and hyphens, e.g. sumi", id))
	}
	if slices.Contains(BuiltInThemes, id) {
		return Theme{}, nil, invalid(fmt.Sprintf("%q is a built-in theme; give the theme another id or name", id))
	}
	appearance, _ := file["appearance"].(string)
	if appearance != "dark" && appearance != "light" {
		return Theme{}, nil, invalid(`a theme file needs "appearance": "dark" or "light", the one its colors paint`)
	}
	var warnings []string
	colors, err := parseColors(appearance, file["colors"], &warnings)
	if err != nil {
		return Theme{}, nil, err
	}
	if len(colors) == 0 {
		return Theme{}, nil, invalid(`a theme file needs "colors" with at least one role, e.g. {"canvas": "#101012"}`)
	}
	other := "light"
	if appearance == "light" {
		other = "dark"
	}
	var variants map[string]map[string]string
	if raw, present := file["variants"]; present && raw != nil {
		object, ok := raw.(map[string]any)
		if !ok {
			return Theme{}, nil, invalid(`"variants" must be an object, e.g. {"` + other + `": {"canvas": "#fafafa"}}`)
		}
		for key, value := range object {
			if key != other {
				return Theme{}, nil, invalid(fmt.Sprintf(`"variants" may only hold %q, the appearance "colors" does not paint; found %q`, other, key))
			}
			variant, err := parseColors(key, value, &warnings)
			if err != nil {
				return Theme{}, nil, err
			}
			if len(variant) > 0 {
				variants = map[string]map[string]string{key: variant}
			}
		}
	}
	css := ""
	if raw, present := file["css"]; present && raw != nil {
		text, ok := raw.(string)
		if !ok {
			return Theme{}, nil, invalid(`"css" must be a string`)
		}
		if css, err = normalizeCSS(text); err != nil {
			return Theme{}, nil, err
		}
	}
	sort.Strings(warnings)
	warnings = slices.Compact(warnings)
	if note := unpaintedNote(colors, variants[other]); note != "" {
		warnings = append(warnings, note)
	}
	return Theme{ID: id, Name: name, Appearance: appearance, Colors: colors, Variants: variants, CSS: css}, warnings, nil
}

func parseColors(appearance string, raw any, warnings *[]string) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid(fmt.Sprintf("the %s colors must be an object of role: color", appearance))
	}
	colors := make(map[string]string, len(object))
	for role, value := range object {
		if !slices.Contains(ColorRoles, role) {
			*warnings = append(*warnings, fmt.Sprintf("left out %q: not a color role (see the roles in the ao theme guide)", role))
			continue
		}
		text, _ := value.(string)
		text = strings.TrimSpace(text)
		if len(text) > maxColorBytes || !colorPattern.MatchString(strings.ToLower(text)) {
			return nil, invalid(fmt.Sprintf("%s %s must be a literal color such as #101012 or oklch(0.2 0.01 250), not %q", appearance, role, clip(fmt.Sprint(value))))
		}
		colors[role] = text
	}
	return colors, nil
}

// unpaintedNote says which of a theme's roles change nothing in this app, so
// neither a person nor an agent is left wondering why a color did not move.
func unpaintedNote(sets ...map[string]string) string {
	var found []string
	for _, role := range UnpaintedRoles {
		for _, colors := range sets {
			if _, ok := colors[role]; ok {
				found = append(found, role)
				break
			}
		}
	}
	if len(found) == 0 {
		return ""
	}
	const shown = 4
	list := strings.Join(found[:min(len(found), shown)], ", ")
	if len(found) > shown {
		list += fmt.Sprintf(" and %d more", len(found)-shown)
	}
	return "kept, but Orchestrator has no surface for: " + list
}

// IDFromName derives a theme id from its name: "One Pro Night" gives
// "one-pro-night". A name with no ASCII letters or digits gets a stable id
// from its hash.
func IDFromName(name string) string {
	label := strings.ToLower(strings.TrimSpace(name))
	id := strings.Trim(nonIDRun.ReplaceAllString(label, "-"), "-")
	if len(id) > 48 {
		id = strings.Trim(id[:48], "-")
	}
	if id != "" {
		return id
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(label))
	return "theme-" + strconv.FormatUint(uint64(hash.Sum32()), 36)
}

func normalizeSwitch(sw Switch, themes []Theme) (Switch, error) {
	themeID := strings.ToLower(strings.TrimSpace(sw.ThemeID))
	if themeID != "" && !slices.Contains(BuiltInThemes, themeID) && !slices.ContainsFunc(themes, func(t Theme) bool { return t.ID == themeID }) {
		known := slices.Clone(BuiltInThemes)
		for _, t := range themes {
			known = append(known, t.ID)
		}
		return Switch{}, invalid(fmt.Sprintf("there is no theme %q; available: %s", sw.ThemeID, strings.Join(known, ", ")))
	}
	mode := strings.ToLower(strings.TrimSpace(sw.Mode))
	switch mode {
	case "", "light", "dark", "system":
	default:
		return Switch{}, invalid(fmt.Sprintf("mode must be light, dark or system, not %q", sw.Mode))
	}
	if themeID == "" && mode == "" {
		return Switch{}, invalid("name a theme, a mode, or both")
	}
	id := strings.TrimSpace(sw.ID)
	if !switchIDPattern.MatchString(id) {
		return Switch{}, invalid("the switch id must be 1-64 letters, digits, - or _")
	}
	return Switch{ThemeID: themeID, Mode: mode, ID: id}, nil
}

func normalizeCSS(css string) (string, error) {
	css = strings.TrimSpace(css)
	if css == "" {
		return "", nil
	}
	if len(css) > MaxCSSBytes {
		return "", invalid(fmt.Sprintf("theme CSS is %d bytes; at most %d are allowed", len(css), MaxCSSBytes))
	}
	if strings.ContainsRune(css, 0) {
		return "", invalid("theme CSS contains a NUL byte")
	}
	if problem := cssLoadsFile(css); problem != "" {
		return "", invalid("theme CSS " + problem)
	}
	return css, nil
}

// clip keeps an echoed value short enough for an error message.
func clip(text string) string {
	if len(text) <= 80 {
		return text
	}
	return text[:80] + "..."
}

func invalid(message string) error {
	return apierr.Invalid("CUSTOM_THEME_INVALID", message, nil)
}
