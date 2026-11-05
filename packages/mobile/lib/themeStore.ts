import AsyncStorage from "@react-native-async-storage/async-storage";
import {
	DEFAULT_PREFERENCE,
	DEFAULT_THEME_STYLE,
	isThemePreference,
	isThemeStyle,
	THEME_KEY,
	THEME_STYLE_KEY,
	type ThemePreference,
	type ThemeStyle,
} from "./themePreference";

// Storage side of the theme preference. Split from themePreference.ts so the
// decision logic there stays free of React Native imports and unit-testable —
// the same split as onboarding.ts / onboardingStore.ts.
//
// The preference is not a secret, so AsyncStorage (plaintext app sandbox) is
// right; SecureStore is reserved for the connection password.

/** Never throws — a storage failure reads as the default. */
export async function loadThemePreference(): Promise<ThemePreference> {
	try {
		const raw = await AsyncStorage.getItem(THEME_KEY);
		return isThemePreference(raw) ? raw : DEFAULT_PREFERENCE;
	} catch {
		return DEFAULT_PREFERENCE;
	}
}

export async function saveThemePreference(preference: ThemePreference): Promise<void> {
	try {
		await AsyncStorage.setItem(THEME_KEY, preference);
	} catch {
		/* a lost preference only means the picker resets next launch */
	}
}

/** Never throws; an unknown or retired theme id reads as the default. */
export async function loadThemeStyle(): Promise<ThemeStyle> {
	try {
		const raw = await AsyncStorage.getItem(THEME_STYLE_KEY);
		return isThemeStyle(raw) ? raw : DEFAULT_THEME_STYLE;
	} catch {
		return DEFAULT_THEME_STYLE;
	}
}

export async function saveThemeStyle(style: ThemeStyle): Promise<void> {
	try {
		await AsyncStorage.setItem(THEME_STYLE_KEY, style);
	} catch {
		/* a lost choice only means the picker resets next launch */
	}
}

export type ThemeSettings = { preference: ThemePreference; style: ThemeStyle };

/**
 * Both stored choices, read together so the root can hold the splash until they
 * are in and the first frame already draws the user's theme. Never throws: each
 * half falls back to its default.
 */
export async function loadThemeSettings(): Promise<ThemeSettings> {
	const [preference, style] = await Promise.all([loadThemePreference(), loadThemeStyle()]);
	return { preference, style };
}
