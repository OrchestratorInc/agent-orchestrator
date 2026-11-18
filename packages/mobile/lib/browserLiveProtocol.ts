export type BrowserLiveFrame = {
	sequence: bigint;
	capturedAtMs: bigint;
	width: number;
	height: number;
	jpeg: ArrayBuffer;
};

export type BrowserFrameRect = { left: number; top: number; width: number; height: number };

/**
 * Electron mouse-wheel deltas use the same sign as a direct-manipulation drag:
 * positive Y scrolls up, moving content down with a downward finger gesture.
 *
 * `scale` is how many phone points one desktop pixel occupies in the fitted
 * frame. The drag is measured on the phone but scrolls the desktop page, so
 * dividing by it keeps the content under the finger instead of crawling.
 */
export function browserWheelDeltaFromDrag(deltaX: number, deltaY: number, scale = 1): { deltaX: number; deltaY: number } {
	const factor = Number.isFinite(scale) && scale > 0 ? scale : 1;
	return { deltaX: deltaX / factor, deltaY: deltaY / factor };
}

export function browserLiveURL(config: { secure?: boolean; host: string; httpPort: string }, sessionID: string): string {
	const scheme = config.secure ? "wss" : "ws";
	const host = config.host.trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, "").replace(/\/+$/, "");
	return `${scheme}://${host}:${config.httpPort}/api/v1/sessions/${encodeURIComponent(sessionID)}/browser/live`;
}

export function decodeBrowserFrame(buffer: ArrayBuffer): BrowserLiveFrame {
	if (buffer.byteLength < 23) throw new Error("Browser frame is truncated");
	const view = new DataView(buffer);
	if (view.getUint8(0) !== 1 || view.getUint8(1) !== 1) throw new Error("Browser frame version is unsupported");
	const width = view.getUint16(18);
	const height = view.getUint16(20);
	if (width < 1 || height < 1) throw new Error("Browser frame dimensions are invalid");
	return {
		sequence: view.getBigUint64(2),
		capturedAtMs: view.getBigUint64(10),
		width,
		height,
		jpeg: buffer.slice(22),
	};
}

/** Fit a captured browser frame without stretching it or distorting pointer coordinates. */
export function containBrowserFrame(
	container: { width: number; height: number },
	frame: { width: number; height: number },
): BrowserFrameRect {
	if (container.width <= 0 || container.height <= 0 || frame.width <= 0 || frame.height <= 0) {
		return { left: 0, top: 0, width: 0, height: 0 };
	}
	const scale = Math.min(container.width / frame.width, container.height / frame.height);
	const width = frame.width * scale;
	const height = frame.height * scale;
	return {
		left: (container.width - width) / 2,
		top: (container.height - height) / 2,
		width,
		height,
	};
}

/**
 * Where a point in the viewport lands on the desktop page, as fractions of the
 * fitted frame. Null when it falls in the letterbox around the frame.
 */
export function normalizedFramePoint(rect: BrowserFrameRect, point: { x: number; y: number }): { x: number; y: number } | null {
	if (rect.width <= 0 || rect.height <= 0) return null;
	if (point.x < rect.left || point.x > rect.left + rect.width || point.y < rect.top || point.y > rect.top + rect.height) return null;
	return { x: (point.x - rect.left) / rect.width, y: (point.y - rect.top) / rect.height };
}

/** React Native Image accepts data URIs, while URL.createObjectURL is web-only. */
export function browserJPEGDataURI(buffer: ArrayBuffer): string {
	const bytes = new Uint8Array(buffer);
	let binary = "";
	const chunkSize = 0x8000;
	for (let offset = 0; offset < bytes.length; offset += chunkSize) {
		binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length)));
	}
	return `data:image/jpeg;base64,${btoa(binary)}`;
}
