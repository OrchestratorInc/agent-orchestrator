import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "./useWorkspaceQuery";

const { patch, del, get } = vi.hoisted(() => ({ patch: vi.fn(), del: vi.fn(), get: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { PATCH: patch, DELETE: del, GET: get },
	apiErrorMessage: (error: { message: string }) => error.message,
}));
import { useRememberProjectPermissions } from "./useRememberProjectPermissions";

function setup(sourceHarness?: string) {
	const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const wrapper = ({ children }: { children: ReactNode }) =>
		<QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { ...renderHook(() => useRememberProjectPermissions("project-one", sourceHarness), { wrapper }), invalidate, client };
}

// The daemon's copy of the project: GET reads what the last successful write left.
let stored: string | undefined;
beforeEach(() => {
	stored = undefined;
	patch.mockReset();
	del.mockReset();
	get.mockReset();
	get.mockImplementation(async () => ({ data: { status: "ok", project: { config: stored ? { agentConfig: { permissions: stored } } : {} } } }));
});

describe("project permission persistence", () => {
	it("writes only the requested permission to the correct project and refreshes project data", async () => {
		patch.mockImplementation(async (_path, { body }) => { stored = body.permissions; return { data: { project: {} } }; });
		const { result, invalidate } = setup();
		expect(patch).not.toHaveBeenCalled();
		await act(async () => { await result.current.remember("bypass-permissions"); });
		expect(patch).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", {
			params: { path: { id: "project-one" } }, body: { permissions: "bypass-permissions" },
		});
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "project-one"] });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
		await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
	});

	it("reads the project's remembered mode and forgets it", async () => {
		stored = "auto";
		del.mockImplementation(async () => { stored = undefined; return { data: { project: {} } }; });
		const { result, invalidate } = setup();
		await waitFor(() => expect(result.current.savedMode).toBe("auto"));
		await act(async () => { await result.current.remember(null); });
		expect(del).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", { params: { path: { id: "project-one" } } });
		expect(patch).not.toHaveBeenCalled();
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "project-one"] });
		await waitFor(() => expect(result.current.savedMode).toBeUndefined());
	});

	it("reports errors without claiming success and allows an explicit retry", async () => {
		patch.mockResolvedValueOnce({ error: { message: "Project not found" } }).mockImplementationOnce(async () => { stored = "auto"; return {}; });
		const { result, invalidate } = setup();
		await act(async () => { await expect(result.current.remember("auto")).rejects.toEqual({ message: "Project not found" }); });
		await waitFor(() => expect(result.current.error).toBe("Project not found"));
		expect(result.current.savedMode).toBeUndefined();
		expect(invalidate).not.toHaveBeenCalled();
		await act(async () => { await result.current.remember("auto"); });
		await waitFor(() => expect(result.current.savedMode).toBe("auto"));
		expect(result.current.error).toBeUndefined();
	});
});


it("sends source harness context so the daemon can preserve Full access across providers", async () => {
	patch.mockResolvedValue({});
	const { result } = setup("codex");
	await act(async () => { await result.current.remember("default"); });
	expect(patch).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", {
		params: { path: { id: "project-one" } }, body: { permissions: "default", sourceHarness: "codex" },
	});
});

it("reads a Codex Full access write as the bypass mode the project stores", async () => {
	patch.mockImplementation(async () => { stored = "bypass-permissions"; return {}; });
	const { result } = setup("codex");
	await act(async () => { await result.current.remember("default"); });
	await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
});

it("shows a mode changed elsewhere once the project refetches", async () => {
	patch.mockImplementation(async (_path, { body }) => { stored = body.permissions; return {}; });
	const { result, client } = setup();
	await act(async () => { await result.current.remember("auto"); });
	await waitFor(() => expect(result.current.savedMode).toBe("auto"));
	stored = "bypass-permissions";
	await act(async () => { await client.invalidateQueries({ queryKey: ["project", "project-one"] }); });
	await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
});

it("does not read a project for a Scratchpad session", async () => {
	const client = new QueryClient();
	const wrapper = ({ children }: { children: ReactNode }) =>
		<QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result } = renderHook(() => useRememberProjectPermissions("__standalone__"), { wrapper });
	await act(async () => {});
	expect(get).not.toHaveBeenCalled();
	expect(result.current.savedMode).toBeUndefined();
});
