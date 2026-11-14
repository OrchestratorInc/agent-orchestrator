import { DEFAULT_THEME_STYLE, isThemeStyle, type ThemeStyle } from "./theme-palettes";
import { installThemeStylesheet } from "./theme-tokens";

export type Theme = "light" | "dark";
export type ThemePreference = Theme | "system";
export type { ThemeStyle };

export const themeStorageKey = "ao.theme";
export const themeStyleStorageKey = "ao.theme-style";

function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

export function systemTheme(): Theme {
	if (typeof window === "undefined") return "dark";
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function readStoredThemePreference(): ThemePreference {
	try {
		const stored = getLocalStorage()?.getItem(themeStorageKey);
		if (stored === "light" || stored === "dark" || stored === "system") return stored;
	} catch {
		// ignore
	}
	return "system";
}

/** Resolve the active light/dark appearance from a stored preference. */
export function resolveTheme(preference: ThemePreference = readStoredThemePreference()): Theme {
	if (preference === "system") return systemTheme();
	return preference;
}

export function readStoredThemeStyle(): ThemeStyle {
	try {
		const stored = getLocalStorage()?.getItem(themeStyleStorageKey);
		if (isThemeStyle(stored)) return stored;
	} catch {
		// ignore
	}
	return DEFAULT_THEME_STYLE;
}

export function applyDocumentTheme(theme: Theme): void {
	if (typeof document === "undefined") return;
	document.documentElement.dataset.theme = theme;
	document.documentElement.style.colorScheme = theme;
}

export function applyDocumentThemeStyle(style: ThemeStyle): void {
	if (typeof document === "undefined") return;
	if (style === DEFAULT_THEME_STYLE) {
		delete document.documentElement.dataset.styleTheme;
	} else {
		installThemeStylesheet();
		document.documentElement.dataset.styleTheme = style;
	}
}

/** True when the document already shows `style`, e.g. because it is being previewed. */
export function isDocumentThemeStyle(style: ThemeStyle): boolean {
	if (typeof document === "undefined") return false;
	return (document.documentElement.dataset.styleTheme ?? DEFAULT_THEME_STYLE) === style;
}

let previewing = false;

/**
 * Show `style` while the user browses the theme menu; `null` ends the preview
 * and restores `saved`. Skips the View Transition: its snapshot freezes the
 * page (hover states included) on every swap. Transitions are held off for the
 * whole preview instead, so nothing tweens between palettes, and released two
 * frames after it ends so the restored palette paints without tweening.
 */
export function previewDocumentThemeStyle(style: ThemeStyle | null, saved: ThemeStyle): void {
	if (typeof document === "undefined") return;
	const root = document.documentElement;
	if (style !== null) {
		previewing = true;
		root.dataset.themeTransition = "active";
		applyDocumentThemeStyle(style);
		return;
	}
	if (!previewing) return;
	previewing = false;
	applyDocumentThemeStyle(saved);
	requestAnimationFrame(() =>
		requestAnimationFrame(() => {
			if (!previewing) delete root.dataset.themeTransition;
		}),
	);
}

/**
 * Apply a theme DOM update under a View Transition so per-element
 * `transition-colors` / background tweens are hidden behind a snapshot.
 * Default VT crossfade is disabled in CSS — this is an instant cut.
 * Falls back to a plain update when the API is unavailable.
 *
 * Also sets `data-theme-transition` so global CSS can zero out
 * transition-duration while tokens swap; otherwise composer, search, and
 * switch chrome tween from the old palette and look stuck white/black.
 *
 * `onReveal` runs when the new theme reaches the screen; main flips the
 * native window material with it. A translucent window takes its own path
 * (`runTranslucentThemeTransition`).
 */
export function runThemeTransition(update: () => void, onReveal?: () => void): void {
	if (typeof document === "undefined") return;

	const root = document.documentElement;
	if (root.dataset.windowTranslucent === "true" && typeof document.startViewTransition === "function") {
		runTranslucentThemeTransition(update, onReveal);
		return;
	}
	const finish = () => {
		requestAnimationFrame(() => {
			delete root.dataset.themeTransition;
		});
	};
	const run = () => {
		root.dataset.themeTransition = "active";
		update();
		void root.offsetHeight;
	};

	if (typeof document.startViewTransition !== "function") {
		run();
		onReveal?.();
		finish();
		return;
	}

	const transition = document.startViewTransition(() => {
		run();
	});
	onReveal?.();
	void transition.finished.finally(finish);
}

/**
 * Translucent windows: the update runs inside a View Transition that keeps the
 * old frame on screen while React re-renders theme consumers, then reveals
 * everything in one frame. styles.css turns off every transition animation
 * there (the root group's own 250 ms animation froze the page, and the old
 * snapshot showed its tint through the new one), so the cut costs no extra
 * time. No forced style flush: each one is a whole-document recalc, which the
 * translucent styles make expensive.
 */
function runTranslucentThemeTransition(update: () => void, onReveal?: () => void): void {
	const root = document.documentElement;
	const transition = document.startViewTransition(async () => {
		root.dataset.themeTransition = "active";
		update();
		// Let React flush the re-render the update scheduled, so theme
		// consumers change in the revealed frame rather than after it.
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	// `ready` fires once the new frame is rendered and about to be shown,
	// ~60 ms after the update itself. Flipping the native material any earlier
	// shows it under the old frame.
	void transition.ready.then(onReveal, onReveal);
	void transition.finished.finally(() =>
		requestAnimationFrame(() =>
			requestAnimationFrame(() => {
				if (!previewing) delete root.dataset.themeTransition;
			}),
		),
	);
}
