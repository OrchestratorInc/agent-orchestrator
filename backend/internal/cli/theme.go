package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	customThemesPath = "settings/themes"
	themeSwitchPath  = "settings/theme-switch"
	themeFileVersion = 1
	// maxThemeFileBytes is the daemon's request limit: a theme's 2 MiB of CSS
	// can take several times that once JSON has escaped an inline SVG.
	maxThemeFileBytes = 16 << 20
	// themeSetAttempts bounds how often `set` starts over after another
	// writer changed the theme between its read and its write.
	themeSetAttempts = 4
)

// customThemeDTO mirrors the daemon's CustomThemeResponse. The CLI keeps its
// own copies so it need not import httpd.
type customThemeDTO struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Appearance string                       `json:"appearance"`
	Colors     map[string]string            `json:"colors"`
	Variants   map[string]map[string]string `json:"variants"`
	CSS        string                       `json:"css"`
	UpdatedAt  time.Time                    `json:"updatedAt"`
}

type themeSwitchDTO struct {
	ThemeID string `json:"themeId"`
	Mode    string `json:"mode"`
	ID      string `json:"id"`
}

type customThemesDTO struct {
	Themes []customThemeDTO `json:"themes"`
	Switch themeSwitchDTO   `json:"switch"`
}

// importThemeRequest mirrors ImportCustomThemeRequest.
type importThemeRequest struct {
	File        map[string]any `json:"file"`
	ID          string         `json:"id,omitempty"`
	IfUpdatedAt *string        `json:"ifUpdatedAt,omitempty"`
	Use         bool           `json:"use,omitempty"`
	Mode        string         `json:"mode,omitempty"`
}

type importThemeResponse struct {
	Theme    customThemeDTO `json:"theme"`
	Warnings []string       `json:"warnings"`
}

type themeSwitchRequest struct {
	ThemeID string `json:"themeId,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// builtInThemeNames are the desktop app's built-in themes
// (packages/shared/theme/palettes.ts).
const builtInThemeNames = "orchestrate, vercel, github, one, catppuccin, tokyo-night, dracula, nord, gruvbox, rose-pine, solarized, kanagawa, vscode, monokai, material, ayu, night-owl, everforest, vitesse, flexoki"

func newThemeCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Make, share and switch AO desktop app themes",
		Long: "Make custom themes for the AO desktop app and switch every open window to\n" +
			"one. A custom theme appears in Settings > Appearance next to the built-in\n" +
			"themes; the user can switch away from it, export it or delete it there.\n\n" +
			"A theme is one JSON file: a name, the appearance its colors paint (dark or\n" +
			"light), colors by role (canvas, sidebar, text, accent, ...), optionally the\n" +
			"other appearance's colors under variants, and optionally CSS. Every role is\n" +
			"optional; what a theme leaves out keeps Orchestrator's default look.\n\n" +
			"Themes never load files: embed fonts and images in the CSS as data: URLs.",
		Example: `  ao theme
  ao theme set sumi --name "Sumi" --dark canvas=#101012 --dark sidebar=#3f0e40 --light canvas=#fafafa --use
  ao theme set sumi --dark accent=#e0a84f
  ao theme show sumi > sumi.json
  ao theme import sumi.json --use
  ao theme use nord --mode dark
  ao theme mode light
  ao theme delete sumi`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listThemes(cmd.Context(), cmd.OutOrStdout(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")

	var listJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List custom and built-in themes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listThemes(cmd.Context(), cmd.OutOrStdout(), listJSON)
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "print JSON")
	cmd.AddCommand(list)

	var showOut string
	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Print a custom theme's file, ready to share or edit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			theme, err := ctx.findTheme(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// Not json.Marshal: it writes < > & as \u003c and so on, which makes
			// an inline SVG unreadable and several times its size.
			var buffer bytes.Buffer
			encoder := json.NewEncoder(&buffer)
			encoder.SetEscapeHTML(false)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(themeFile(theme)); err != nil {
				return err
			}
			data := buffer.Bytes()
			if showOut == "" {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			if err := os.WriteFile(showOut, data, 0o644); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s to %s.\n", theme.ID, showOut)
			return err
		},
	}
	show.Flags().StringVarP(&showOut, "out", "o", "", "write the file here instead of printing it")
	cmd.AddCommand(show)

	var importID, importMode string
	var importUse bool
	importCmd := &cobra.Command{
		Use:   "import <file|->",
		Short: "Add a theme file to the theme picker, replacing the theme with the same id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := themeUseMode(importUse, importMode)
			if err != nil {
				return err
			}
			file, err := readThemeFile(cmd.InOrStdin(), args[0])
			if err != nil {
				return err
			}
			return ctx.importTheme(cmd, importThemeRequest{File: file, ID: strings.TrimSpace(importID), Use: importUse, Mode: mode})
		},
	}
	importCmd.Flags().StringVar(&importID, "id", "", "store it under this id instead of the file's")
	importCmd.Flags().BoolVar(&importUse, "use", false, "switch every window to the theme")
	importCmd.Flags().StringVar(&importMode, "mode", "", "with --use, also switch to light, dark or system")
	cmd.AddCommand(importCmd)

	cmd.AddCommand(newThemeSetCommand(ctx))

	var useMode string
	use := &cobra.Command{
		Use:   "use <id>",
		Short: "Switch every window to a built-in or custom theme",
		Long:  "Switch every open window to a theme: a custom one, or a built-in one (" + builtInThemeNames + ").",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := themeMode(useMode)
			if err != nil {
				return err
			}
			id := strings.TrimSpace(args[0])
			var library customThemesDTO
			if err := ctx.putJSON(cmd.Context(), themeSwitchPath, themeSwitchRequest{ThemeID: id, Mode: mode}, &library); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Every window switched to %s%s.\n", id, themeModeSuffix(mode))
			return err
		},
	}
	use.Flags().StringVar(&useMode, "mode", "", "also switch to light, dark or system")
	cmd.AddCommand(use)

	cmd.AddCommand(&cobra.Command{
		Use:   "mode <light|dark|system>",
		Short: "Switch every window between light and dark",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := themeMode(args[0])
			if err != nil {
				return err
			}
			if mode == "" {
				return usageError{errors.New("mode must be light, dark or system")}
			}
			var library customThemesDTO
			if err := ctx.putJSON(cmd.Context(), themeSwitchPath, themeSwitchRequest{Mode: mode}, &library); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Every window switched to %s.\n", mode)
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a custom theme; windows showing it go back to the default theme",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			var library customThemesDTO
			if err := ctx.deleteJSON(cmd.Context(), customThemesPath+"/"+url.PathEscape(id), &library); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", id)
			return err
		},
	})
	return cmd
}

// newThemeSetCommand changes part of a theme without rewriting its file by
// hand, and makes the theme when it does not exist yet.
func newThemeSetCommand(ctx *commandContext) *cobra.Command {
	var (
		name, appearance, cssSource, mode string
		both, dark, light                 []string
		clearCSS, use                     bool
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Make a custom theme, or change some of its colors, name or CSS",
		Long: "Make a custom theme, or change part of one and keep the rest. Colors are\n" +
			"<role>=<color> pairs: --dark and --light for one appearance, --set for both.\n" +
			"A color is any literal CSS color (#101012, oklch(0.2 0.01 250)); an empty\n" +
			"value (<role>=) removes the role.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switchMode, err := themeUseMode(use, mode)
			if err != nil {
				return err
			}
			if clearCSS && cssSource != "" {
				return usageError{errors.New("give --css or --clear-css, not both")}
			}
			if appearance != "" && appearance != "dark" && appearance != "light" {
				return usageError{fmt.Errorf("--appearance must be dark or light, not %q", appearance)}
			}
			id := strings.ToLower(strings.TrimSpace(args[0]))
			// Read once: stdin cannot be read again if the write has to start over.
			newCSS := ""
			if cssSource != "" && !clearCSS {
				text, err := readThemeText(cmd.InOrStdin(), cssSource)
				if err != nil {
					return err
				}
				newCSS = text
			}
			// The change is applied to the theme as just read and written back
			// only if it is still as read, so two `set` commands at once (or a
			// delete in between) cannot undo each other.
			build := func() (importThemeRequest, error) {
				library, err := ctx.getThemes(cmd.Context())
				if err != nil {
					return importThemeRequest{}, err
				}
				theme := customThemeDTO{ID: id, Name: id, Appearance: "dark"}
				existing := false
				for _, candidate := range library.Themes {
					if candidate.ID == id {
						theme, existing = candidate, true
					}
				}
				if !existing && len(dark) == 0 && len(both) == 0 && len(light) > 0 {
					theme.Appearance = "light"
				}
				if appearance != "" && appearance != theme.Appearance {
					if existing {
						return importThemeRequest{}, usageError{fmt.Errorf("%s paints %s; its appearance cannot change (make a new theme instead)", id, theme.Appearance)}
					}
					theme.Appearance = appearance
				}
				if strings.TrimSpace(name) != "" {
					theme.Name = strings.TrimSpace(name)
				}
				colors := map[string]map[string]string{"dark": {}, "light": {}}
				other := "light"
				if theme.Appearance == "light" {
					other = "dark"
				}
				for role, value := range theme.Colors {
					colors[theme.Appearance][role] = value
				}
				for role, value := range theme.Variants[other] {
					colors[other][role] = value
				}
				for _, group := range []struct {
					pairs []string
					modes []string
				}{{both, []string{"dark", "light"}}, {dark, []string{"dark"}}, {light, []string{"light"}}} {
					for _, pair := range group.pairs {
						role, value, ok := strings.Cut(pair, "=")
						role, value = strings.TrimSpace(role), strings.TrimSpace(value)
						if !ok || role == "" {
							return importThemeRequest{}, usageError{fmt.Errorf("%q is not <role>=<color>, e.g. canvas=#101012", pair)}
						}
						for _, target := range group.modes {
							if value == "" {
								delete(colors[target], role)
							} else {
								colors[target][role] = value
							}
						}
					}
				}
				theme.Colors = colors[theme.Appearance]
				theme.Variants = nil
				if len(colors[other]) > 0 {
					theme.Variants = map[string]map[string]string{other: colors[other]}
				}
				switch {
				case clearCSS:
					theme.CSS = ""
				case cssSource != "":
					theme.CSS = newCSS
				}
				revision := ""
				if existing {
					revision = theme.UpdatedAt.UTC().Format(time.RFC3339Nano)
				}
				return importThemeRequest{File: themeFile(theme), IfUpdatedAt: &revision, Use: use, Mode: switchMode}, nil
			}
			for attempt := 1; ; attempt++ {
				req, err := build()
				if err != nil {
					return err
				}
				err = ctx.importTheme(cmd, req)
				var apiErr apiResponseError
				if attempt < themeSetAttempts && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict && apiErr.ErrorBody.Code == "CUSTOM_THEME_CHANGED" {
					continue
				}
				return err
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name shown in the theme picker (a new theme defaults to its id)")
	cmd.Flags().StringVar(&appearance, "appearance", "", "for a new theme, the appearance its main colors paint: dark (default) or light")
	cmd.Flags().StringArrayVar(&both, "set", nil, "color for both appearances, <role>=<color> (repeatable)")
	cmd.Flags().StringArrayVar(&dark, "dark", nil, "color for the dark appearance, <role>=<color> (repeatable)")
	cmd.Flags().StringArrayVar(&light, "light", nil, "color for the light appearance, <role>=<color> (repeatable)")
	cmd.Flags().StringVar(&cssSource, "css", "", "file with the theme's CSS, - for stdin; replaces the CSS it has")
	cmd.Flags().BoolVar(&clearCSS, "clear-css", false, "remove the theme's CSS")
	cmd.Flags().BoolVar(&use, "use", false, "switch every window to the theme")
	cmd.Flags().StringVar(&mode, "mode", "", "with --use, also switch to light, dark or system")
	return cmd
}

func (c *commandContext) getThemes(ctx context.Context) (customThemesDTO, error) {
	var library customThemesDTO
	err := c.getJSON(ctx, customThemesPath, &library)
	return library, err
}

func (c *commandContext) findTheme(ctx context.Context, id string) (customThemeDTO, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	library, err := c.getThemes(ctx)
	if err != nil {
		return customThemeDTO{}, err
	}
	ids := make([]string, 0, len(library.Themes))
	for _, theme := range library.Themes {
		if theme.ID == id {
			return theme, nil
		}
		ids = append(ids, theme.ID)
	}
	if len(ids) == 0 {
		return customThemeDTO{}, usageError{fmt.Errorf("there is no custom theme %q; there are none yet (built-in themes have no file)", id)}
	}
	return customThemeDTO{}, usageError{fmt.Errorf("there is no custom theme %q; custom themes: %s", id, strings.Join(ids, ", "))}
}

func (c *commandContext) importTheme(cmd *cobra.Command, req importThemeRequest) error {
	var res importThemeResponse
	if err := c.postJSON(cmd.Context(), customThemesPath, req, &res); err != nil {
		return err
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
	}
	theme := res.Theme
	other := "light"
	if theme.Appearance == "light" {
		other = "dark"
	}
	parts := []string{fmt.Sprintf("%s %s", theme.Appearance, countColors(len(theme.Colors)))}
	if variant := theme.Variants[other]; len(variant) > 0 {
		parts = append(parts, fmt.Sprintf("%s %s", other, countColors(len(variant))))
	} else {
		parts = append(parts, "no "+other+" colors (that appearance keeps the default look)")
	}
	if theme.CSS != "" {
		parts = append(parts, fmt.Sprintf("CSS %.1f KiB", float64(len(theme.CSS))/1024))
	}
	message := fmt.Sprintf("Saved %s (%q): %s.", theme.ID, theme.Name, strings.Join(parts, ", "))
	if req.Use {
		message += fmt.Sprintf(" Every window switched to it%s.", themeModeSuffix(req.Mode))
	} else {
		message += fmt.Sprintf(" It is in the theme picker; `ao theme use %s` shows it.", theme.ID)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
	return err
}

func countColors(n int) string {
	if n == 1 {
		return "1 color"
	}
	return fmt.Sprintf("%d colors", n)
}

func (c *commandContext) listThemes(ctx context.Context, w io.Writer, asJSON bool) error {
	library, err := c.getThemes(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(w, library)
	}
	var b strings.Builder
	if len(library.Themes) == 0 {
		b.WriteString("Custom themes: none\n")
	} else {
		b.WriteString("Custom themes:\n")
		for _, theme := range library.Themes {
			appearances := theme.Appearance
			if len(theme.Variants) > 0 {
				appearances = "dark and light"
			}
			fmt.Fprintf(&b, "  %s  %q, %s\n", theme.ID, theme.Name, appearances)
		}
	}
	fmt.Fprintf(&b, "Built-in themes: %s\n", builtInThemeNames)
	if library.Switch.ID != "" {
		asked := library.Switch.ThemeID
		if asked == "" {
			asked = "the current theme"
		}
		fmt.Fprintf(&b, "Last switch asked for: %s%s\n", asked, themeModeSuffix(library.Switch.Mode))
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// themeFile is the theme as its shareable file.
func themeFile(theme customThemeDTO) map[string]any {
	file := map[string]any{
		"version":    themeFileVersion,
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

func readThemeText(stdin io.Reader, source string) (string, error) {
	var reader io.Reader = stdin
	if source != "-" {
		file, err := os.Open(source)
		if err != nil {
			return "", usageError{err}
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxThemeFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxThemeFileBytes {
		return "", usageError{fmt.Errorf("%s is larger than %d MiB", themeSourceName(source), maxThemeFileBytes>>20)}
	}
	return string(data), nil
}

func readThemeFile(stdin io.Reader, source string) (map[string]any, error) {
	text, err := readThemeText(stdin, source)
	if err != nil {
		return nil, err
	}
	var file map[string]any
	if err := json.Unmarshal([]byte(text), &file); err != nil {
		return nil, usageError{fmt.Errorf("%s is not a theme file (a JSON object): %w", themeSourceName(source), err)}
	}
	return file, nil
}

func themeSourceName(source string) string {
	if source == "-" {
		return "stdin"
	}
	return source
}

func themeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "light", "dark", "system":
		return mode, nil
	}
	return "", usageError{fmt.Errorf("mode must be light, dark or system, not %q", mode)}
}

// themeUseMode checks --mode for commands where it only applies with --use.
func themeUseMode(use bool, mode string) (string, error) {
	mode, err := themeMode(mode)
	if err != nil {
		return "", err
	}
	if mode != "" && !use {
		return "", usageError{errors.New("--mode switches windows, so it needs --use")}
	}
	return mode, nil
}

func themeModeSuffix(mode string) string {
	if mode == "" {
		return ""
	}
	return ", " + mode
}
