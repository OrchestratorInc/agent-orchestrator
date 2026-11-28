import { aoBridge } from "./bridge";
import { applyDocumentTheme, applyDocumentThemeStyle, readStoredThemePreference, readStoredThemeStyle, resolveTheme } from "./theme";
import { applyNewDesktopUi, readStoredNewDesktopUi } from "./new-desktop-ui";
import { applyWindowTranslucency, initializeWindowTranslucency, readStoredWindowTranslucency } from "./window-translucency";

// Runs as the first main.tsx import, before styles.css, so data-theme and
// data-style-theme are set before token CSS paints (avoids a flash on load).
// Send the theme before requesting transparency so the loader's native material
// follows the app appearance. Windows stays opaque until main accepts the request;
// macOS keeps its existing immediate, saved-preference-based loader appearance.
const themePreference = readStoredThemePreference();
applyDocumentTheme(resolveTheme(themePreference));
applyDocumentThemeStyle(readStoredThemeStyle());
applyNewDesktopUi(readStoredNewDesktopUi());
void aoBridge.theme?.set(themePreference)?.catch(() => undefined);
const translucent = readStoredWindowTranslucency();
initializeWindowTranslucency(translucent);
void applyWindowTranslucency(translucent);
