import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { usePreparedClone } from "./usePreparedClone";
import { apiClient } from "../lib/api-client";

function progressSnapshot(overrides: Record<string, unknown> = {}) {
	return {
		phase: "receiving",
		percent: 45,
		itemsDone: 1234,
		itemsTotal: 2748,
		transferredBytes: 2097152,
		speedBytesPerSecond: 1572864,
		startedAt: "2026-10-12T00:00:00Z",
		updatedAt: "2026-10-12T00:00:01Z",
		...overrides,
	};
}

describe("usePreparedClone progress polling", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	it("polls clone progress while prepare is in flight and stops when it settles", async () => {
		vi.useFakeTimers();
		let resolvePrepare: (value: { data: { path: string; remoteUrl: string; preparationId: string } }) => void = () => {};
		const preparePromise = new Promise<{ data: { path: string; remoteUrl: string; preparationId: string } }>((resolve) => {
			resolvePrepare = resolve;
		});
		vi.spyOn(apiClient, "POST").mockReturnValue(preparePromise as never);
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: progressSnapshot() } as never);

		const { result } = renderHook(() => usePreparedClone());
		let prepared: { preparationId: string } | undefined;
		act(() => {
			void result.current.prepare("https://example.com/repo.git", "/repos").then((value) => {
				prepared = value;
			});
		});

		// Before the first poll only the busy state is known.
		expect(result.current.progress).toBeNull();

		await act(async () => {
			await vi.advanceTimersByTimeAsync(500);
		});
		expect(get).toHaveBeenCalledWith("/api/v1/projects/clone/progress", {
			params: { query: { remoteUrl: "https://example.com/repo.git", destinationParent: "/repos" } },
		});
		expect(result.current.progress?.phase).toBe("receiving");

		await act(async () => {
			resolvePrepare({ data: { path: "/repos/repo", remoteUrl: "https://example.com/repo.git", preparationId: "prep-1" } });
			await vi.advanceTimersByTimeAsync(100);
		});
		expect(prepared).toMatchObject({ preparationId: "prep-1" });
		expect(result.current.progress).toBeNull();

		const callsAfterCompletion = get.mock.calls.length;
		await act(async () => {
			await vi.advanceTimersByTimeAsync(2000);
		});
		expect(get.mock.calls.length).toBe(callsAfterCompletion);
	});

	it("ignores progress misses so older daemons keep the plain busy state", async () => {
		vi.useFakeTimers();
		vi.spyOn(apiClient, "POST").mockResolvedValue({
			data: { path: "/repos/repo", remoteUrl: "https://example.com/repo.git", preparationId: "prep-1" },
		} as never);
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: undefined, error: { code: "CLONE_PROGRESS_NOT_FOUND" } } as never);

		const { result } = renderHook(() => usePreparedClone());
		await act(async () => {
			void result.current.prepare("https://example.com/repo.git", "/repos");
			await vi.advanceTimersByTimeAsync(1500);
		});

		expect(result.current.progress).toBeNull();
	});
});
