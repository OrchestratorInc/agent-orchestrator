import type { BrowserLiveFrame } from "./browserLiveProtocol";
import type { BrowserLiveCommand, BrowserLiveState } from "./browserLive";

/**
 * The seam between the phone's browser pane and whatever streams the page to
 * it: the desktop through the daemon (LocalBrowserTransport) or a Cloud
 * sandbox's browser host. The pane speaks only these types.
 */

export type BrowserControl = "agent" | "human" | "none";
export type FocusedEditable = {
	kind: "text" | "password" | "email" | "number" | "url" | "search" | "tel" | "textarea" | "contenteditable";
	value?: string;
} | null;
export type BrowserViewState = BrowserLiveState & { control: BrowserControl; focusedEditable: FocusedEditable };
export type BrowserFrame = BrowserLiveFrame & {
	/**
	 * Only the image's top-left `width`×`height` pixels show the page (a
	 * fullscreen frame, padded to the window); the rest is drawn clipped away.
	 */
	crop?: { width: number; height: number; imageWidth: number; imageHeight: number };
};
export type BrowserCommand =
	| BrowserLiveCommand
	| { type: "viewport"; payload: { width: number; height: number; dpr: number; mobile: boolean } | null }
	| { type: "quality"; payload: { quality: number; maxFps: number } }
	| { type: "control"; payload: { action: "take" | "release" } }
	| { type: "dialog"; payload: { accept: boolean; promptText?: string } }
	// Typed on the phone and sent straight to the host: never stored or logged.
	| { type: "auth"; payload: { origin: string; username: string; password: string } | { origin: string; cancel: true } }
	| { type: "select"; payload: { id: string; index: number; ctx?: string } }
	// The files the person picked for the page's file input; none cancels it.
	| { type: "files"; payload: { files: BrowserPickedFile[]; folder: boolean } };
export type BrowserPickedFile = { uri: string; name: string; type?: string };
export type BrowserCapabilities = {
	text: boolean; keys: boolean; viewport: boolean; quality: boolean; control: boolean; dialogs: boolean;
	auth: boolean; select: boolean; files: boolean; downloads: boolean;
};
export type BrowserStatus = "connecting" | "open" | "closed" | "error";
/** Something the page asks the person. All its text comes from the page. */
export type BrowserDialog =
	| { kind: "alert" | "confirm" | "beforeunload"; message: string }
	| { kind: "prompt"; message: string; defaultPrompt?: string }
	| { kind: "auth"; origin: string; realm?: string; failed: boolean }
	| { kind: "select"; id: string; ctx?: string; options: Array<{ label: string; disabled?: boolean }>; selected: number }
	| { kind: "filechooser"; multiple: boolean; accept: string; folder: boolean };
/** A file the page downloaded on the host. */
export type BrowserDownload = { id: string; name: string; size: number };
export type BrowserTransportHandlers = {
	onStatus(status: BrowserStatus, message?: string): void;
	onState(state: BrowserViewState): void;
	onFrame(frame: BrowserFrame): void;
	onDialog?(dialog: BrowserDialog): void;
	/** The host closed the page's dialog itself, as when the page navigated. */
	onDialogClosed?(): void;
	onDownload?(download: BrowserDownload): void;
	// Also used when an older desktop does not support browser streaming.
	onDisabled?(): void;
};

export interface BrowserTransport {
	readonly capabilities: BrowserCapabilities;
	connect(handlers: BrowserTransportHandlers): void;
	send(command: BrowserCommand): void;
	close(): void;
	/** Where to fetch a download the host listed, with whatever sign-in it needs. */
	downloadRequest?(id: string): Promise<{ url: string; headers: Record<string, string> }>;
}

const CONTROLS = new Set<BrowserControl>(["agent", "human", "none"]);

/** Hosts that predate control and focus reporting leave them out. */
export function toViewState(state: BrowserLiveState & { control?: BrowserControl; focusedEditable?: FocusedEditable }): BrowserViewState {
	return {
		...state,
		control: state.control && CONTROLS.has(state.control) ? state.control : "none",
		focusedEditable: state.focusedEditable ?? null,
	};
}

/** Whether a transport with these capabilities can carry the command at all. */
export function commandSupported(capabilities: BrowserCapabilities, command: BrowserCommand): boolean {
	switch (command.type) {
		case "input": {
			const kind = command.payload.kind;
			if (kind === "text") return capabilities.text;
			if (kind === "key") return capabilities.keys;
			return true;
		}
		case "viewport": return capabilities.viewport;
		case "quality": return capabilities.quality;
		case "control": return capabilities.control;
		case "dialog": return capabilities.dialogs;
		case "auth": return capabilities.auth;
		case "select": return capabilities.select;
		case "files": return capabilities.files;
		default: return true;
	}
}
