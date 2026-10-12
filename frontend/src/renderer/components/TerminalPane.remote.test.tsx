import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { AttachableTerminal } from "../hooks/useTerminalSession";
import type { TerminalMux } from "../lib/terminal-mux";
import { TerminalCacheProvider, TerminalPane } from "./TerminalPane";
import { TooltipProvider } from "./ui/tooltip";

const state = vi.hoisted(() => ({
	bases: { "box-a": "http://127.0.0.1:41001/proxy-a", "box-b": "http://127.0.0.1:41002/proxy-b" } as Record<string, string>,
	options: new Map<string, { createMux?: () => TerminalMux; daemonReady: boolean }>(),
	queries: [] as Array<{ hostId: string; isSuccess: boolean; data: Array<{ sessions: WorkspaceSession[] }> }>,
	mounts: 0, unmounts: 0,
	attach: vi.fn(() => () => {}), syncVisibleSize: vi.fn(),
}));
vi.mock("../lib/host-clients", async (original) => ({
	...await original<typeof import("../lib/host-clients")>(), baseUrlForHost: (hostId: string) => state.bases[hostId],
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ isSuccess: true, data: [] }),
	useRemoteWorkspaceSnapshots: () => state.queries,
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	cloudSessionsQueryKey: ["cloud-sessions"], workspaceQueryKey: ["workspaces"],
}));
vi.mock("../hooks/useShellTerminals", () => ({ useShellTerminals: () => ({ isSuccess: true, data: [] }) }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: {}, baseUrl: "" }) }));
vi.mock("../lib/use-session-link-navigation", () => ({ useSessionLinkNavigation: () => vi.fn() }));
vi.mock("./RestoreUnavailableDialog", () => ({ RestoreUnavailableDialog: () => null }));
vi.mock("../hooks/useTerminalSession", () => ({
	useTerminalSession: (session: WorkspaceSession, options: { createMux?: () => TerminalMux; daemonReady: boolean }) => {
		state.options.set(`${session.hostId}:${session.id}`, options);
		return { attach: state.attach, syncVisibleSize: state.syncVisibleSize, state: "attached", hasAttached: true, replaySettled: true, replayPaintPending: false };
	},
}));
vi.mock("./XtermTerminal", () => ({
	XtermTerminal: ({ onReady }: { onReady: (terminal: AttachableTerminal) => void }) => {
		useEffect(() => {
			state.mounts++;
			onReady({ cols: 80, rows: 24, hasMeasuredGrid: true,
				write: vi.fn((_data, done) => done?.()), writeln: vi.fn(), showLatestOutput: vi.fn(),
				prepareForActivation: async () => {}, requestActivationFocus: vi.fn(), notifyCursorColorScheme: vi.fn(),
				sendUserInput: () => true, onUserInput: () => ({ dispose() {} }), onResize: () => ({ dispose() {} }),
			});
			return () => { state.unmounts++; };
		}, []);
		return <div data-testid="remote-xterm" />;
	},
}));

class Socket {
	static OPEN = 1;
	static instances: Socket[] = [];
	readyState = 0;
	closed = false;
	constructor(readonly url: string) { Socket.instances.push(this); }
	addEventListener() {}
	send() {}
	close() { this.closed = true; }
}
function worker(id: string, hostId: string): WorkspaceSession {
	return { id, hostId, title: id, mode: "tui", kind: "worker", terminalHandleId: `handle-${id}`, terminalGeneration: "epoch-a", workspaceId: "shared", workspaceName: "Project", provider: "opencode", status: "working", updatedAt: "now", prs: [] };
}
function tree(sessions: WorkspaceSession[], client: QueryClient) {
	return <QueryClientProvider client={client}><TooltipProvider><TerminalCacheProvider daemonReady={false} theme="dark">
		{sessions.map((session) => <TerminalPane key={`${session.hostId}:${session.id}`} session={session} daemonReady theme="dark" fontSize={12} focusRequested={false} />)}
	</TerminalCacheProvider></TooltipProvider></QueryClientProvider>;
}
beforeEach(() => {
	state.bases = { "box-a": "http://127.0.0.1:41001/proxy-a", "box-b": "http://127.0.0.1:41002/proxy-b" };
	state.options.clear(); state.mounts = 0; state.unmounts = 0; Socket.instances = [];
	state.queries = ["box-a", "box-b"].map((hostId) => ({ hostId, isSuccess: true, data: [{ sessions: [worker("same", hostId), worker("second", hostId)] }] }));
	vi.stubGlobal("WebSocket", Socket);
});
afterEach(() => vi.unstubAllGlobals());

it("retains distinct host-qualified terminals and shares exactly one mux per host", async () => {
	const client = new QueryClient();
	const sessions = [worker("same", "box-a"), worker("second", "box-a"), worker("same", "box-b")];
	const view = render(tree(sessions, client));
	await waitFor(() => expect(state.mounts).toBe(3));
	const first = state.options.get("box-a:same")!.createMux!;
	const second = state.options.get("box-a:second")!.createMux!;
	const other = state.options.get("box-b:same")!.createMux!;
	expect(first).toBe(second); expect(first).not.toBe(other);
	const leases = [first(), second(), other()];
	expect(Socket.instances.map((socket) => socket.url)).toEqual(["ws://127.0.0.1:41001/proxy-a/mux", "ws://127.0.0.1:41002/proxy-b/mux"]);
	expect(state.options.get("box-a:same")!.daemonReady).toBe(true);
	const node = screen.getAllByTestId("remote-xterm")[0];
	view.rerender(tree([], client));
	expect(node.isConnected).toBe(true);
	view.rerender(tree(sessions, client));
	expect(screen.getAllByTestId("remote-xterm")[0]).toBe(node);
	expect(state.mounts).toBe(3); expect(state.unmounts).toBe(0);
	view.unmount();
	expect(Socket.instances.every((socket) => socket.closed)).toBe(true);
	for (const lease of leases) lease.dispose();
});

it("preserves remote buffers on failed snapshots and disposes only an authoritatively removed session", async () => {
	const client = new QueryClient(); const sessions = [worker("same", "box-a"), worker("same", "box-b")];
	const view = render(tree(sessions, client));
	await waitFor(() => expect(state.mounts).toBe(2));
	view.rerender(tree([], client));
	state.queries = state.queries.map((query) => query.hostId === "box-a" ? { ...query, isSuccess: false, data: [] } : query);
	view.rerender(tree([], client));
	expect(state.unmounts).toBe(0);
	state.queries = state.queries.map((query) => query.hostId === "box-a" ? { ...query, isSuccess: true } : query);
	view.rerender(tree([], client));
	await waitFor(() => expect(state.unmounts).toBe(1));
	view.rerender(tree([sessions[1]], client));
	expect(state.mounts).toBe(2);
});

it("replaces a changed proxy transport while retaining the original xterm DOM", async () => {
	const client = new QueryClient(); const sessions = [worker("same", "box-a")];
	const view = render(tree(sessions, client));
	await waitFor(() => expect(state.mounts).toBe(1));
	const node = screen.getByTestId("remote-xterm");
	const oldLease = state.options.get("box-a:same")!.createMux!();
	state.bases["box-a"] = "http://127.0.0.1:41003/new";
	act(() => view.rerender(tree(sessions, client)));
	expect(Socket.instances[0].closed).toBe(true);
	const newLease = state.options.get("box-a:same")!.createMux!();
	expect(Socket.instances[1].url).toBe("ws://127.0.0.1:41003/new/mux");
	expect(screen.getByTestId("remote-xterm")).toBe(node); expect(state.mounts).toBe(1);
	oldLease.dispose(); newLease.dispose(); view.unmount();
});
