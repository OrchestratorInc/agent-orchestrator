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
	| { type: "dialog"; payload: { accept: boolean; promptText?: string } };
export type BrowserCapabilities = { text: boolean; keys: boolean; viewport: boolean; quality: boolean; control: boolean; dialogs: boolean };
export type BrowserStatus = "connecting" | "open" | "closed" | "error";
export type BrowserDialog = { kind: "alert" | "confirm" | "prompt" | "beforeunload" | "auth"; message: string };
export type BrowserTransportHandlers = {
	onStatus(status: BrowserStatus, message?: string): void;
	onState(state: BrowserViewState): void;
	onFrame(frame: BrowserFrame): void;
	onDialog?(dialog: BrowserDialog): void;
	// Also used when an older desktop does not support browser streaming.
	onDisabled?(): void;
	/**
	 * Any message arrived. `heartbeat` is true for a host that sends one on a
	 * schedule (a Cloud pong, the daemon's heartbeat): only then does silence
	 * mean the stream is gone, since a still page sends nothing.
	 */
	onAlive?(info: { heartbeat: boolean }): void;
};

export interface BrowserTransport {
	readonly capabilities: BrowserCapabilities;
	connect(handlers: BrowserTransportHandlers): void;
	send(command: BrowserCommand): void;
	close(): void;
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
		default: return true;
	}
}
