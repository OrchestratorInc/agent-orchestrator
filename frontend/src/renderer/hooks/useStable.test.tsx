import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useLatestCallback, useStableSet } from "./useStable";

describe("useStableSet", () => {
	it("keeps the same Set while its members are unchanged", () => {
		const { result, rerender } = renderHook(({ set }) => useStableSet(set), {
			initialProps: { set: new Set(["a", "b"]) },
		});
		const first = result.current;
		rerender({ set: new Set(["b", "a"]) });
		expect(result.current).toBe(first);
	});

	it("returns the new Set once a member changes", () => {
		const { result, rerender } = renderHook(({ set }) => useStableSet(set), {
			initialProps: { set: new Set(["a"]) },
		});
		const next = new Set(["a", "b"]);
		rerender({ set: next });
		expect(result.current).toBe(next);
	});
});

describe("useLatestCallback", () => {
	it("keeps one identity and always calls the latest function", () => {
		const first = vi.fn(() => "first");
		const second = vi.fn(() => "second");
		const { result, rerender } = renderHook(({ fn }) => useLatestCallback(fn), {
			initialProps: { fn: first as () => string },
		});
		const callback = result.current;
		rerender({ fn: second });
		expect(result.current).toBe(callback);
		expect(callback()).toBe("second");
		expect(first).not.toHaveBeenCalled();
	});
});
