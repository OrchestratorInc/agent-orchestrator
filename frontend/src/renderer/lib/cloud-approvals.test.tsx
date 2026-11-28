import { describe, expect, it, vi } from "vitest";
import type { CloudCpBrowserApproval, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";

const hooks = vi.hoisted(() => ({
	ready: true,
	userId: "me",
	listBrowserApprovals: vi.fn(),
	sessions: [] as Array<{ id: string; cloud?: { orgId: string; observedState?: string; desiredState?: string; createdByUserId?: string; requestedBy?: { userId: string } } }>,
	// The local daemon is down: the merged workspace list has nothing yet.
	localDown: false,
	// AO Cloud's own session list, read without the local daemon.
	cloudSessions: [] as Array<{ id: string; observedState?: string; desiredState?: string; createdByUserId?: string; requestedBy?: { userId: string } }>,
	tap: undefined as undefined | ((target: { baseUrl: string; orgId: string; sessionId: string }, event: { type: string }) => void),
}));
vi.mock("./cloud-cp/session-events-hub", () => ({
	tapCloudSessionEvents: (listener: NonNullable<typeof hooks.tap>) => {
		hooks.tap = listener;
		return () => {
			hooks.tap = undefined;
		};
	},
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: hooks.ready, baseUrl: "https://cp.test/", userId: hooks.userId, client: { listBrowserApprovals: hooks.listBrowserApprovals } }),
}));
vi.mock("./telemetry", () => ({ captureRendererException: vi.fn() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: hooks.localDown ? undefined : [{ id: "p", sessions: hooks.sessions }] }),
	useCloudSessionsQuery: () => ({ data: hooks.cloudSessions }),
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));

import { QueryClient, QueryClientProvider, focusManager } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { act, type ReactNode } from "react";
import {
	decideCloudApproval,
	fetchCloudPendingApprovals,
	nextExpiryIn,
	toPendingApproval,
	useCloudPendingApprovals,
	useLiveCloudPendingApprovals,
	useWatchCloudApprovals,
} from "./cloud-approvals";

const TARGET = { baseUrl: "https://cp.test/", orgId: "org-1", sessionId: "s-1" };

function cloudRequest(over: Partial<CloudCpBrowserApproval> = {}): CloudCpBrowserApproval {
	return {
		id: "a-1", sessionId: "s-1", action: "click", category: "pay", reason: "It reads like a payment.",
		site: "shop.example", summary: 'Click "Place order" on shop.example',
		createdAt: "2026-11-17T10:00:00Z", expiresAt: "2026-11-17T10:01:30Z", ...over,
	};
}

describe("cloud approvals", () => {
	it("turns a cloud request into the local prompt's shape, routed back to the cloud", () => {
		expect(toPendingApproval(cloudRequest(), TARGET)).toEqual({
			id: "a-1", sessionId: "s-1", kind: "browser-action", category: "payment", site: "shop.example",
			action: 'Click "Place order" on shop.example', reason: "It reads like a payment.",
			createdAt: "2026-11-17T10:00:00Z", expiresAt: "2026-11-17T10:01:30Z",
			cloud: { baseUrl: "https://cp.test", orgId: "org-1" },
		});
	});

	it("waits for the decider when the caller may not decide, and stays decidable when the server does not say", () => {
		expect(toPendingApproval(cloudRequest({ canDecide: false, decider: { userId: "u-1", name: "Priya Shah" } }), TARGET).waitingFor).toEqual({ name: "Priya Shah" });
		expect(toPendingApproval(cloudRequest({ canDecide: false }), TARGET).waitingFor).toEqual({ name: undefined });
		expect(toPendingApproval(cloudRequest({ canDecide: true, decider: { name: "Me" } }), TARGET).waitingFor).toBeUndefined();
		expect(toPendingApproval(cloudRequest(), TARGET).waitingFor).toBeUndefined();
	});

	it("names every cloud category with the local prompt's words", () => {
		const names = ["pay", "send", "post", "delete", "account", "script", "unknown"].map(
			(category) => toPendingApproval(cloudRequest({ category: category as CloudCpBrowserApproval["category"] }), TARGET).category,
		);
		expect(names).toEqual(["payment", "message", "publish", "delete", "account", "script", "unknown"]);
	});

	it("reads a session's pending list", async () => {
		const listBrowserApprovals = vi.fn(async () => ({ pending: [cloudRequest()] }));
		const result = await fetchCloudPendingApprovals({ listBrowserApprovals } as unknown as CloudCpClient, TARGET);
		expect(listBrowserApprovals).toHaveBeenCalledWith("org-1", "s-1");
		expect(result.map((request) => request.id)).toEqual(["a-1"]);
	});

	it("sends the decision to the session's decide route", async () => {
		const decideBrowserApproval = vi.fn(async () => ({ id: "a-1", decision: "allow-site" as const }));
		await expect(decideCloudApproval({ decideBrowserApproval } as unknown as CloudCpClient, TARGET, "a-1", "allow-site")).resolves.toBe("decided");
		expect(decideBrowserApproval).toHaveBeenCalledWith("org-1", "s-1", "a-1", { decision: "allow-site" });
	});

	it("treats a request that already ended as answered, and reports other failures", async () => {
		const failWith = (status: number) =>
			({ decideBrowserApproval: vi.fn(async () => { throw new CloudCpError("no", { status }); }) }) as unknown as CloudCpClient;
		await expect(decideCloudApproval(failWith(409), TARGET, "a-1", "deny")).resolves.toBe("ended");
		await expect(decideCloudApproval(failWith(404), TARGET, "a-1", "deny")).resolves.toBe("ended");
		await expect(decideCloudApproval(failWith(403), TARGET, "a-1", "deny")).rejects.toThrow("no");
	});

	it("says when the next request expires, to reread then", () => {
		const soon = toPendingApproval(cloudRequest({ id: "soon", expiresAt: "2026-11-17T10:00:10Z" }), TARGET);
		const later = toPendingApproval(cloudRequest({ id: "later", expiresAt: "2026-11-17T10:01:00Z" }), TARGET);
		const now = Date.parse("2026-11-17T10:00:05Z");
		expect(nextExpiryIn([soon, later], now)).toBe(5_000);
		expect(nextExpiryIn([soon], now + 5_000)).toBeUndefined();
	});

	it("keeps a request the control plane still lists, whatever this machine's clock says", async () => {
		// expiresAt by the control plane's clock is already past by this one's: a clock running ahead.
		hooks.sessions = [{ id: "s-skew", cloud: { orgId: "org-1", desiredState: "running", observedState: "running" } }];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({
			pending: sessionId === "s-skew" ? [cloudRequest({ id: "skewed", sessionId: "s-skew", expiresAt: new Date(Date.now() - 60_000).toISOString() })] : [],
		}));
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		const reader = renderHook(() => useCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(shell.result.current.map((request) => request.id)).toContain("skewed"));
		expect(reader.result.current.map((request) => request.id)).toContain("skewed");
		shell.unmount();
		client.clear();
	});

	it("stops reading a session the control plane no longer has", async () => {
		hooks.sessions = [];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => {
			if (sessionId === "s-gone") throw new CloudCpError("not found", { status: 404 });
			return { pending: [] };
		});
		const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(hooks.tap).toBeDefined());
		act(() => hooks.tap?.({ baseUrl: "https://cp.test", orgId: "org-1", sessionId: "s-gone" }, { type: "browser.approval_requested" }));
		await waitFor(() => expect(hooks.listBrowserApprovals.mock.calls.map(([, id]) => id)).toContain("s-gone"));
		await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ["approvals", "pending", "cloud"] }).some((q) => q.queryKey.includes("s-gone") && q.getObserversCount() > 0)).toBe(false));
		expect(warn).not.toHaveBeenCalledWith(expect.stringContaining("s-gone"), expect.anything());
		warn.mockRestore();
		shell.unmount();
		client.clear();
	});

	it("reads only running cloud sessions, and every other reader sees the result without fetching", async () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		hooks.sessions = [
			{ id: "local-1" },
			{ id: "s-1", cloud: { orgId: "org-1", observedState: "running" } },
			{ id: "s-2", cloud: { orgId: "org-1", observedState: "stopped" } },
		];
		hooks.listBrowserApprovals.mockReset().mockResolvedValue({ pending: [cloudRequest({ expiresAt: future })] });
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const reader = renderHook(() => useCloudPendingApprovals(), { wrapper });
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(reader.result.current.map((request) => request.id)).toEqual(["a-1"]));
		expect(hooks.listBrowserApprovals.mock.calls).toEqual([["org-1", "s-1"]]);
		shell.unmount();
		// Nothing reads it any more: its last list is not shown, and the reader
		// took its lists with it.
		await waitFor(() => expect(reader.result.current).toEqual([]));
		expect(client.getQueryCache().findAll({ queryKey: ["approvals", "pending", "cloud"] })).toHaveLength(0);
		client.clear();
	});

	it("forgets one account's requests and heard sessions when it signs out", async () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		hooks.sessions = [];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({
			pending: sessionId === "s-acct" ? [cloudRequest({ id: "first-account", sessionId: "s-acct", expiresAt: future })] : [],
		}));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const reader = renderHook(() => useCloudPendingApprovals(), { wrapper });
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		try {
			await waitFor(() => expect(hooks.tap).toBeDefined());
			// The first account's session announces a request on an open stream.
			act(() => hooks.tap?.({ baseUrl: "https://cp.test", orgId: "org-1", sessionId: "s-acct" }, { type: "browser.approval_requested" }));
			await waitFor(() => expect(reader.result.current.map((request) => request.id)).toEqual(["first-account"]));

			// Sign out, then another account signs in whose reads fail.
			hooks.ready = false;
			shell.rerender();
			await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ["approvals", "pending", "cloud"] })).toHaveLength(0));
			hooks.listBrowserApprovals.mockReset().mockRejectedValue(new CloudCpError("down", { status: 503 }));
			const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
			hooks.ready = true;
			hooks.userId = "someone-else";
			shell.rerender();
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
			// The earlier account's session is not read again, and its request is not shown.
			expect(hooks.listBrowserApprovals.mock.calls.map(([, id]) => id)).not.toContain("s-acct");
			expect(reader.result.current).toEqual([]);
			warn.mockRestore();
		} finally {
			hooks.ready = true;
			hooks.userId = "me";
			shell.unmount();
			client.clear();
		}
	});

	it("finds a cloud session's request while the local daemon is down", async () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		hooks.localDown = true;
		hooks.sessions = [];
		hooks.cloudSessions = [{ id: "s-cold", desiredState: "running", observedState: "running", createdByUserId: "me" }];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({
			pending: sessionId === "s-cold" ? [cloudRequest({ id: "cold-start", sessionId: "s-cold", expiresAt: future })] : [],
		}));
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		try {
			await waitFor(() => expect(shell.result.current.map((request) => request.id)).toEqual(["cold-start"]));
			expect(hooks.listBrowserApprovals).toHaveBeenCalledWith("org-1", "s-cold");
		} finally {
			hooks.localDown = false;
			hooks.cloudSessions = [];
			shell.unmount();
			client.clear();
		}
	});

	it("reads org-wide only the sessions this person may decide", async () => {
		const running = { orgId: "org-1", desiredState: "running", observedState: "running" };
		hooks.sessions = [
			{ id: "s-mine", cloud: { ...running, createdByUserId: "me" } },
			{ id: "s-for-me", cloud: { ...running, createdByUserId: "bot", requestedBy: { userId: "me" } } },
			{ id: "s-teammate", cloud: { ...running, createdByUserId: "priya" } },
			{ id: "s-legacy", cloud: running },
		];
		hooks.listBrowserApprovals.mockReset().mockResolvedValue({ pending: [] });
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(hooks.listBrowserApprovals.mock.calls.length).toBeGreaterThanOrEqual(3));
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
		const read = new Set(hooks.listBrowserApprovals.mock.calls.map(([, id]) => id));
		const listed = ["s-mine", "s-for-me", "s-teammate", "s-legacy"];
		expect(listed.filter((id) => read.has(id)).sort()).toEqual(["s-for-me", "s-legacy", "s-mine"]);
		shell.unmount();
		client.clear();
	});

	it("drops a session's request once the live reader stops reading that session", async () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		hooks.sessions = [{ id: "s-stop", cloud: { orgId: "org-1", desiredState: "running", observedState: "running" } }];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({
			pending: sessionId === "s-stop" ? [cloudRequest({ id: "stopping", sessionId: "s-stop", expiresAt: future })] : [],
		}));
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const reader = renderHook(() => useCloudPendingApprovals(), { wrapper });
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(reader.result.current.map((request) => request.id)).toEqual(["stopping"]));
		// Stopped from the sidebar: it leaves the running sessions the reader reads.
		hooks.sessions = [{ id: "s-stop", cloud: { orgId: "org-1", desiredState: "stopped", observedState: "stopped" } }];
		shell.rerender();
		await waitFor(() => expect(reader.result.current).toEqual([]));
		shell.unmount();
		client.clear();
	});

	describe("sessions the lists have not caught up with", () => {
		const future = () => new Date(Date.now() + 60_000).toISOString();
		function mount() {
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
			const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
			return { client, wrapper, shell };
		}
		const readSessions = () => hooks.listBrowserApprovals.mock.calls.map(([, sessionId]) => sessionId);

		it("reads the session on screen even when no list has it", async () => {
			hooks.sessions = [];
			hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({
				pending: [cloudRequest({ id: `on-${sessionId}`, sessionId, expiresAt: future() })],
			}));
			const { client, wrapper, shell } = mount();
			const dock = renderHook(() => useWatchCloudApprovals("org-1", "s-screen"), { wrapper });
			await waitFor(() => expect(shell.result.current.map((request) => request.id)).toEqual(["on-s-screen"]));
			dock.unmount();
			shell.unmount();
			client.clear();
		});

		it("reads a session that is starting toward running, not only one already running", async () => {
			hooks.sessions = [{ id: "s-starting", cloud: { orgId: "org-1", desiredState: "running", observedState: "bootstrapping" } }];
			hooks.listBrowserApprovals.mockReset().mockResolvedValue({ pending: [] });
			const { client, shell } = mount();
			await waitFor(() => expect(readSessions()).toContain("s-starting"));
			shell.unmount();
			client.clear();
		});

		it("reads a session that announced an approval on an open stream", async () => {
			hooks.sessions = [];
			hooks.listBrowserApprovals.mockReset().mockResolvedValue({ pending: [] });
			const { client, shell } = mount();
			await waitFor(() => expect(hooks.tap).toBeDefined());
			act(() => hooks.tap?.({ baseUrl: "https://cp.test", orgId: "org-1", sessionId: "s-heard" }, { type: "browser.approval_requested" }));
			await waitFor(() => expect(readSessions()).toContain("s-heard"));
			shell.unmount();
			client.clear();
		});

		it("logs a failed read instead of passing it off as nothing pending", async () => {
			hooks.sessions = [{ id: "s-failing", cloud: { orgId: "org-1", observedState: "running" } }];
			hooks.listBrowserApprovals.mockReset().mockRejectedValue(new CloudCpError("boom", { status: 500 }));
			const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
			const { client, shell } = mount();
			await waitFor(() => expect(warn).toHaveBeenCalledWith(expect.stringContaining("s-failing"), expect.any(CloudCpError)));
			warn.mockRestore();
			shell.unmount();
			client.clear();
		});
	});

	it("keeps reading while the window is hidden, so a request made then still shows", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		hooks.sessions = [{ id: "s-hidden", cloud: { orgId: "org-1", desiredState: "running", observedState: "running" } }];
		let pending: CloudCpBrowserApproval[] = [];
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => ({ pending: sessionId === "s-hidden" ? pending : [] }));
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
		focusManager.setFocused(false);
		try {
			pending = [cloudRequest({ id: "while-hidden", sessionId: "s-hidden", expiresAt: new Date(Date.now() + 300_000).toISOString() })];
			await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
			expect(shell.result.current.map((request) => request.id)).toEqual(["while-hidden"]);
		} finally {
			focusManager.setFocused(undefined);
			shell.unmount();
			client.clear();
			vi.useRealTimers();
		}
	});

	it("shows a request in a window hidden from startup, even when the first read fails", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
		// The app's own client and defaults: only the approval read's own options keep it going.
		const { queryClient: appClient } = await import("./query-client");
		appClient.mount();
		hooks.sessions = [{ id: "s-startup", cloud: { orgId: "org-1", desiredState: "running", observedState: "running" } }];
		let reads = 0;
		hooks.listBrowserApprovals.mockReset().mockImplementation(async (_org: string, sessionId: string) => {
			if (sessionId !== "s-startup") return { pending: [] };
			reads += 1;
			if (reads === 1) throw new CloudCpError("not yet", { status: 503 });
			return { pending: [cloudRequest({ id: "at-startup", sessionId: "s-startup", expiresAt: new Date(Date.now() + 300_000).toISOString() })] };
		});
		const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={appClient}>{children}</QueryClientProvider>;
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		try {
			await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
			expect(shell.result.current.map((request) => request.id)).toContain("at-startup");
		} finally {
			shell.unmount();
			appClient.clear();
			appClient.unmount();
			warn.mockRestore();
			visibility.mockRestore();
			vi.useRealTimers();
		}
	});
});
