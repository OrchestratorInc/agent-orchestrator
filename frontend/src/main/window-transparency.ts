import type { BaseWindow } from "electron";
import type { WindowTransparencyState } from "../shared/window-transparency";

type WindowHost = Pick<BaseWindow, "isDestroyed" | "setBackgroundColor" | "setBackgroundMaterial" | "setVibrancy">;
type Appearance = { dark: boolean; reducedTransparency: boolean; highContrast: boolean };

export function supportsWindowTransparency(platform: NodeJS.Platform, release: string): boolean {
	if (platform === "darwin") return true;
	if (platform !== "win32") return false;
	const match = /^(\d+)\.(\d+)\.(\d+)(?:\.|$)/.exec(release);
	return Boolean(match && Number(match[1]) >= 10 && Number(match[3]) >= 22621);
}

/** Owns native requests and opaque fallbacks for known suppression/thrown errors.
 * Electron cannot report silent DWM material failures; enabled is configured state.
 */
export class WindowTransparency {
	private requested = false;
	private failed = false;
	private applied = new WeakMap<WindowHost, { enabled: boolean; color: string; dark: boolean }>();
	// Only remove material we requested. Even "none" changes Electron's native
	// window composition, so a default-off Windows window must never receive it.
	private windowsMaterial = new WeakSet<WindowHost>();
	readonly supported: boolean;
	state: WindowTransparencyState;

	constructor(private platform: NodeJS.Platform, release: string) {
		this.supported = supportsWindowTransparency(platform, release);
		this.state = { supported: this.supported, enabled: false, reason: this.supported ? null : "unsupported" };
	}

	request(enabled: boolean): void {
		this.requested = enabled;
		// A new explicit request (including a new shell document) can retry a failure.
		this.failed = false;
	}

	sync(window: WindowHost, opaqueColor: string, appearance: Appearance, force = false): WindowTransparencyState {
		if (window.isDestroyed()) return this.state;
		const reason = !this.supported ? "unsupported"
			: this.failed ? "error"
			: this.requested && (appearance.reducedTransparency || appearance.highContrast) ? "system"
			: null;
		let enabled = this.requested && reason === null;
		const color = enabled ? "#00000000" : opaqueColor;
		const applied = this.applied.get(window);
		try {
			// Paint the fallback before removing a material, avoiding a clear/black frame.
			if (force || applied?.color !== color) window.setBackgroundColor(color);
			if (this.platform === "darwin") {
				if (force || applied?.enabled !== enabled || applied?.dark !== appearance.dark) {
					window.setVibrancy(enabled ? "under-window" : null);
				}
			} else if (this.platform === "win32" && this.supported) {
				if (enabled && (force || applied?.enabled !== true || applied?.dark !== appearance.dark)) {
					// A throwing setter may already have changed native state. Keep
					// ownership until cleanup succeeds, including after a partial failure.
					this.windowsMaterial.add(window);
					window.setBackgroundMaterial("acrylic");
				} else if (!enabled && this.windowsMaterial.has(window)) {
					window.setBackgroundMaterial("none");
					this.windowsMaterial.delete(window);
				}
			}
			this.applied.set(window, { enabled, color, dark: appearance.dark });
		} catch (error) {
			console.warn("AO: unable to apply window transparency; restoring opaque background", error);
			this.failed = true;
			enabled = false;
			this.applied.delete(window);
			try {
				window.setBackgroundColor(opaqueColor);
			} catch {
				// Still try to remove a material if painting the fallback failed.
			}
			try {
				if (this.platform === "darwin") window.setVibrancy(null);
				else if (this.windowsMaterial.has(window)) {
					window.setBackgroundMaterial("none");
					this.windowsMaterial.delete(window);
				}
			} catch {
				// The window may be closing, or DWM may be unavailable.
			}
		}
		this.state = { supported: this.supported, enabled, reason: this.failed ? "error" : reason };
		return this.state;
	}
}
