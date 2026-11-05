// Coding agents the user started by hand inside a shell tab (claude, codex,
// gemini, ...). The local daemon names each one from the terminal's foreground
// process and streams its state; the tab swaps its terminal icon for the
// agent's logo and shows whether it is working, done or waiting on the user.
// Nothing here is persisted: when the agent exits, its entry disappears and the
// tab goes back to a plain terminal.

import { useEffect } from "react";
import { create } from "zustand";
import type { components } from "../../api/schema";
import { eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";

type ShellTerminalAgentsResponse = components["schemas"]["ShellTerminalAgentsResponse"];

export type ShellAgentState = "working" | "idle" | "waiting";

export type ShellAgent = {
	harness: string;
	state: ShellAgentState;
};

type ShellAgentStore = {
	agents: Record<string, ShellAgent>;
	/**
	 * Tabs whose agent finished a turn the user has not looked at yet. The tab
	 * keeps a "done" dot until it is selected, so a finished agent stands out
	 * from one that was already idle.
	 */
	unseenDone: Record<string, true>;
	replace: (agents: ShellTerminalAgentsResponse["agents"]) => void;
	markSeen: (handleId: string) => void;
};

export const useShellAgentStore = create<ShellAgentStore>((set) => ({
	agents: {},
	unseenDone: {},
	replace: (list) =>
		set((current) => {
			const agents: Record<string, ShellAgent> = {};
			const unseenDone: Record<string, true> = {};
			for (const entry of list ?? []) {
				const agent: ShellAgent = { harness: entry.harness, state: entry.state };
				agents[entry.handleId] = agent;
				const previous = current.agents[entry.handleId];
				const finishedNow = previous?.state === "working" && agent.state === "idle";
				if (agent.state === "idle" && (finishedNow || current.unseenDone[entry.handleId])) {
					unseenDone[entry.handleId] = true;
				}
			}
			return { agents, unseenDone };
		}),
	markSeen: (handleId) =>
		set((current) => {
			if (!current.unseenDone[handleId]) return current;
			const unseenDone = { ...current.unseenDone };
			delete unseenDone[handleId];
			return { unseenDone };
		}),
}));

const EVENTSOURCE_CLOSED = 2;
const RETRY_MS = 5_000;

let listeners = 0;
let source: EventSource | undefined;
let sourceBaseUrl: string | undefined;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let unsubscribeBaseUrl: (() => void) | undefined;

function applyEvent(event: MessageEvent) {
	try {
		const body = JSON.parse(String(event.data)) as ShellTerminalAgentsResponse;
		useShellAgentStore.getState().replace(body.agents ?? []);
	} catch {
		// A malformed frame changes nothing; the next one replaces everything.
	}
}

function disconnect() {
	if (retryTimer) clearTimeout(retryTimer);
	retryTimer = undefined;
	source?.close();
	source = undefined;
	sourceBaseUrl = undefined;
}

function connect() {
	if (listeners === 0 || typeof EventSource === "undefined" || !hasTrustedApiBaseUrl()) return;
	const baseUrl = getApiBaseUrl();
	if (source && sourceBaseUrl === baseUrl && source.readyState !== EVENTSOURCE_CLOSED) return;
	disconnect();
	sourceBaseUrl = baseUrl;
	try {
		const connected = new EventSource(`${eventStreamBaseUrl(baseUrl)}/api/v1/shell-terminals/agents/events`);
		source = connected;
		connected.addEventListener("shell_terminal_agents", applyEvent as EventListener);
		connected.onerror = () => {
			if (source !== connected) return;
			// States are live facts: while the stream is down, show plain
			// terminals rather than a status that may already be stale. The
			// first frame after reconnecting restores them.
			useShellAgentStore.getState().replace([]);
			if (connected.readyState === EVENTSOURCE_CLOSED && !retryTimer) {
				retryTimer = setTimeout(() => {
					retryTimer = undefined;
					connect();
				}, RETRY_MS);
			}
		};
	} catch {
		source = undefined;
	}
}

/**
 * Keeps the agent stream open while at least one shell tab is mounted. The
 * stream is shared: each tab only reads its own entry from the store.
 */
export function useShellAgentStream(enabled: boolean): void {
	useEffect(() => {
		if (!enabled) return;
		listeners += 1;
		if (listeners === 1) unsubscribeBaseUrl = subscribeApiBaseUrl(connect);
		connect();
		return () => {
			listeners -= 1;
			if (listeners > 0) return;
			unsubscribeBaseUrl?.();
			unsubscribeBaseUrl = undefined;
			disconnect();
			useShellAgentStore.getState().replace([]);
		};
	}, [enabled]);
}
