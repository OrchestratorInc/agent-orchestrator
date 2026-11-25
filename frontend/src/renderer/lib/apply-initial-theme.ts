import { aoBridge } from "./bridge";
import { applyDocumentTheme, applyDocumentThemeStyle, readStoredThemePreference, readStoredThemeStyle, resolveTheme } from "./theme";
import { applyWindowTranslucency, readStoredWindowTranslucency } from "./window-translucency";

// Runs as the first main.tsx import, before styles.css, so data-theme and
// data-style-theme are set before token CSS paints (avoids a flash on load).
// Window transparency applies here too, so the startup loader is translucent
// and main sets vibrancy before it shows the window. The theme preference goes
// to main now as well: the vibrancy material follows nativeTheme, and the
// shell only sends it after the loader, so a light app on a dark Mac (or the
// reverse) would otherwise show its loader over the wrong material.
const themePreference = readStoredThemePreference();
applyDocumentTheme(resolveTheme(themePreference));
// A custom theme (`ao theme`) paints from the copy saved when it last showed;
// the shell swaps in the daemon's current one once it answers.
applyDocumentThemeStyle(readStoredThemeStyle());
applyWindowTranslucency(readStoredWindowTranslucency());
void aoBridge.theme?.set(themePreference)?.catch(() => undefined);
