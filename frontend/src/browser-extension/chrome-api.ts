// The slice of Chrome's extension API the AO Browser Bridge uses, typed
// locally so the frontend does not need @types/chrome. MV3 methods return
// promises when called without a callback.

type Listener<A extends unknown[]> = {
	addListener(callback: (...args: A) => void): void;
	removeListener(callback: (...args: A) => void): void;
};

export type Tab = {
	id?: number;
	windowId: number;
	groupId: number;
	url?: string;
	pendingUrl?: string;
	title?: string;
	active: boolean;
	status?: "loading" | "complete" | "unloaded";
	favIconUrl?: string;
};

export type TabGroup = { id: number; windowId: number; title?: string; color: string; collapsed: boolean };
export type WindowInfo = { id?: number; focused: boolean; type?: string; state?: string };
export type Frame = { frameId: number; parentFrameId: number; url: string; documentId?: string };
export type DebuggeeTarget = { tabId: number };
export type MessageSender = { tab?: Tab; frameId?: number; id?: string; url?: string };

export type Port = {
	name: string;
	postMessage(message: unknown): void;
	disconnect(): void;
	onMessage: Listener<[unknown, Port]>;
	onDisconnect: Listener<[Port]>;
};

export type WebRequestDetails = {
	requestId: string;
	tabId: number;
	frameId: number;
	url: string;
	method: string;
	type: string;
	timeStamp: number;
	statusCode?: number;
	error?: string;
	fromCache?: boolean;
	initiator?: string;
};

type WebRequestEvent = {
	addListener(callback: (details: WebRequestDetails) => void, filter: { urls: string[]; tabId?: number }): void;
	removeListener(callback: (details: WebRequestDetails) => void): void;
};

export type NavigationDetails = { tabId: number; frameId: number; url: string; error?: string; transitionType?: string };

export type ChromeApi = {
	runtime: {
		id: string;
		lastError?: { message?: string };
		connectNative(application: string): Port;
		getManifest(): { version: string };
		getURL(path: string): string;
		sendMessage(message: unknown): Promise<unknown>;
		onMessage: Listener<[unknown, MessageSender, (response?: unknown) => void]>;
		onStartup: Listener<[]>;
		onInstalled: Listener<[unknown]>;
	};
	tabs: {
		create(props: { url?: string; active?: boolean; windowId?: number; index?: number }): Promise<Tab>;
		update(tabId: number, props: { url?: string; active?: boolean }): Promise<Tab>;
		get(tabId: number): Promise<Tab>;
		remove(tabIds: number | number[]): Promise<void>;
		query(query: { groupId?: number; windowId?: number; active?: boolean }): Promise<Tab[]>;
		group(options: { tabIds: number | number[]; groupId?: number; createProperties?: { windowId?: number } }): Promise<number>;
		ungroup(tabIds: number | number[]): Promise<void>;
		sendMessage(tabId: number, message: unknown, options?: { frameId?: number }): Promise<unknown>;
		captureVisibleTab(windowId: number, options: { format: "png" | "jpeg" }): Promise<string>;
		goBack(tabId: number): Promise<void>;
		onRemoved: Listener<[number, { windowId: number; isWindowClosing: boolean }]>;
		onActivated: Listener<[{ tabId: number; windowId: number }]>;
		onUpdated: Listener<[number, { status?: string; url?: string; groupId?: number; title?: string }, Tab]>;
	};
	tabGroups: {
		update(groupId: number, props: { title?: string; color?: string; collapsed?: boolean }): Promise<TabGroup>;
		get(groupId: number): Promise<TabGroup>;
		onRemoved: Listener<[TabGroup]>;
	};
	windows: {
		create(props: { url?: string; type?: "normal" | "popup"; width?: number; height?: number; focused?: boolean }): Promise<WindowInfo & { tabs?: Tab[] }>;
		getLastFocused(options?: { windowTypes?: string[] }): Promise<WindowInfo>;
		update(windowId: number, props: { focused?: boolean; drawAttention?: boolean }): Promise<WindowInfo>;
		remove(windowId: number): Promise<void>;
		onRemoved: Listener<[number]>;
	};
	scripting: {
		executeScript(injection: { target: { tabId: number; frameIds?: number[]; allFrames?: boolean }; } & ({ files: string[] } | {
			func: (action: "track" | "observed" | "release", pointerIds?: number[]) => boolean | "idle" | number;
			args: ["track" | "observed" | "release", number[]?];
			world: "ISOLATED";
		})): Promise<{ result?: unknown }[]>;
	};
	debugger: {
		attach(target: DebuggeeTarget, version: string): Promise<void>;
		detach(target: DebuggeeTarget): Promise<void>;
		sendCommand(target: DebuggeeTarget, method: string, params?: Record<string, unknown>): Promise<unknown>;
		onEvent: Listener<[DebuggeeTarget, string, unknown]>;
		onDetach: Listener<[DebuggeeTarget, string]>;
	};
	permissions: {
		contains(permissions: { permissions?: string[]; origins?: string[] }): Promise<boolean>;
		request(permissions: { permissions?: string[]; origins?: string[] }): Promise<boolean>;
	};
	storage: {
		local: StorageArea;
		session: StorageArea;
	};
	webNavigation: {
		getAllFrames(details: { tabId: number }): Promise<Frame[] | null>;
		onCommitted: Listener<[NavigationDetails]>;
		onCompleted: Listener<[NavigationDetails]>;
		onErrorOccurred: Listener<[NavigationDetails]>;
	};
	webRequest: {
		onBeforeRequest: WebRequestEvent;
		onCompleted: WebRequestEvent;
		onErrorOccurred: WebRequestEvent;
	};
	action: {
		setBadgeText(details: { text: string }): Promise<void>;
		setBadgeBackgroundColor(details: { color: string }): Promise<void>;
		setTitle(details: { title: string }): Promise<void>;
	};
};

export type StorageArea = {
	get(keys: string | string[]): Promise<Record<string, unknown>>;
	set(items: Record<string, unknown>): Promise<void>;
	remove(keys: string | string[]): Promise<void>;
};

export function chromeApi(): ChromeApi {
	return (globalThis as unknown as { chrome: ChromeApi }).chrome;
}
