// Coding agents the user started by hand in a shell tab (claude, codex, ...).
// The daemon names each from the terminal's foreground program and pushes the
// set over the terminal mux. Nothing is persisted: when an agent exits, its tab
// goes back to a plain terminal.

import { useEffect } from "react";
import { create } from "zustand";
import { getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { muxUrlFromApiBase } from "../lib/terminal-mux";

export type ShellAgentState = "working" | "idle" | "waiting";

export type ShellAgent = {
	harness: string;
	state: ShellAgentState;
};

type Snapshot = ({ handleId: string } & ShellAgent)[];

type ShellAgentStore = {
	agents: Record<string, ShellAgent>;
	/** Tabs whose agent finished a turn the user has not seen; cleared on select. */
	unseenDone: Record<string, true>;
	replace: (snapshot: Snapshot) => void;
	/** Drops the agents while disconnected but keeps the done marks. */
	clearAgents: () => void;
	markSeen: (handleId: string) => void;
};

export const useShellAgentStore = create<ShellAgentStore>((set) => ({
	agents: {},
	unseenDone: {},
	replace: (snapshot) =>
		set((current) => {
			const agents: Record<string, ShellAgent> = {};
			const unseenDone: Record<string, true> = {};
			for (const { handleId, harness, state } of snapshot) {
				agents[handleId] = { harness, state };
				const finished = current.agents[handleId]?.state === "working" || current.unseenDone[handleId];
				if (state === "idle" && finished) unseenDone[handleId] = true;
			}
			return { agents, unseenDone };
		}),
	clearAgents: () => set({ agents: {} }),
	markSeen: (handleId) =>
		set((current) => {
			if (!current.unseenDone[handleId]) return current;
			const unseenDone = { ...current.unseenDone };
			delete unseenDone[handleId];
			return { unseenDone };
		}),
}));

const RETRY_MS = 5_000;

let listeners = 0;
let socket: WebSocket | undefined;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let unsubscribeBaseUrl: (() => void) | undefined;

function connect() {
	if (listeners === 0 || socket || typeof WebSocket === "undefined" || !hasTrustedApiBaseUrl()) return;
	const ws = new WebSocket(muxUrlFromApiBase(getApiBaseUrl()));
	socket = ws;
	ws.onopen = () => {
		if (socket === ws) ws.send(JSON.stringify({ ch: "shell-agents", type: "subscribe" }));
	};
	ws.onmessage = (event: MessageEvent) => {
		if (socket !== ws) return;
		try {
			const frame: unknown = JSON.parse(String(event.data));
			if (!frame || typeof frame !== "object" || !("ch" in frame) || frame.ch !== "shell-agents"
				|| !("type" in frame) || frame.type !== "snapshot" || !("agents" in frame) || !Array.isArray(frame.agents)) return;
			const entries: unknown[] = frame.agents;
			const agents: Snapshot = [];
			for (const entry of entries) {
				if (!entry || typeof entry !== "object" || !("handleId" in entry) || typeof entry.handleId !== "string"
					|| !("harness" in entry) || typeof entry.harness !== "string" || !("state" in entry)
					|| (entry.state !== "working" && entry.state !== "idle" && entry.state !== "waiting")) return;
				agents.push({ handleId: entry.handleId, harness: entry.harness, state: entry.state });
			}
			useShellAgentStore.getState().replace(agents);
		} catch {
			// A malformed frame changes nothing; the next snapshot replaces everything.
		}
	};
	ws.onclose = () => {
		if (socket !== ws) return;
		socket = undefined;
		// Statuses are live facts: show plain tabs rather than stale ones.
		useShellAgentStore.getState().clearAgents();
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
	useShellAgentStore.getState().clearAgents();
	ws?.close();
}

/** Keeps one shared mux subscription open while any shell tab is mounted. */
export function useShellAgentStream(enabled: boolean): void {
	useEffect(() => {
		if (!enabled) return;
		listeners += 1;
		if (listeners === 1) {
			unsubscribeBaseUrl = subscribeApiBaseUrl(() => {
				disconnect();
				connect();
			});
		}
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
