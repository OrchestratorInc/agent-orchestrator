import { aoBridge } from "./bridge";
import { applyDocumentTheme, applyDocumentThemeStyle, readStoredThemePreference, readStoredThemeStyle, resolveTheme } from "./theme";
import { applyWindowTranslucency, readStoredWindowTranslucency } from "./window-translucency";

const themePreference = readStoredThemePreference();
applyDocumentTheme(resolveTheme(themePreference));
applyDocumentThemeStyle(readStoredThemeStyle());
applyWindowTranslucency(readStoredWindowTranslucency());
void aoBridge.theme?.set(themePreference)?.catch(() => undefined);
