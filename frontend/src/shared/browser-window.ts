// The shell opens a session's Browser page in its own window with
// window.open("about:blank", browserWindowFrameName(viewId)). The main process
// reads the view back from that name to know which session the window holds.

/** Sent to the app shell when a Browser window gets or gives back its pages. */
export type BrowserWindowState = { viewId: string; open: boolean };

const FRAME_PREFIX = "ao-browser-window:";

export function browserWindowFrameName(viewId: string): string {
	return `${FRAME_PREFIX}${encodeURIComponent(viewId)}`;
}

/** The view a Browser window's name holds, or null for any other window. */
export function browserWindowViewId(frameName: string): string | null {
	if (!frameName.startsWith(FRAME_PREFIX)) return null;
	try {
		const viewId = decodeURIComponent(frameName.slice(FRAME_PREFIX.length));
		return viewId ? viewId : null;
	} catch {
		return null;
	}
}
