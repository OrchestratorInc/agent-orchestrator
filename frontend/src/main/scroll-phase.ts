import path from "node:path";
import { app } from "electron";
import type { ScrollPhase } from "../shared/scroll-phase";

type ScrollPhaseAddon = { start: (callback: (phase: ScrollPhase) => void) => void };

/**
 * Starts relaying macOS trackpad finger down/up to `send` through the
 * native-scroll-phase addon (native/scroll-phase). Swipe navigation needs it to
 * tell a held swipe from a released one. Returns false when the addon is not
 * available; the renderer then falls back to wheel-timing heuristics.
 */
export function startScrollPhaseRelay(send: (phase: ScrollPhase) => void): boolean {
	if (process.platform !== "darwin") return false;
	const addonPath = app.isPackaged
		? path.join(process.resourcesPath, "scroll-phase", "ao-scroll-phase.node")
		: path.join(app.getAppPath(), "scroll-phase", "ao-scroll-phase.node");
	try {
		const addon = { exports: {} as Partial<ScrollPhaseAddon> };
		process.dlopen(addon, addonPath);
		if (typeof addon.exports.start !== "function") return false;
		addon.exports.start(send);
		return true;
	} catch (error) {
		console.warn("scroll phase addon unavailable; swipe navigation uses wheel timing only:", error);
		return false;
	}
}
