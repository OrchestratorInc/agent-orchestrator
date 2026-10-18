import { requireOptionalNativeModule } from "expo-modules-core";
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Appearance, Platform, useColorScheme } from "react-native";
import { themeFor, type ColorScheme, type Theme } from "./theme";
import {
	DEFAULT_PREFERENCE,
	DEFAULT_THEME_STYLE,
	nativeColorSchemeOverride,
	resolveScheme,
	type ThemePreference,
	type ThemeStyle,
} from "./themePreference";
import { saveThemePreference, saveThemeStyle, type ThemeSettings } from "./themeStore";

type ThemeState = {
	theme: Theme;
	scheme: ColorScheme;
	preference: ThemePreference;
	setPreference: (p: ThemePreference) => void;
	/** The named colour theme; Orchestrate unless the user picked another. */
	style: ThemeStyle;
	setStyle: (style: ThemeStyle) => void;
};

const ThemeContext = createContext<ThemeState | null>(null);

export function useTheme(): Theme {
	return useThemeState().theme;
}

export function useThemeState(): ThemeState {
	const ctx = useContext(ThemeContext);
	if (!ctx) throw new Error("useTheme must be used within <ThemeProvider>");
	return ctx;
}

/**
 * Styles that follow the theme.
 *
 * `StyleSheet.create` runs at module load, so a stylesheet that reads colours
 * directly is frozen to whichever palette was imported first — the reason light
 * mode is a migration rather than a token swap. Wrapping the sheet in a factory
 * defers it to render, and the memo keeps it to one rebuild per theme change
 * rather than one per render.
 *
 * The factory must be a module-level const, or its identity changes every
 * render and the memo never hits.
 */
export function useThemedStyles<T>(factory: (t: Theme) => T): T {
	const theme = useTheme();
	return useMemo(() => factory(theme), [factory, theme]);
}

/**
 * `initial` is the stored choice, read by the root layout while the splash is
 * still up (loadThemeSettings), so the first frame already draws the user's
 * theme. Reading it here after mount painted one frame of the default palette
 * first: a visible flash for anyone on a named theme or an explicit light/dark.
 */
export function ThemeProvider({ children, initial }: { children: ReactNode; initial?: ThemeSettings }) {
	const systemScheme = useColorScheme();
	const [preference, setPreferenceState] = useState<ThemePreference>(initial?.preference ?? DEFAULT_PREFERENCE);
	const [style, setStyleState] = useState<ThemeStyle>(initial?.style ?? DEFAULT_THEME_STYLE);

	const setPreference = useCallback((next: ThemePreference) => {
		// Optimistic: the switch should move under the finger, not after a write.
		setPreferenceState(next);
		void saveThemePreference(next);
	}, []);

	const setStyle = useCallback((next: ThemeStyle) => {
		setStyleState(next);
		void saveThemeStyle(next);
	}, []);

	useEffect(() => {
		Appearance.setColorScheme(nativeColorSchemeOverride(preference));
	}, [preference]);

	// Recomputed whenever the OS scheme changes, so a "system" preference follows
	// live instead of only at next launch.
	const scheme = resolveScheme(preference, systemScheme);

	// The window sits below the React tree, so a page transition that opens a gap
	// shows its colour — the platform default, white — and nothing inside the tree
	// can cover it. Keeping the window on the palette is what stops the white edge
	// around a screen as it moves.
	const theme = themeFor(scheme, style);
	const backgroundColor = theme.bgBase;
	useEffect(() => {
		// Optional, not required: this is a native module, and a build made before it
		// was added has no ExpoSystemUI to bind to. `expo-system-ui`'s own JS calls
		// requireNativeModule at import time and throws there — so importing the
		// package is exactly what broke the app on such a build. Asking the runtime
		// for the module directly returns null instead, and the only thing at stake
		// is whether the window is tinted.
		const systemUI = requireOptionalNativeModule<{ setBackgroundColorAsync(color: string): Promise<void> }>("ExpoSystemUI");
		if (systemUI) void systemUI.setBackgroundColorAsync(backgroundColor).catch(() => {});
	}, [backgroundColor]);

	// Android's navigation bar is system chrome, and it follows the *activity's*
	// theme rather than anything React Native draws. A phone in light mode with the
	// app in dark mode therefore got Android's light contrast scrim under the
	// gesture pill: a solid #e7e7e7 strip across the bottom of a dark screen. The
	// bar's content and scrim follow this call instead of the device setting.
	//
	// Guarded like the window colour above, because the module is native and a
	// build made before it would have nothing to bind to.
	useEffect(() => {
		if (Platform.OS !== "android") return;
		const navigationBar = requireOptionalNativeModule<{ setStyle(style: "light" | "dark"): Promise<void> }>("ExpoNavigationBar");
		void navigationBar?.setStyle(scheme === "dark" ? "light" : "dark").catch(() => {});
	}, [scheme]);

	const value = useMemo<ThemeState>(
		() => ({ theme, scheme, preference, setPreference, style, setStyle }),
		[theme, scheme, preference, setPreference, style, setStyle],
	);

	return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
