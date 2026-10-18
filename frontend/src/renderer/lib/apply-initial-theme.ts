import { applyDocumentTheme, applyDocumentThemeStyle, readStoredThemeStyle, resolveTheme } from "./theme";
import { applyWindowTranslucency, readStoredWindowTranslucency } from "./window-translucency";

// Runs as the first main.tsx import, before styles.css, so data-theme and
// data-style-theme are set before token CSS paints (avoids a flash on load).
// Window transparency applies here too, so the startup loader is translucent
// and main sets vibrancy before it shows the window.
applyDocumentTheme(resolveTheme());
applyDocumentThemeStyle(readStoredThemeStyle());
applyWindowTranslucency(readStoredWindowTranslucency());
