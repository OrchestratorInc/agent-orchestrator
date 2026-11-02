import { useSyncExternalStore } from "react";
import type { BrowserRuntimeState } from "../../main/browser-view-host";

// null until the main process answers, so a panel never flashes "disconnected"
// before the first read.
let current: BrowserRuntimeState | null = null;
const listeners = new Set<() => void>();
let stopIpc: (() => void) | undefined;

function receive(state: BrowserRuntimeState) {
	if (
		current?.connected === state.connected &&
		current.connecting === state.connecting &&
		current.rejected === state.rejected
	) {
		return;
	}
	current = state;
	listeners.forEach((listener) => listener());
}

// One IPC subscription for every mounted panel, opened by the first and closed by
// the last. It subscribes before reading, so a late snapshot never replaces a push.
function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	if (!stopIpc) {
		const session = { live: true, pushed: false };
		const off = window.ao?.browser.onRuntimeState((state) => {
			session.pushed = true;
			receive(state);
		});
		void window.ao?.browser
			.getRuntimeState()
			.then((state) => {
				if (session.live && !session.pushed) receive(state);
			})
			.catch(() => undefined);
		stopIpc = () => {
			session.live = false;
			off?.();
		};
	}
	return () => {
		listeners.delete(listener);
		if (listeners.size > 0) return;
		stopIpc?.();
		stopIpc = undefined;
		current = null;
	};
}

const getSnapshot = () => current;

/**
 * The desktop browser runtime link, read only by the surfaces that show it. It
 * lives outside useBrowserView so link changes re-render the Browser panel, not
 * the whole SessionView that owns the view model (#6466).
 */
export function useBrowserRuntimeState(): BrowserRuntimeState | null {
	return useSyncExternalStore(subscribe, getSnapshot);
}
