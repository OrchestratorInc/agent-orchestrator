import { createElement, type ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { WorkspaceSummary } from "../types/workspace";
import { beforeEach, expect, it, vi } from "vitest";
const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: get }, hasTrustedApiBaseUrl: () => true }));
vi.mock("./useWorkspaceQuery", () => ({ toWorkspaceSession: (row: { id: string }, project: { id: string }) => ({ id: row.id, workspaceId: project.id }) }));
import { fetchArchivedSessions, useArchivedWorkspaces } from "./useArchivedSessions";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
beforeEach(() => get.mockReset());

it("follows every cursor within the selected project in server order", async () => {
	get.mockResolvedValueOnce({ data: { sessions: [{ id: "old-pinned" }], nextCursor: "next" } })
		.mockResolvedValueOnce({ data: { sessions: [{ id: "old-pinned" }, { id: "newer" }] } });
	expect(await fetchArchivedSessions("todo-app")).toEqual([{ id: "old-pinned" }, { id: "newer" }]);
	expect(get.mock.calls.map(([, options]) => options.params.query)).toEqual([
		{ active: false, limit: 100, project: "todo-app", cursor: undefined },
		{ active: false, limit: 100, project: "todo-app", cursor: "next" },
	]);
});

it("does not truncate standalone history behind another project's newest rows", async () => {
	get.mockResolvedValueOnce({ data: { sessions: [{ id: "project", projectId: "p" }], nextCursor: "next" } })
		.mockResolvedValueOnce({ data: { sessions: [{ id: "solo", projectId: "" }] } });
	expect(await fetchArchivedSessions(STANDALONE_WORKSPACE_ID)).toEqual([{ id: "solo", projectId: "" }]);
});

it("surfaces a later page failure rather than presenting an incomplete archive", async () => {
	get.mockResolvedValueOnce({ data: { sessions: [{ id: "first" }], nextCursor: "next" } })
		.mockResolvedValueOnce({ error: new Error("offline") });
	await expect(fetchArchivedSessions()).rejects.toThrow("offline");
});

it("rejects a repeated cursor", async () => {
	get.mockResolvedValue({ data: { sessions: [], nextCursor: "same" } });
	await expect(fetchArchivedSessions()).rejects.toThrow("did not advance");
});


it("does not read history until opened and lets a restored live row win", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const live = { id: "restored", isTerminated: false };
	const workspaces = [{ id: "p", name: "Project", path: "/p", archivedCount: 2, sessions: [live] }] as WorkspaceSummary[];
	get.mockResolvedValue({ data: { sessions: [{ id: "ended", projectId: "p" }, { id: "restored", projectId: "p", isTerminated: true }] } });
	const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
	const { result, rerender } = renderHook(({ open }) => useArchivedWorkspaces(workspaces, open, "p"), { initialProps: { open: false }, wrapper });
	expect(get).not.toHaveBeenCalled();
	expect(result.current.workspaces).toBe(workspaces);
	rerender({ open: true });
	await waitFor(() => expect(result.current.isSuccess).toBe(true));
	expect(result.current.workspaces[0].sessions.map((row) => row.id)).toEqual(["restored", "ended"]);
	expect(result.current.workspaces[0].sessions[0]).toBe(live);
	client.clear();
});
