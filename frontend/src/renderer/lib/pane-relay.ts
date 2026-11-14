// The window's side of agent-driven split panes. An agent runs `ao pane`; the
// daemon relays the request over the terminal mux (ch "panes") to every window
// that subscribed, and the window showing that session applies it and answers
// with the new layout. Each session view on screen also reports its layout as
// it changes, so `ao pane ls` can answer without asking the window.
//
// One socket per window, opened while a session view is registered (local
// sessions in Developer Mode only; see SessionView). Nothing is persisted.

import type { components } from "../../api/schema";
import { getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { layoutBoxes, leafKeys, type TabGroup } from "./pane-split";
import { muxUrlFromApiBase } from "./terminal-mux";

export type PaneLayout = components["schemas"]["ControllersPaneLayout"];
export type PaneCommandRequest = components["schemas"]["ControllersPaneCommandRequest"];

/** Thrown by a handler to refuse a request with a code the agent sees. */
export class PaneCommandError extends Error {
	constructor(
		readonly code: string,
		message: string,
	) {
		super(message);
	}
}

/** Applies one request to a session on screen and returns its new layout. */
export type PaneCommandHandler = (request: PaneCommandRequest) => Promise<PaneLayout>;

type CommandFrame = { id: string; sessionId: string; action: string; args?: PaneCommandRequest };

const RETRY_MS = 5_000;
export const NOT_SHOWN = "SESSION_NOT_ON_SCREEN";

const handlers = new Map<string, PaneCommandHandler>();
const layouts = new Map<string, PaneLayout>();
let socket: WebSocket | undefined;
let open = false;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let unsubscribeBaseUrl: (() => void) | undefined;

function send(frame: Record<string, unknown>) {
	if (socket && open) socket.send(JSON.stringify({ ch: "panes", ...frame }));
}

function connect() {
	if (handlers.size === 0 || socket || typeof WebSocket === "undefined" || !hasTrustedApiBaseUrl()) return;
	const ws = new WebSocket(muxUrlFromApiBase(getApiBaseUrl()));
	socket = ws;
	ws.onopen = () => {
		if (socket !== ws) return;
		open = true;
		send({ type: "subscribe" });
		// The daemon forgets a window's layouts when its socket closes.
		for (const [sessionId, layout] of layouts) send({ type: "layout", sessionId, layout });
	};
	ws.onmessage = (event: MessageEvent) => {
		if (socket !== ws) return;
		const command = parseCommand(event.data);
		if (command) void answer(command);
	};
	ws.onclose = () => {
		if (socket !== ws) return;
		socket = undefined;
		open = false;
		retryTimer = setTimeout(() => {
			retryTimer = undefined;
			connect();
		}, RETRY_MS);
	};
}

function disconnect() {
	clearTimeout(retryTimer);
	retryTimer = undefined;
	const ws = socket;
	socket = undefined;
	open = false;
	ws?.close();
}

function parseCommand(data: unknown): CommandFrame | null {
	try {
		const frame: unknown = JSON.parse(String(data));
		if (!frame || typeof frame !== "object" || !("ch" in frame) || frame.ch !== "panes" || !("type" in frame) || frame.type !== "command") return null;
		const command = "command" in frame ? frame.command : undefined;
		if (!command || typeof command !== "object") return null;
		const { id, sessionId, action, args } = command as Record<string, unknown>;
		if (typeof id !== "string" || typeof sessionId !== "string" || typeof action !== "string") return null;
		const request = (args && typeof args === "object" ? args : { action }) as PaneCommandRequest;
		return { id, sessionId, action, args: { ...request, action: action as PaneCommandRequest["action"] } };
	} catch {
		return null;
	}
}

async function answer(command: CommandFrame) {
	const handler = handlers.get(command.sessionId);
	if (!handler) {
		send({ type: "result", result: { id: command.id, ok: false, code: NOT_SHOWN, message: "This session is not open in the AO window." } });
		return;
	}
	try {
		const layout = await handler(command.args ?? { action: command.action as PaneCommandRequest["action"] });
		send({ type: "result", result: { id: command.id, ok: true, layout } });
	} catch (error) {
		const code = error instanceof PaneCommandError ? error.code : "PANE_COMMAND_FAILED";
		const message = error instanceof Error ? error.message : String(error);
		send({ type: "result", result: { id: command.id, ok: false, code, message } });
	}
}

/**
 * Registers the session view on screen for a session: it answers that
 * session's requests until the returned function is called.
 */
export function registerPaneSession(sessionId: string, handler: PaneCommandHandler): () => void {
	handlers.set(sessionId, handler);
	if (handlers.size === 1 && !unsubscribeBaseUrl) {
		unsubscribeBaseUrl = subscribeApiBaseUrl(() => {
			disconnect();
			connect();
		});
	}
	connect();
	return () => {
		if (handlers.get(sessionId) !== handler) return;
		handlers.delete(sessionId);
		layouts.delete(sessionId);
		send({ type: "layout", sessionId, layout: null });
		if (handlers.size > 0) return;
		unsubscribeBaseUrl?.();
		unsubscribeBaseUrl = undefined;
		disconnect();
	};
}

/** Reports what a registered session shows now. */
export function reportPaneLayout(sessionId: string, layout: PaneLayout): void {
	if (!handlers.has(sessionId)) return;
	layouts.set(sessionId, layout);
	send({ type: "layout", sessionId, layout });
}

/** The last layout reported for a session. */
export function latestPaneLayout(sessionId: string): PaneLayout | undefined {
	return layouts.get(sessionId);
}

export type PaneTabDescription = PaneLayout["tabs"][number];

/** What a session shows, in the shape agents read from `ao pane`. */
export function describePaneLayout(input: {
	enabled: boolean;
	selected: string;
	shownGroupId?: string;
	tabs: PaneTabDescription[];
	groups: readonly TabGroup[];
}): PaneLayout {
	const round = (value: number) => Math.round(value * 1000) / 1000;
	return {
		enabled: input.enabled,
		selected: input.selected,
		...(input.shownGroupId ? { shown: input.shownGroupId } : {}),
		tabs: input.tabs,
		groups: input.groups.map((group) => {
			const keys = leafKeys(group.root);
			return {
				id: group.id,
				focus: group.focus && keys.includes(group.focus) ? group.focus : keys[0]!,
				panes: [...layoutBoxes(group.root).leaves].map(([key, box]) => ({
					key,
					x: round(box.x),
					y: round(box.y),
					width: round(box.w),
					height: round(box.h),
				})),
			};
		}),
	};
}
