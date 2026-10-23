import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useCloudSessionsQuery } from "./useWorkspaceQuery";

const cloud = vi.hoisted(() => ({ listSessions: vi.fn(), listSessionArtifacts: vi.fn() }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: cloud, ready: true, baseUrl: "https://cp.test" }) }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" }, ready: true }) }));

const files = [{ path: "report.html", name: "report.html", kind: "html", contentType: "text/html", size: 3, sha256: "a".repeat(64), updatedAt: "2026-10-16T10:00:00Z" }];

function session(id: string, options: { count?: number; artifactsUpdatedAt?: string; updatedAt?: string; isTerminated?: boolean } = {}) {
	const updatedAt = options.updatedAt ?? "2026-10-16T10:00:00Z";
	return {
		id, orgId: "org-1", projectId: "p-1", updatedAt, createdAt: updatedAt,
		artifactCount: options.count, artifactsUpdatedAt: options.artifactsUpdatedAt, isTerminated: options.isTerminated ?? false,
	};
}

describe("useCloudSessionsQuery artifact files", () => {
	let queryClient: QueryClient;
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;

	beforeEach(() => {
		queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		cloud.listSessions.mockReset();
		cloud.listSessionArtifacts.mockReset().mockResolvedValue({ artifacts: files });
	});

	it("reads the files of a live session that has any, once per artifact version", async () => {
		cloud.listSessions.mockResolvedValue({ items: [
			session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z" }),
			session("s-2", { count: 0 }),
			session("s-3", { count: 2, isTerminated: true }),
		] });
		const { result } = renderHook(() => useCloudSessionsQuery(), { wrapper });
		await waitFor(() => expect(result.current.data?.[0]?.artifactFiles).toEqual(files));
		expect(result.current.data?.[1]?.artifactFiles).toBeUndefined();
		expect(result.current.data?.[2]?.artifactFiles).toBeUndefined();
		expect(cloud.listSessionArtifacts).toHaveBeenCalledTimes(1);

		// Activity changes updatedAt but not the files: no new read.
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z", updatedAt: "2026-10-16T10:09:00Z" })] });
		await result.current.refetch();
		await waitFor(() => expect(result.current.data?.[0]?.updatedAt).toBe("2026-10-16T10:09:00Z"));
		expect(cloud.listSessionArtifacts).toHaveBeenCalledTimes(1);

		// A commit moves artifactsUpdatedAt: read again.
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:10:00Z" })] });
		await result.current.refetch();
		await waitFor(() => expect(cloud.listSessionArtifacts).toHaveBeenCalledTimes(2));
	});

	it("keeps the last good list when a new read fails", async () => {
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z" })] });
		const { result } = renderHook(() => useCloudSessionsQuery(), { wrapper });
		await waitFor(() => expect(result.current.data?.[0]?.artifactFiles).toEqual(files));

		cloud.listSessionArtifacts.mockRejectedValue(new Error("unavailable"));
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:10:00Z" })] });
		await result.current.refetch();
		await waitFor(() => expect(cloud.listSessionArtifacts).toHaveBeenCalledTimes(2));
		expect(result.current.data?.[0]?.artifactFiles).toEqual(files);
	});

	it("shows the session list before its file lists arrive", async () => {
		let release: (value: { artifacts: typeof files }) => void = () => {};
		cloud.listSessionArtifacts.mockReturnValue(new Promise((resolve) => { release = resolve; }));
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z" })] });
		const { result } = renderHook(() => useCloudSessionsQuery(), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(1));
		expect(result.current.data?.[0]?.artifactFiles).toBeUndefined();
		release({ artifacts: files });
		await waitFor(() => expect(result.current.data?.[0]?.artifactFiles).toEqual(files));
	});

	it("follows a change in how many sessions have files without a hook warning", async () => {
		const errors = vi.spyOn(console, "error").mockImplementation(() => {});
		cloud.listSessions.mockResolvedValue({ items: [session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z" })] });
		const { result } = renderHook(() => useCloudSessionsQuery(), { wrapper });
		await waitFor(() => expect(result.current.data?.[0]?.artifactFiles).toEqual(files));

		cloud.listSessions.mockResolvedValue({ items: [
			session("s-1", { count: 1, artifactsUpdatedAt: "2026-10-16T10:00:00Z" }),
			session("s-2", { count: 1, artifactsUpdatedAt: "2026-10-16T10:05:00Z" }),
		] });
		await result.current.refetch();
		await waitFor(() => expect(result.current.data?.[1]?.artifactFiles).toEqual(files));
		expect(errors.mock.calls.flat().join(" ")).not.toContain("changed size between renders");
		errors.mockRestore();
	});
});
