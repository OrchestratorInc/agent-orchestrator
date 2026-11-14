import { useSyncExternalStore } from "react";
import type { BrowserRuntimeState } from "../../main/browser-view-host";

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

export function useBrowserRuntimeState(): BrowserRuntimeState | null {
	return useSyncExternalStore(subscribe, getSnapshot);
}
