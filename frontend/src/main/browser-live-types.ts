export type BrowserLiveFrame = {
	sequence: bigint;
	capturedAtMs: bigint;
	width: number;
	height: number;
	jpeg: Buffer;
};

export type BrowserLiveTab = { id: string; title: string; url: string; active: boolean };

export type BrowserLiveState = {
	url: string;
	title: string;
	loading: boolean;
	canGoBack: boolean;
	canGoForward: boolean;
	activeTabId: string;
	tabs: BrowserLiveTab[];
	width: number;
	height: number;
	/** What the page may ask the phone here; older desktops send none. */
	asks?: Array<"auth" | "files">;
};

/** Something the page asks the person on the phone. */
export type BrowserLiveAsk =
	| { kind: "auth"; origin: string; realm?: string; failed: boolean }
	// `id` names this chooser; the phone's files must name it back.
	| { kind: "filechooser"; id: string; multiple: boolean; accept: string; folder: boolean };

export type BrowserLiveSink = {
	frame(frame: BrowserLiveFrame): void;
	state(state: BrowserLiveState): void;
	error(code: string, message: string): void;
	ask?(ask: BrowserLiveAsk): void;
	/** The page no longer asks (it timed out, or the page moved on). */
	askClosed?(): void;
	/** A line for the phone to show while the stream stays healthy. */
	notice?(message: string): void;
};

/** The phone's answer to a sign-in. Never stored or logged. */
export type BrowserRemoteAuth = { origin: string; username: string; password: string } | { origin: string; cancel: true };

/** The phone's files for the page's file input, in base64 chunks of at most 32 KiB. */
export type BrowserRemoteFiles =
	| { action: "chunk"; chooser: string; file: number; name: string; offset: number; data: string }
	| { action: "done"; chooser: string; count: number }
	| { action: "cancel"; chooser: string };

export type BrowserRemoteInput =
	| { kind: "pointer"; phase: "move" | "down" | "up"; x: number; y: number; button?: "left" | "middle" | "right" }
	| { kind: "wheel"; deltaX: number; deltaY: number }
	| { kind: "key"; phase: "down" | "up"; key: string; code: string; modifiers?: string[] }
	| { kind: "text"; value: string };

export type BrowserRemoteNavigation = {
	action: "open" | "back" | "forward" | "reload" | "stop";
	url?: string;
};

export type BrowserRemoteTabAction = {
	action: "new" | "select" | "close";
	tabId?: string;
	url?: string;
};
