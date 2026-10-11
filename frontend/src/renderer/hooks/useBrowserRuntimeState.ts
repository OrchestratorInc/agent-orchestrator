import { useEffect, useState } from "react";
import type { BrowserRuntimeState } from "../../main/browser-view-host";

// null until the main process answers, so a panel never flashes "disconnected"
// before the first read.
let current: BrowserRuntimeState | null = null;
const listeners = new Set<(state: BrowserRuntimeState | null) => void>();
let revision = 0;
let stop: (() => void) | undefined;

function receive(state: BrowserRuntimeState) {
	if (current?.connected === state.connected && current.rejected === state.rejected) return;
	current = state;
	revision += 1;
	for (const listener of listeners) listener(state);
}

function connect() {
	let live = true;
	// Subscribe before reading. A late snapshot must never replace a push.
	const off = window.ao?.browser.onRuntimeState(receive);
	const requestedRevision = revision;
	void window.ao?.browser
		.getRuntimeState()
		.then((state) => {
			if (live && requestedRevision === revision) receive(state);
		})
		.catch(() => undefined);
	return () => {
		live = false;
		off?.();
	};
}

/**
 * The desktop browser runtime link, read only by the surfaces that show it. It
 * lives outside useBrowserView so link changes re-render the Browser panel, not
 * the whole SessionView that owns the view model (#6466).
 */
export function useBrowserRuntimeState(): BrowserRuntimeState | null {
	const [state, setState] = useState(current);
	useEffect(() => {
		listeners.add(setState);
		setState(current);
		if (!stop) stop = connect();
		return () => {
			listeners.delete(setState);
			if (listeners.size === 0) {
				stop?.();
				stop = undefined;
				current = null;
			}
		};
	}, []);
	return state;
}

export async function reconnectBrowserRuntime(): Promise<void> {
	await window.ao?.browser.reconnectRuntime();
}
