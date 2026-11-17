import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useChatMessageFocus, useChatNavigationRequest } from "./useChatMessageFocus";

describe("useChatMessageFocus", () => {
	beforeEach(() => useUiStore.setState({ chatMessageFocusRequest: null }));

	it("leaves a reviewer chat hit to that review's transcript", () => {
		useUiStore.getState().requestChatMessageFocus("w-fix", "m1", "flaky login", "review-1");
		const worker = vi.fn();
		const otherReview = vi.fn();
		const reviewer = vi.fn();
		renderHook(() => useChatMessageFocus("w-fix", undefined, true, worker));
		renderHook(() => useChatMessageFocus("w-fix", "review-2", true, otherReview));
		expect(worker).not.toHaveBeenCalled();
		expect(otherReview).not.toHaveBeenCalled();
		renderHook(() => useChatMessageFocus("w-fix", "review-1", true, reviewer));
		expect(reviewer).toHaveBeenCalledWith(expect.objectContaining({ messageId: "m1", text: "flaky login" }));
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});

	it("waits for the transcript to load, and ignores other sessions", () => {
		useUiStore.getState().requestChatMessageFocus("w-fix", "m1", "flaky login");
		const focus = vi.fn();
		renderHook(() => useChatMessageFocus("other", undefined, true, focus));
		const { rerender } = renderHook(({ ready }) => useChatMessageFocus("w-fix", undefined, ready, focus), { initialProps: { ready: false } });
		expect(focus).not.toHaveBeenCalled();
		rerender({ ready: true });
		expect(focus).toHaveBeenCalledTimes(1);
	});

	it("keeps a navigation request for the chat it was asked of, until that chat applied it", () => {
		const { result, rerender } = renderHook(({ owner }) => useChatNavigationRequest<{ id: string }>(owner), { initialProps: { owner: "b" } });
		act(() => result.current.navigate({ id: "palette:1" }));
		expect(result.current.request).toEqual({ id: "palette:1" });
		rerender({ owner: "a" });
		expect(result.current.request).toBeUndefined();
		rerender({ owner: "b" });
		act(() => result.current.handled("palette:0"));
		expect(result.current.request).toEqual({ id: "palette:1" });
		act(() => result.current.handled("palette:1"));
		expect(result.current.request).toBeUndefined();
	});
});
