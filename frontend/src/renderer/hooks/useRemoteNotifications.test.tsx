import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { remotes, dismiss } = vi.hoisted(() => ({
	remotes: { connect: vi.fn(), disconnect: vi.fn(async () => undefined) },
	dismiss: vi.fn(async (_id: string) => undefined),
}));
vi.mock("../lib/bridge", () => ({ aoBridge: { remotes, notifications: { dismiss } } }));

import { connectHost, connectedHosts, disconnectHost } from "../lib/host-clients";
import { useUiStore } from "../stores/ui-store";
import {
	connectRemoteNotificationStreams,
	reconcileRemoteToastsFromHistory,
	reconcileRemoteToastsFromSnapshot,
	remoteNativeNotificationId,
	remoteNotificationsQueryKey,
	trackRemoteToast,
	useRemoteNotificationHosts,
} from "./useRemoteNotifications";

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

async function connect(hostId: string, port: number) {
	const url = `http://${hostId}:3001`;
	remotes.connect.mockResolvedValueOnce({ hostId, label: `Host ${hostId}`, url, base: `http://127.0.0.1:${port}` });
	await connectHost(url);
}

beforeEach(() => useUiStore.setState({ developerMode: true, remoteHosts: true }));

afterEach(async () => {
	useUiStore.setState({ developerMode: false, remoteHosts: false });
	for (const hostId of connectedHosts()) await disconnectHost(hostId);
	remotes.connect.mockReset();
	dismiss.mockReset();
	vi.unstubAllGlobals();
});

it("keeps same-ID notifications and unread counts separate by host, then drops disconnected hosts", async () => {
	const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
		const url = new URL(input instanceof Request ? input.url : String(input));
		const hostId = url.port === "4101" ? "A" : "B";
		return new Response(JSON.stringify({
			notifications: [{ id: "same-id", title: `From ${hostId}`, status: "unread" }],
			unreadCount: hostId === "A" ? 2 : 3,
			unresolvedCount: 0,
		}), { status: 200, headers: { "content-type": "application/json" } });
	});
	vi.stubGlobal("fetch", fetchMock);
	await connect("A", 4101);
	await connect("B", 4102);

	const { result } = renderHook(() => useRemoteNotificationHosts("unread"), { wrapper });
	await waitFor(() => expect(result.current.totalUnreadCount).toBe(5));
	expect(result.current.hosts.map(({ hostId, label, data }) => [hostId, label, data?.notifications[0]?.title])).toEqual([
		["A", "Host A", "From A"],
		["B", "Host B", "From B"],
	]);
	expect(remoteNotificationsQueryKey("A", "unread")).not.toEqual(remoteNotificationsQueryKey("B", "unread"));
	expect(fetchMock.mock.calls.every(([input]) => {
		const url = new URL(input instanceof Request ? input.url : String(input));
		return url.searchParams.get("status") === "unread" && url.searchParams.get("limit") === "100";
	})).toBe(true);

	await act(async () => { await disconnectHost("B"); });
	await waitFor(() => expect(result.current.hosts.map((host) => host.hostId)).toEqual(["A"]));
	expect(result.current.totalUnreadCount).toBe(2);
});

it("does not fetch while disabled and reports a failed host without masking healthy hosts", async () => {
	const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
		const url = new URL(input instanceof Request ? input.url : String(input));
		if (url.port === "4102") return new Response('{"message":"offline"}', { status: 503, headers: { "content-type": "application/json" } });
		return new Response(JSON.stringify({ notifications: [], unreadCount: 4, unresolvedCount: 0 }), {
			status: 200, headers: { "content-type": "application/json" },
		});
	});
	vi.stubGlobal("fetch", fetchMock);
	await connect("A", 4101);
	await connect("B", 4102);

	const { result, rerender } = renderHook(({ enabled }) => useRemoteNotificationHosts("all", enabled), {
		initialProps: { enabled: false }, wrapper,
	});
	expect(fetchMock).not.toHaveBeenCalled();
	expect(result.current.totalUnreadCount).toBe(0);
	rerender({ enabled: true });
	await waitFor(() => expect(result.current.hosts[1]?.isError).toBe(true), { timeout: 3_000 });
	expect(result.current.hosts[0]?.data?.unreadCount).toBe(4);
	expect(result.current.hosts[1]?.data).toBeUndefined();
	expect(result.current.totalUnreadCount).toBe(0);
	expect(remoteNotificationsQueryKey("A", "all")).not.toEqual(remoteNotificationsQueryKey("A", "unread"));
});

it("does not count stale unread data after a host goes offline", async () => {
	let offline = false;
	vi.stubGlobal("fetch", vi.fn(async () => new Response(
		offline ? '{"message":"offline"}' : '{"notifications":[],"unreadCount":4,"unresolvedCount":0}',
		{ status: offline ? 503 : 200, headers: { "content-type": "application/json" } },
	)));
	await connect("A", 4101);
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { result } = renderHook(() => useRemoteNotificationHosts("unread"), {
		wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
	});
	await waitFor(() => expect(result.current.totalUnreadCount).toBe(4));

	offline = true;
	await act(async () => { await queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey("A", "unread") }); });
	await waitFor(() => expect(result.current.hosts[0]?.isError).toBe(true), { timeout: 3_000 });
	expect(result.current.hosts[0]?.data).toBeUndefined();
	expect(result.current.totalUnreadCount).toBe(0);
});

it("dismisses exactly the resolved host's OS toast from its live stream", async () => {
	const sources: { url: string; listeners: Map<string, (event: Event) => void> }[] = [];
	vi.stubGlobal("EventSource", class {
		listeners = new Map<string, (event: Event) => void>();
		onmessage: unknown = null;
		onerror: unknown = null;
		constructor(public url: string) { sources.push(this); }
		addEventListener(type: string, listener: (event: Event) => void) { this.listeners.set(type, listener); }
		close() {}
	});
	vi.stubGlobal("fetch", vi.fn(async () => new Response('{"notifications":[],"unreadCount":0,"unresolvedCount":0}', {
		status: 200, headers: { "content-type": "application/json" },
	})));
	await connect("A", 4101);
	await connect("B", 4102);
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	const close = connectRemoteNotificationStreams(queryClient);
	const streamA = sources.find((source) => source.url.includes(":4101/"))!;
	const deliver = (type: string, data: unknown) =>
		streamA.listeners.get(type)?.({ type, data: JSON.stringify(data) } as MessageEvent<string>);

	// A host still waiting on another approval publishes no resolution: nothing is withdrawn.
	deliver("notification_created", { id: "same-id", status: "unread" });
	expect(dismiss).not.toHaveBeenCalled();
	// A resolution without the authoritative stamp is not trusted either.
	deliver("notification_resolved", { id: "same-id" });
	expect(dismiss).not.toHaveBeenCalled();

	deliver("notification_resolved", { id: "same-id", resolvedAt: "2026-09-30T10:05:00Z" });
	expect(dismiss).toHaveBeenCalledTimes(1);
	expect(dismiss).toHaveBeenCalledWith(remoteNativeNotificationId("A", "same-id"));
	expect(remoteNativeNotificationId("A", "same-id")).not.toBe(remoteNativeNotificationId("B", "same-id"));
	// The history still refetches; dismissal does not stand in for it.
	expect(invalidate).toHaveBeenCalledWith({ queryKey: remoteNotificationsQueryKey("A", "unread") });
	close();
});

it("finds a live remote toast's missed resolution on a later history page, and keeps it when unknown", async () => {
	let failPageTwo = false;
	const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
		const url = new URL(input instanceof Request ? input.url : String(input));
		const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
		if (url.searchParams.get("cursor") === "page-2") {
			if (failPageTwo) return json({ message: "offline" }, 503);
			return json({ notifications: [
				{ id: "older-open", type: "needs_input", status: "read" },
				{ id: "older-approval", type: "needs_input", status: "read", resolvedAt: "2026-09-30T10:05:00Z" },
			], unreadCount: 0, unresolvedCount: 1 });
		}
		return json({ notifications: [{ id: "newer", type: "pr_merged", status: "unread" }], nextCursor: "page-2", unreadCount: 1, unresolvedCount: 1 });
	});
	vi.stubGlobal("fetch", fetchMock);
	await connect("P", 4103);
	trackRemoteToast("P", { id: "older-approval", type: "needs_input" });
	trackRemoteToast("P", { id: "older-open", type: "needs_input" });
	// Informational toasts never resolve, so they are not tracked.
	trackRemoteToast("P", { id: "newer", type: "pr_merged" });

	// A failing page proves nothing.
	failPageTwo = true;
	reconcileRemoteToastsFromSnapshot("P", []);
	await reconcileRemoteToastsFromHistory("P");
	expect(dismiss).not.toHaveBeenCalled();

	failPageTwo = false;
	await reconcileRemoteToastsFromHistory("P");
	expect(dismiss).toHaveBeenCalledTimes(1);
	expect(dismiss).toHaveBeenCalledWith(remoteNativeNotificationId("P", "older-approval"));

	// The still-open one stays tracked: a later snapshot showing it resolved withdraws it.
	reconcileRemoteToastsFromSnapshot("P", [{ id: "older-open", type: "needs_input", status: "read", resolvedAt: "2026-09-30T10:06:00Z" } as never]);
	expect(dismiss).toHaveBeenLastCalledWith(remoteNativeNotificationId("P", "older-open"));
	expect(dismiss).toHaveBeenCalledTimes(2);
});

it("still looks up a toast tracked after an empty reconcile, as on a first stream open", async () => {
	const fetchMock = vi.fn(async () => new Response(JSON.stringify({
		notifications: [{ id: "late-approval", type: "needs_input", status: "unread", resolvedAt: "2026-09-30T10:05:00Z" }],
		unreadCount: 1, unresolvedCount: 0,
	}), { status: 200, headers: { "content-type": "application/json" } }));
	vi.stubGlobal("fetch", fetchMock);
	await connect("E", 4104);

	// The initial onVerified reconcile runs before any toast exists.
	await reconcileRemoteToastsFromHistory("E");
	expect(fetchMock).not.toHaveBeenCalled();

	trackRemoteToast("E", { id: "late-approval", type: "needs_input" });
	await reconcileRemoteToastsFromHistory("E");

	expect(fetchMock).toHaveBeenCalledTimes(1);
	expect(dismiss).toHaveBeenCalledTimes(1);
	expect(dismiss).toHaveBeenCalledWith(remoteNativeNotificationId("E", "late-approval"));
});
