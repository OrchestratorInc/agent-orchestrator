import { describe, expect, it, vi } from "vitest";
import type { CloudCpBrowserApproval, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";

const hooks = vi.hoisted(() => ({
	listBrowserApprovals: vi.fn(),
	sessions: [] as Array<{ id: string; cloud?: { orgId: string; observedState?: string; desiredState?: string } }>,
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
	useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test/", client: { listBrowserApprovals: hooks.listBrowserApprovals } }),
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: [{ id: "p", sessions: hooks.sessions }] }) }));

import { QueryClient, QueryClientProvider, focusManager } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { act, type ReactNode } from "react";
import {
	decideCloudApproval,
	fetchCloudPendingApprovals,
	nextExpiryIn,
	toPendingApproval,
	unexpired,
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

	it("names every cloud category with the local prompt's words", () => {
		const names = ["pay", "send", "post", "delete", "account", "script", "unknown"].map(
			(category) => toPendingApproval(cloudRequest({ category: category as CloudCpBrowserApproval["category"] }), TARGET).category,
		);
		expect(names).toEqual(["payment", "message", "publish", "delete", "account", "script", "script"]);
	});

	it("reads a session's pending list", async () => {
		const listBrowserApprovals = vi.fn(async () => ({ pending: [cloudRequest()] }));
		const result = await fetchCloudPendingApprovals({ listBrowserApprovals } as unknown as CloudCpClient, TARGET);
		expect(listBrowserApprovals).toHaveBeenCalledWith("org-1", "s-1");
		expect(result.map((request) => request.id)).toEqual(["a-1"]);
	});

	it("sends the decision to the session's decide route", async () => {
		const decideBrowserApproval = vi.fn(async () => ({ id: "a-1", decision: "allow-site" as const }));
		await decideCloudApproval({ decideBrowserApproval } as unknown as CloudCpClient, TARGET, "a-1", "allow-site");
		expect(decideBrowserApproval).toHaveBeenCalledWith("org-1", "s-1", "a-1", { decision: "allow-site" });
	});

	it("treats a request that already ended as answered, and reports other failures", async () => {
		const failWith = (status: number) =>
			({ decideBrowserApproval: vi.fn(async () => { throw new CloudCpError("no", { status }); }) }) as unknown as CloudCpClient;
		await expect(decideCloudApproval(failWith(409), TARGET, "a-1", "deny")).resolves.toBeUndefined();
		await expect(decideCloudApproval(failWith(404), TARGET, "a-1", "deny")).resolves.toBeUndefined();
		await expect(decideCloudApproval(failWith(403), TARGET, "a-1", "deny")).rejects.toThrow("no");
	});

	it("drops a request once it expires, and says when the next one will", () => {
		const soon = toPendingApproval(cloudRequest({ id: "soon", expiresAt: "2026-11-17T10:00:10Z" }), TARGET);
		const later = toPendingApproval(cloudRequest({ id: "later", expiresAt: "2026-11-17T10:01:00Z" }), TARGET);
		const now = Date.parse("2026-11-17T10:00:05Z");
		expect(nextExpiryIn([soon, later], now)).toBe(5_000);
		expect(unexpired([soon, later], now).map((request) => request.id)).toEqual(["soon", "later"]);
		expect(unexpired([soon, later], now + 5_000).map((request) => request.id)).toEqual(["later"]);
		expect(nextExpiryIn([soon], now + 5_000)).toBeUndefined();
	});

	it("reads only running cloud sessions, and every other reader sees the result without fetching", async () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		hooks.sessions = [
			{ id: "local-1" },
			{ id: "s-1", cloud: { orgId: "org-1", observedState: "running" } },
			{ id: "s-2", cloud: { orgId: "org-1", observedState: "stopped" } },
		];
		hooks.listBrowserApprovals.mockResolvedValue({ pending: [cloudRequest({ expiresAt: future })] });
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const reader = renderHook(() => useCloudPendingApprovals(), { wrapper });
		const shell = renderHook(() => useLiveCloudPendingApprovals(), { wrapper });
		await waitFor(() => expect(reader.result.current.map((request) => request.id)).toEqual(["a-1"]));
		expect(hooks.listBrowserApprovals.mock.calls).toEqual([["org-1", "s-1"]]);
		shell.unmount();
		// The reader follows the shared cache, not the shell's lifetime.
		act(() => client.removeQueries({ queryKey: ["approvals", "pending", "cloud"] }));
		await waitFor(() => expect(reader.result.current).toEqual([]));
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
});
