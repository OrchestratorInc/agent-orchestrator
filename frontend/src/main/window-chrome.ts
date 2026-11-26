import type { BaseWindow, WebContents } from "electron";
import { MAC_TRAFFIC_LIGHT_ROW_HEIGHT, MAC_WINDOW_BUTTON_RADIUS, MAC_WINDOW_BUTTON_X } from "../shared/window-chrome";

/**
 * Native buttons use points, while the shell header scales with Chromium zoom.
 * `edgeExtra` is the extra edge spacing Interface v2 adds (0 when it is off).
 */
export function syncMacWindowButtons(
	window: Pick<BaseWindow, "isDestroyed" | "isFullScreen" | "setWindowButtonPosition" | "getWindowButtonPosition">,
	shell: Pick<WebContents, "isDestroyed" | "getZoomFactor">,
	edgeExtra = 0,
): void {
	if (window.isDestroyed() || shell.isDestroyed()) return;
	if (window.isFullScreen()) return;
	const x = MAC_WINDOW_BUTTON_X + edgeExtra;
	const y = Math.max(0, Math.round(MAC_TRAFFIC_LIGHT_ROW_HEIGHT * shell.getZoomFactor() / 2 - MAC_WINDOW_BUTTON_RADIUS));
	const current = window.getWindowButtonPosition();
	if (current?.x === x && current.y === y) return;
	window.setWindowButtonPosition({ x, y });
}
