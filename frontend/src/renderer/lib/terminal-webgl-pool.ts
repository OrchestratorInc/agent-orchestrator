import { WebglAddon } from "@xterm/addon-webgl";
import type { Terminal } from "@xterm/xterm";

/**
 * Chromium keeps at most 16 live WebGL contexts per renderer process and
 * silently kills the oldest one past that. Every cached terminal used to hold
 * its own context forever, so the 17th terminal knocked an older one onto the
 * slow DOM renderer for good. The pool keeps contexts only for the most
 * recently shown terminals, well under the cap, and gives a parked terminal a
 * fresh context when it is shown again.
 */
export const MAX_WEBGL_TERMINALS = 8;

export type WebglLease = {
	/** The terminal is about to be shown: make sure it renders with WebGL. */
	activate: () => void;
	/** Leave the pool; the terminal's own dispose() frees its context. */
	release: () => void;
};

type Entry = {
	term: Terminal;
	isVisible: () => boolean;
	addon: WebglAddon | null;
};

// Map order is recency: activate() re-inserts, so the first entry is the
// least recently shown terminal.
const entries = new Map<WebglLease, Entry>();
// Set once creating a WebGL renderer fails; later activations stay on the DOM
// renderer instead of failing and warning on every tab switch.
let webglUnavailable = false;

function detach(entry: Entry): void {
	const addon = entry.addon;
	entry.addon = null;
	if (!addon) return;
	try {
		// Disposing the addon hands rendering back to xterm's DOM renderer and
		// frees the GL context.
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
			// Fall back to the DOM renderer now; the next activation creates a
			// new context.
			detach(entry);
			console.warn("xterm: WebGL context lost; box-drawing may drift until the terminal is shown again");
		});
		entry.term.loadAddon(addon);
		entry.addon = addon;
	} catch (error) {
		webglUnavailable = true;
		// WebGL keeps box-drawing glyphs on the cell grid. The canvas addon has no
		// xterm 6 build, so unavailable WebGL falls back to the DOM renderer.
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

export function leaseWebglRenderer(term: Terminal, isVisible: () => boolean): WebglLease {
	const entry: Entry = { term, isVisible, addon: null };
	const lease: WebglLease = {
		activate: () => {
			if (!entries.has(lease)) return;
			entries.delete(lease);
			entries.set(lease, entry);
			if (!entry.addon) attach(entry);
			evictOverCap();
		},
		release: () => {
			entries.delete(lease);
		},
	};
	entries.set(lease, entry);
	return lease;
}
