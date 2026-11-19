import type { WindowTransparencyState } from "../../shared/window-transparency";

/** A renderer has no capability verdict until main answers. */
export type WindowTransparencyView = Omit<WindowTransparencyState, "supported"> & { supported: boolean | null };

/** Shared by Appearance and command search, including the startup/error states. */
export function windowTransparencyPresentation(
	state: WindowTransparencyView,
	platform: { mac: boolean; windows: boolean },
) {
	const unsupported = platform.windows && state.supported === false && state.reason !== "error";
	return {
		visible: platform.mac || platform.windows,
		disabled: unsupported,
		hint: state.reason === "error" ? "settings.windowTranslucencyError" as const
			: unsupported ? "settings.windowTranslucencyUnsupported" as const
			: state.reason === "system" ? "settings.windowTranslucencySystem" as const
			: "settings.windowTranslucencyHint" as const,
	};
}
