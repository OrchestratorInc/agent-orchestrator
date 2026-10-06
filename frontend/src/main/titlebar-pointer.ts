import type { BaseWindow, Point, WebContents } from "electron";
import { MAC_TITLEBAR_HEIGHT } from "../shared/window-chrome";

export const TITLEBAR_POINTER_CHANNEL = "window:titlebar-pointer";
const POLL_MS = 50;

/**
 * macOS never delivers mouse events over a `-webkit-app-region: drag` area, so
 * the page cannot see the pointer on the draggable titlebar row. Poll the OS
 * cursor instead and tell the page where it is (CSS px from the window's left
 * edge) while it is inside the titlebar band, or null when it is not. Only
 * changes are sent. The returned function stops the polling.
 */
export function startTitlebarPointerTracking(options: {
	window: Pick<BaseWindow, "isDestroyed" | "isFocused" | "getContentBounds">;
	shell: () => Pick<WebContents, "isDestroyed" | "getZoomFactor" | "send"> | null;
	getCursorScreenPoint: () => Point;
	intervalMs?: number;
}): () => void {
	let last: number | null | undefined;
	const tick = () => {
		const shell = options.shell();
		if (options.window.isDestroyed() || !shell || shell.isDestroyed()) return;
		let x: number | null = null;
		if (options.window.isFocused()) {
			const zoom = shell.getZoomFactor() || 1;
			const point = options.getCursorScreenPoint();
			const bounds = options.window.getContentBounds();
			const inside =
				point.x >= bounds.x &&
				point.x < bounds.x + bounds.width &&
				point.y >= bounds.y &&
				point.y < bounds.y + MAC_TITLEBAR_HEIGHT * zoom;
			if (inside) x = Math.round((point.x - bounds.x) / zoom);
		}
		if (x === last) return;
		last = x;
		shell.send(TITLEBAR_POINTER_CHANNEL, x);
	};
	const timer = setInterval(tick, options.intervalMs ?? POLL_MS);
	return () => clearInterval(timer);
}
