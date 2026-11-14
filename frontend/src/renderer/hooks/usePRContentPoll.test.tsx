import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PR_CONTENT_POLL_MS, usePRContentPoll } from "./usePRContentPoll";
import { sessionScmSummaryQueryKey } from "./useSessionScmSummary";

function setup(hostId: string | undefined, enabled: boolean) {
	const queryClient = new QueryClient();
	const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	const view = renderHook(() => usePRContentPoll("s1", hostId, enabled), { wrapper });
	return { invalidate, view };
}

describe("usePRContentPoll", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("re-reads the session's PRs on each tick while the window is visible, and stops on unmount", () => {
		const { invalidate, view } = setup(undefined, true);
		vi.advanceTimersByTime(PR_CONTENT_POLL_MS - 1);
		expect(invalidate).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: sessionScmSummaryQueryKey("s1", undefined) });

		const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
		vi.advanceTimersByTime(PR_CONTENT_POLL_MS);
		expect(invalidate).toHaveBeenCalledTimes(1);
		visibility.mockReturnValue("visible");
		vi.advanceTimersByTime(PR_CONTENT_POLL_MS);
		expect(invalidate).toHaveBeenCalledTimes(2);

		view.unmount();
		vi.advanceTimersByTime(PR_CONTENT_POLL_MS * 3);
		expect(invalidate).toHaveBeenCalledTimes(2);
	});

	it("does nothing when disabled, or for a remote host that already refetches", () => {
		const off = setup(undefined, false);
		const remote = setup("host-1", true);
		vi.advanceTimersByTime(PR_CONTENT_POLL_MS * 2);
		expect(off.invalidate).not.toHaveBeenCalled();
		expect(remote.invalidate).not.toHaveBeenCalled();
	});
});
