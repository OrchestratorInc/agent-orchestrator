import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	userId: "alice",
	status: "authenticated",
	get: vi.fn(),
	put: vi.fn(),
}));

vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: state.status, session: state.status === "authenticated" ? { user: { id: state.userId } } : null }),
}));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { getUserPreferences: state.get, putUserPreferences: state.put }, ready: state.status === "authenticated", baseUrl: "https://cloud.test" }),
}));
vi.mock("./useCloudSandboxProviders", () => ({
	useCloudSandboxProviders: () => ({ available: ["nodeops", "coder"], isSuccess: true }),
}));

import { cloudProviderPreferenceQueryKey, useCloudProviderPreference } from "./useCloudProviderPreference";

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((resolvePromise, rejectPromise) => {
		resolve = resolvePromise;
		reject = rejectPromise;
	});
	return { promise, resolve, reject };
}

describe("useCloudProviderPreference", () => {
	beforeEach(() => {
		window.localStorage.clear();
		state.userId = "alice";
		state.status = "authenticated";
		state.get.mockReset().mockImplementation(async () => ({ sandboxProvider: state.userId === "alice" ? "coder" : "nodeops" }));
		state.put.mockReset();
	});

	it("shares a failed legacy migration with the settings hook", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "coder");
		state.get.mockResolvedValue({ sandboxProvider: null });
		state.put.mockRejectedValue(new Error("provider sync failed"));
		const { result } = renderHook(() => ({
			gate: useCloudProviderPreference({ migrateLegacy: true }),
			settings: useCloudProviderPreference(),
		}), { wrapper });
		await waitFor(() => expect(result.current.settings.error).toBe("provider sync failed"));
		expect(result.current.settings.provider).toBeNull();
		expect(window.localStorage.getItem("ao.cloud.sandboxProvider")).toBe("coder");
	});

	it("keeps the newer manual selection when legacy initialization completes later", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "coder");
		state.get.mockResolvedValue({ sandboxProvider: null });
		const migration = deferred<{ sandboxProvider: string }>();
		state.put.mockImplementation((input: { initializeOnly?: boolean }) => input.initializeOnly
			? migration.promise
			: Promise.resolve({ sandboxProvider: "nodeops" }));
		const { result } = renderHook(() => ({
			gate: useCloudProviderPreference({ migrateLegacy: true }),
			settings: useCloudProviderPreference(),
		}), { wrapper });
		await waitFor(() => expect(state.put).toHaveBeenCalledWith({ sandboxProvider: "coder", initializeOnly: true }));
		await act(async () => { await result.current.settings.setProvider("nodeops"); });
		expect(result.current.settings.provider).toBe("nodeops");
		await act(async () => { migration.resolve({ sandboxProvider: "coder" }); await migration.promise; });
		expect(result.current.settings.provider).toBe("nodeops");
	});

	it("does not reload a conflict using a different signed-in account", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "coder");
		state.get.mockImplementation(async () => ({ sandboxProvider: state.userId === "alice" ? null : "nodeops" }));
		const migration = deferred<{ sandboxProvider: string }>();
		state.put.mockReturnValue(migration.promise);
		const { result, rerender } = renderHook(() => useCloudProviderPreference({ migrateLegacy: true }), { wrapper });
		await waitFor(() => expect(state.put).toHaveBeenCalledOnce());
		state.userId = "bob";
		rerender();
		await waitFor(() => expect(result.current.provider).toBe("nodeops"));
		const readsBeforeConflict = state.get.mock.calls.length;
		await act(async () => { migration.reject({ code: "preference_conflict" }); try { await migration.promise; } catch { /* expected */ } });
		expect(state.get).toHaveBeenCalledTimes(readsBeforeConflict);
		expect(result.current.provider).toBe("nodeops");
	});

	it("does not restore a signed-out account's cache after a pending save", async () => {
		const pendingSave = deferred<{ sandboxProvider: string }>();
		state.put.mockReturnValue(pendingSave.promise);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const ownWrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		const { result, rerender } = renderHook(() => useCloudProviderPreference(), { wrapper: ownWrapper });
		await waitFor(() => expect(result.current.provider).toBe("coder"));
		let save!: Promise<void>;
		act(() => { save = result.current.setProvider("nodeops"); });
		await waitFor(() => expect(state.put).toHaveBeenCalledOnce());
		state.status = "unauthenticated";
		rerender();
		queryClient.removeQueries({ queryKey: ["cloud-provider-preference"] });
		await act(async () => { pendingSave.resolve({ sandboxProvider: "nodeops" }); await save; });
		expect(queryClient.getQueryData(cloudProviderPreferenceQueryKey("https://cloud.test", "alice"))).toBeUndefined();
		expect(result.current.provider).toBeNull();
	});

	it("keeps the authoritative value and reports a failed save", async () => {
		state.put.mockRejectedValue(new Error("save failed"));
		const { result } = renderHook(() => useCloudProviderPreference(), { wrapper });
		await waitFor(() => expect(result.current.provider).toBe("coder"));
		await act(async () => { await result.current.setProvider("nodeops"); });
		expect(result.current.provider).toBe("coder");
		expect(result.current.error).toBe("save failed");
	});

	it("does not show the previous account's choice after switching or signing out", async () => {
		const { result, rerender } = renderHook(() => useCloudProviderPreference(), { wrapper });
		await waitFor(() => expect(result.current.provider).toBe("coder"));
		state.userId = "bob";
		rerender();
		await waitFor(() => expect(result.current.provider).toBe("nodeops"));
		state.status = "unauthenticated";
		rerender();
		expect(result.current.provider).toBeNull();
	});
});
