import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
	promptSuggestionBusy,
	promptSuggestionStale,
	promptSuggestionTooLong,
	usePromptOptimization,
} from "./useConversationPromptSuggestion";

function deferred() {
	const { promise, resolve, reject } = Promise.withResolvers<string>();
	return { promise, resolve, reject };
}

describe("usePromptOptimization", () => {
	it("posts the draft and returns the polished text", async () => {
		const request = vi.fn(async () => "  Fix the login bug.  ");
		const { result } = renderHook(() => usePromptOptimization({ enabled: true, request }));
		await act(async () => {
			await expect(result.current.optimize("session-1", "turn-1", "fix login bug")).resolves.toBe("Fix the login bug.");
		});
		expect(request).toHaveBeenCalledWith({
			sessionId: "session-1", turnId: "turn-1", draft: "fix login bug", signal: expect.any(AbortSignal),
		});
		expect(result.current.pending).toBe(false);
		expect(result.current.failure).toBeUndefined();
	});

	it("spends one provider call when a second ask arrives in flight", async () => {
		const gate = deferred();
		const request = vi.fn(() => gate.promise);
		const { result } = renderHook(() => usePromptOptimization({ enabled: true, request }));
		const first = result.current.optimize("session-1", "turn-1", "fix login bug");
		// The second ask returns immediately and is not sent.
		await expect(result.current.optimize("session-1", "turn-1", "fix login bug")).resolves.toBeUndefined();
		expect(request).toHaveBeenCalledOnce();
		gate.resolve("Fix the login bug.");
		await expect(first).resolves.toBe("Fix the login bug.");
		await waitFor(() => expect(result.current.pending).toBe(false));
	});

	it.each([
		["busy", promptSuggestionBusy],
		["stale", promptSuggestionStale],
		["too-long", promptSuggestionTooLong],
		["unavailable", new Error("disconnected")],
	] as const)("reports %s without a result", async (failure, error) => {
		const request = vi.fn(async () => { throw error; });
		const { result } = renderHook(() => usePromptOptimization({ enabled: true, request }));
		await act(async () => {
			await expect(result.current.optimize("session-1", "turn-1", "fix login bug")).resolves.toBeUndefined();
		});
		expect(result.current.failure).toBe(failure);
		expect(result.current.pending).toBe(false);
	});

	it("treats an empty answer as no result rather than an empty draft", async () => {
		const request = vi.fn(async () => "   ");
		const { result } = renderHook(() => usePromptOptimization({ enabled: true, request }));
		await act(async () => {
			await expect(result.current.optimize("session-1", "turn-1", "fix login bug")).resolves.toBeUndefined();
		});
		expect(result.current.failure).toBeUndefined();
	});

	it("does nothing while disabled", async () => {
		const request = vi.fn(async () => "Fix the login bug.");
		const { result } = renderHook(() => usePromptOptimization({ enabled: false, request }));
		await act(async () => {
			await expect(result.current.optimize("session-1", "turn-1", "fix login bug")).resolves.toBeUndefined();
		});
		expect(request).not.toHaveBeenCalled();
	});
});
