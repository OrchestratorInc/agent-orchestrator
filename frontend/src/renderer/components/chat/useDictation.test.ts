import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DictationMessage } from "../../../shared/dictation";
import { aoBridge } from "../../lib/bridge";
import { useDictation } from "./useDictation";

describe("useDictation", () => {
	afterEach(() => vi.restoreAllMocks());

	it("does not let a late superseded event for an earlier run end the next composer's dictation", () => {
		const listeners = new Set<(event: DictationMessage) => void>();
		vi.spyOn(aoBridge.dictation, "onEvent").mockImplementation((listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		});
		const start = vi.spyOn(aoBridge.dictation, "start").mockResolvedValue(undefined);
		const cancel = vi.spyOn(aoBridge.dictation, "cancel").mockResolvedValue(undefined);
		const target = () => ({ onText: vi.fn(), onEnd: vi.fn() });
		const first = renderHook(() => useDictation(target()));
		const next = renderHook(() => useDictation(target()));

		act(() => first.result.current.toggle());
		const firstRun = start.mock.calls[0]?.[0] as number;
		// Main told this renderer another surface took the mic, but the event is
		// still in flight when the user starts the next composer.
		act(() => next.result.current.toggle());
		const nextRun = start.mock.calls[1]?.[0] as number;
		expect(nextRun).not.toBe(firstRun);

		act(() => {
			for (const listener of [...listeners]) listener({ type: "superseded", run: firstRun });
		});
		expect(next.result.current.phase).toBe("starting");

		act(() => {
			for (const listener of [...listeners]) listener({ type: "listening", run: nextRun });
		});
		expect(next.result.current.phase).toBe("listening");
		// Still the owner, so leaving cancels the helper.
		next.unmount();
		expect(cancel).toHaveBeenCalled();
	});
});
