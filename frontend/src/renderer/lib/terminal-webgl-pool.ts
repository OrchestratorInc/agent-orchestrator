import { WebglAddon } from "@xterm/addon-webgl";
import type { Terminal } from "@xterm/xterm";

export const MAX_WEBGL_TERMINALS = 8;

export type WebglLease = {
	activate: () => void;
	setEnabled: (enabled: boolean) => void;
	release: () => void;
};

type Entry = {
	term: Terminal;
	isVisible: () => boolean;
	enabled: boolean;
	addon: WebglAddon | null;
};

const entries = new Map<WebglLease, Entry>();
let webglUnavailable = false;

function detach(entry: Entry): void {
	const addon = entry.addon;
	entry.addon = null;
	if (!addon) return;
	try {
		addon.dispose();
	} catch (error) {
		console.warn("xterm: WebGL renderer dispose failed", error);
	}
}

function attach(entry: Entry): void {
	if (webglUnavailable) return;
	try {
		const addon = new WebglAddon();
		addon.onContextLoss(() => {
			if (entry.addon !== addon) return;
			detach(entry);
			console.warn("xterm: WebGL context lost; box-drawing may drift until the terminal is shown again");
		});
		entry.term.loadAddon(addon);
		entry.addon = addon;
	} catch (error) {
		webglUnavailable = true;
		console.warn("xterm: WebGL renderer unavailable; box-drawing may drift", error);
	}
}

function evictOverCap(): void {
	let attached = 0;
	for (const entry of entries.values()) if (entry.addon) attached++;
	for (const entry of entries.values()) {
		if (attached <= MAX_WEBGL_TERMINALS) return;
		if (!entry.addon || entry.isVisible()) continue;
		detach(entry);
		attached--;
	}
}

export function leaseWebglRenderer(term: Terminal, isVisible: () => boolean, enabled = true): WebglLease {
	const entry: Entry = { term, isVisible, enabled, addon: null };
	const lease: WebglLease = {
		activate: () => {
			if (!entries.has(lease) || !entry.enabled) return;
			entries.delete(lease);
			entries.set(lease, entry);
			if (!entry.addon) attach(entry);
			evictOverCap();
		},
		setEnabled: (nextEnabled) => {
			if (!entries.has(lease) || entry.enabled === nextEnabled) return;
			entry.enabled = nextEnabled;
			if (!nextEnabled) detach(entry);
			else if (entry.isVisible()) lease.activate();
		},
		release: () => {
			entries.delete(lease);
		},
	};
	entries.set(lease, entry);
	return lease;
}
