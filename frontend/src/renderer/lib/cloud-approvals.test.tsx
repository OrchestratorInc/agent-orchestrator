import { describe, expect, it, vi } from "vitest";
import type { CloudCpBrowserApproval, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";

const hooks = vi.hoisted(() => ({
	listBrowserApprovals: vi.fn(),
	sessions: [] as Array<{ id: string; cloud?: { orgId: string; observedState?: string } }>,
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test/", client: { listBrowserApprovals: hooks.listBrowserApprovals } }),
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: [{ id: "p", sessions: hooks.sessions }] }) }));

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import {
	decideCloudApproval,
	fetchCloudPendingApprovals,
	nextExpiryIn,
	toPendingApproval,
	unexpired,
	useCloudPendingApprovals,
	useLiveCloudPendingApprovals,
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
		await waitFor(() => expect(reader.result.current).toEqual([]));
		client.clear();
	});
});
