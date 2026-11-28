export type WindowTransparencyState = {
	supported: boolean;
	// Main has configured the request after known system/error suppression.
	// Electron's Windows material setter returns void and can only log DWM
	// failures, so this is not confirmation that DWM is drawing Acrylic.
	enabled: boolean;
	reason: "unsupported" | "system" | "error" | null;
};

export const WINDOW_TRANSPARENCY_CHANGED = "theme:window-transparency-changed";
