import type { Rect, SettingsWindowSnapshot } from "./settings-window-watch";

// A drop strip docked under System Settings while Cua Driver's macOS
// permissions are missing. AO cannot reach into System Settings, so the strip
// follows its window and offers CuaDriver.app as a drag source: dropping it on
// the privacy list adds it there. The user picks which permission's pane to
// open; each pick reopens System Settings there.

export type PrivacyPane = "accessibility" | "screenRecording";

export const PRIVACY_PANES: readonly PrivacyPane[] = ["accessibility", "screenRecording"];

// The only System Settings panes external-open lets the renderer open.
export const PRIVACY_PANE_URLS: Record<PrivacyPane, string> = {
	accessibility: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
	screenRecording: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
};

export const CUA_DRIVER_APP_PATH = "/Applications/CuaDriver.app";

/** What the renderer asks for: which pane to point at, what is already granted, and its translated copy. */
export interface PrivacyGuideRequest {
	pane: PrivacyPane;
	granted: Record<PrivacyPane, boolean>;
	text: {
		drag: string;
		hint: string;
		labels: Record<PrivacyPane, string>;
		granted: string;
		close: string;
	};
}

// Panel links navigate to this scheme and the main process intercepts them.
const GUIDE_SCHEME = "ao-privacy-guide:";
const MAX_TEXT = 400;

export const PRIVACY_GUIDE_HEIGHT = 72;
const MIN_WIDTH = 560;
const GAP = 8;

export type PrivacyGuideAction = { kind: "pane"; pane: PrivacyPane } | { kind: "close" };

export function isPrivacyPane(value: unknown): value is PrivacyPane {
	return value === "accessibility" || value === "screenRecording";
}

function text(value: unknown): string | undefined {
	return typeof value === "string" && value.length > 0 && value.length <= MAX_TEXT ? value : undefined;
}

/** Validates an IPC payload from the renderer; anything malformed is refused whole. */
export function coercePrivacyGuideRequest(value: unknown): PrivacyGuideRequest | undefined {
	if (!value || typeof value !== "object") return undefined;
	const input = value as Record<string, unknown>;
	const granted = input.granted as Record<string, unknown> | undefined;
	const copy = input.text as Record<string, unknown> | undefined;
	const labels = copy?.labels as Record<string, unknown> | undefined;
	if (!isPrivacyPane(input.pane) || !granted || !copy || !labels) return undefined;
	const drag = text(copy.drag), hint = text(copy.hint), grantedText = text(copy.granted), close = text(copy.close);
	const accessibility = text(labels.accessibility), screenRecording = text(labels.screenRecording);
	if (!drag || !hint || !grantedText || !close || !accessibility || !screenRecording) return undefined;
	return {
		pane: input.pane,
		granted: { accessibility: granted.accessibility === true, screenRecording: granted.screenRecording === true },
		text: {
			drag,
			hint,
			labels: { accessibility, screenRecording },
			granted: grantedText,
			close,
		},
	};
}

export function parsePrivacyGuideAction(url: string): PrivacyGuideAction | undefined {
	if (!url.startsWith(GUIDE_SCHEME)) return undefined;
	const [kind, pane] = url.slice(GUIDE_SCHEME.length).replace(/^\/+/, "").split("/");
	if (kind === "close") return { kind: "close" };
	if (kind === "pane" && isPrivacyPane(pane)) return { kind: "pane", pane };
	return undefined;
}

/**
 * Docks the strip under the System Settings window, as wide as it, inside the
 * work area; with no room below it sits over the window's bottom edge.
 */
export function placePrivacyGuide(settings: Rect, workArea: Rect): Rect {
	const height = PRIVACY_GUIDE_HEIGHT;
	const width = Math.round(Math.min(Math.max(settings.width, MIN_WIDTH), workArea.width - GAP * 2));
	const minX = workArea.x + GAP, maxX = workArea.x + workArea.width - width - GAP;
	const x = Math.round(Math.min(Math.max(settings.x + (settings.width - width) / 2, minX), maxX));
	const below = settings.y + settings.height + GAP;
	const y = below + height <= workArea.y + workArea.height ? below : settings.y + settings.height - height - GAP;
	return { x, y: Math.round(y), width, height };
}

function escapeHtml(value: string): string {
	return value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

const GEAR_SVG = `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/></svg>`;

/** The CuaDriver icon once read; until then a gear, spinning while the read is in flight. */
export type PrivacyGuideIcon = { kind: "loading" } | { kind: "missing" } | { kind: "image"; dataUrl: string };

function iconHtml(icon: PrivacyGuideIcon): string {
	if (icon.kind === "image" && icon.dataUrl.startsWith("data:image/")) return `<img src="${escapeHtml(icon.dataUrl)}" alt="" draggable="false">`;
	return `<span class="placeholder${icon.kind === "loading" ? " loading" : ""}">${GEAR_SVG}</span>`;
}

/** The strip's whole page. Colors follow the app's popover tokens in both themes. */
export function renderPrivacyGuideHtml(request: PrivacyGuideRequest, icon: PrivacyGuideIcon = { kind: "missing" }): string {
	const { pane, granted, text: copy } = request;
	const choices = PRIVACY_PANES.map((choice) => {
		const state = choice === pane ? " selected" : "";
		const mark = granted[choice] ? `<span class="mark" aria-label="${escapeHtml(copy.granted)}">✓</span>` : "";
		return `<a class="choice${state}" href="${GUIDE_SCHEME}//pane/${choice}" role="tab" aria-selected="${choice === pane}">${mark}${escapeHtml(copy.labels[choice])}</a>`;
	}).join("");
	return `<!doctype html>
<html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">
<title>${escapeHtml(copy.drag)}</title>
<style>
:root { color-scheme: dark; --bg: oklch(0.300 0.002 250); --fg: oklch(0.970 0.002 250); --muted: oklch(0.720 0.002 250); --hover: oklch(0.295 0.002 250); --tile: oklch(0.360 0.002 250); --border: oklch(1 0 0 / 9%); --track: oklch(0.235 0.002 250); --tab: oklch(0.400 0.002 250); --ok: #4ade80; }
@media (prefers-color-scheme: light) { :root { color-scheme: light; --bg: oklch(1 0 0); --fg: oklch(0.18 0 0); --muted: oklch(0.5 0 0); --hover: oklch(0.955 0 0); --tile: oklch(0.975 0 0); --border: oklch(0 0 0 / 10%); --track: oklch(0.93 0 0); --tab: oklch(1 0 0); --ok: #16a34a; } }
* { box-sizing: border-box; }
html, body { margin: 0; height: 100%; }
body { font: 13px/1.35 -apple-system, BlinkMacSystemFont, system-ui, sans-serif; color: var(--fg); background: var(--bg); border: 1px solid var(--border); border-radius: 12px; padding: 0 10px 0 12px; display: flex; align-items: center; gap: 12px; user-select: none; overflow: hidden; }
a { color: inherit; text-decoration: none; outline: none; }
a:focus-visible { box-shadow: 0 0 0 2px var(--muted); }
.app { display: flex; align-items: center; gap: 8px; padding: 5px 12px 5px 6px; border-radius: 10px; background: var(--tile); border: 1px solid var(--border); cursor: grab; flex: none; }
.app:active { cursor: grabbing; }
.app img { width: 36px; height: 36px; pointer-events: none; }
.app > span:last-child { font-weight: 600; }
.placeholder { position: relative; width: 36px; height: 36px; display: grid; place-items: center; border-radius: 8px; background: var(--hover); color: var(--muted); }
.placeholder.loading::after { content: ""; position: absolute; inset: 3px; border-radius: 50%; border: 2px solid transparent; border-top-color: var(--muted); animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .placeholder.loading::after { animation-duration: 2.4s; } }
.copy { flex: 1; min-width: 0; }
.copy strong { display: flex; align-items: center; gap: 6px; font-weight: 600; min-width: 0; }
.copy strong span { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.copy svg { flex: none; }
.copy p { margin: 2px 0 0; color: var(--muted); font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
nav { display: flex; gap: 2px; padding: 3px; border-radius: 9px; background: var(--track); border: 1px solid var(--border); flex: none; }
.choice { display: flex; align-items: center; font-size: 12px; padding: 5px 10px; border-radius: 6px; color: var(--muted); white-space: nowrap; }
.choice:hover { color: var(--fg); }
.choice.selected { background: var(--tab); color: var(--fg); font-weight: 500; box-shadow: 0 1px 2px oklch(0 0 0 / 25%), inset 0 0 0 1px var(--border); }
.mark { color: var(--ok); margin-right: 4px; }
.close { color: var(--muted); width: 24px; height: 24px; display: grid; place-items: center; border-radius: 6px; flex: none; }
.close:hover { background: var(--hover); color: var(--fg); }
</style></head>
<body>
<div class="app" draggable="true" data-drag-app>${iconHtml(icon)}<span>CuaDriver</span></div>
<div class="copy">
<strong><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12 7-7 7 7"/><path d="M12 19V5"/></svg><span>${escapeHtml(copy.drag)}</span></strong>
<p>${escapeHtml(copy.hint)}</p>
</div>
<nav role="tablist" aria-label="${escapeHtml(copy.drag)}">${choices}</nav>
<a class="close" href="${GUIDE_SCHEME}//close" aria-label="${escapeHtml(copy.close)}" title="${escapeHtml(copy.close)}"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg></a>
</body></html>`;
}

export interface PrivacyGuideWindow {
	loadURL(url: string): Promise<unknown>;
	setBounds(bounds: Rect): void;
	showInactive(): void;
	hide(): void;
	isVisible(): boolean;
	isFocused(): boolean;
	close(): void;
	isDestroyed(): boolean;
	onNavigate(handler: (url: string) => void): void;
	onClosed(handler: () => void): void;
}

export interface PrivacyGuideDeps {
	createWindow(): PrivacyGuideWindow;
	openExternal(url: string): Promise<unknown>;
	readIcon(appPath: string): Promise<string | undefined>;
	/** Streams System Settings snapshots; onError means they will not come. Returns a stop. */
	watchSettings(onSnapshot: (snapshot: SettingsWindowSnapshot) => void, onError: () => void): () => void;
	workAreaFor(bounds: Rect): Rect;
	/** Where the strip goes when System Settings cannot be followed. */
	fallbackBounds(): Rect;
	ownPid: number;
}

/** Owns the one guide strip: opens the chosen pane, (re)renders it, docks it to System Settings, and follows its links. */
export class PrivacyGuide {
	private window?: PrivacyGuideWindow;
	private request?: PrivacyGuideRequest;
	private icon: PrivacyGuideIcon = { kind: "loading" };
	private iconRead?: Promise<void>;
	private renderId = 0;
	private stopWatching?: () => void;
	private sawSettings = false;
	private following = true;

	constructor(private readonly deps: PrivacyGuideDeps) {}

	async show(request: PrivacyGuideRequest): Promise<void> {
		this.request = request;
		await this.deps.openExternal(PRIVACY_PANE_URLS[request.pane]);
		const window = this.ensureWindow();
		// The strip shows at once with a spinning gear and swaps in the icon when read.
		this.iconRead ??= this.deps.readIcon(CUA_DRIVER_APP_PATH)
			.then((dataUrl) => { this.icon = dataUrl ? { kind: "image", dataUrl } : { kind: "missing" }; })
			.catch(() => { this.icon = { kind: "missing" }; })
			.then(() => this.render())
			.catch(() => undefined);
		await this.render();
		// Docked placement waits for the first snapshot so the strip never flashes elsewhere.
		if (!this.following && !window.isDestroyed()) {
			window.setBounds(this.deps.fallbackBounds());
			window.showInactive();
		}
	}

	private async render(): Promise<void> {
		const window = this.window;
		if (!window || window.isDestroyed() || !this.request) return;
		// A newer render (the icon arriving, a pane pick) aborts this load; only
		// the latest one's failure matters.
		const id = ++this.renderId;
		try {
			await window.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(renderPrivacyGuideHtml(this.request, this.icon))}`);
		} catch (error) {
			if (id === this.renderId && !window.isDestroyed()) throw error;
		}
	}

	/**
	 * Updates what is granted; closes once the last missing permission is
	 * granted. A guide opened to review grants that were all already on stays.
	 */
	async update(granted: Record<PrivacyPane, boolean>): Promise<void> {
		if (!this.request || !this.window) return;
		if (PRIVACY_PANES.every((pane) => granted[pane])) {
			if (!PRIVACY_PANES.every((pane) => this.request!.granted[pane])) this.close();
			return;
		}
		const changed = PRIVACY_PANES.some((pane) => granted[pane] !== this.request!.granted[pane]);
		if (!changed) return;
		// Move on to the next missing permission once the shown one is granted.
		const pane = granted[this.request.pane] ? PRIVACY_PANES.find((candidate) => !granted[candidate])! : this.request.pane;
		await this.show({ ...this.request, pane, granted });
	}

	close(): void {
		const window = this.window;
		this.reset();
		if (window && !window.isDestroyed()) window.close();
	}

	private reset(): void {
		this.stopWatching?.();
		this.stopWatching = undefined;
		this.window = undefined;
		this.request = undefined;
		this.sawSettings = false;
		this.following = true;
	}

	private follow(snapshot: SettingsWindowSnapshot): void {
		const window = this.window;
		if (!window || window.isDestroyed()) return;
		if (snapshot.settingsPid < 0) {
			// The user quit System Settings: the guide has nothing left to point at.
			if (this.sawSettings) this.close();
			return;
		}
		this.sawSettings = true;
		const inFront = snapshot.frontPid === snapshot.settingsPid || (snapshot.frontPid === this.deps.ownPid && window.isFocused());
		if (!snapshot.bounds || !inFront) {
			if (window.isVisible()) window.hide();
			return;
		}
		window.setBounds(placePrivacyGuide(snapshot.bounds, this.deps.workAreaFor(snapshot.bounds)));
		if (!window.isVisible()) window.showInactive();
	}

	private ensureWindow(): PrivacyGuideWindow {
		if (this.window && !this.window.isDestroyed()) return this.window;
		const window = this.deps.createWindow();
		window.onNavigate((url) => {
			const action = parsePrivacyGuideAction(url);
			if (action?.kind === "close") this.close();
			else if (action?.kind === "pane" && this.request) void this.show({ ...this.request, pane: action.pane });
		});
		window.onClosed(() => {
			if (this.window === window) this.reset();
		});
		this.window = window;
		this.stopWatching = this.deps.watchSettings(
			(snapshot) => this.follow(snapshot),
			() => {
				// Cannot follow System Settings; park the strip where it can still be used.
				this.following = false;
				if (this.window && !this.window.isDestroyed()) {
					this.window.setBounds(this.deps.fallbackBounds());
					this.window.showInactive();
				}
			},
		);
		return window;
	}
}
