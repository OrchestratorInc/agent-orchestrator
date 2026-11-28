import { act, create } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useAfterPush } from "./useAfterPush";

const seen: boolean[] = [];
function Harness() {
	seen.push(useAfterPush());
	return null;
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	seen.length = 0;
});
afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

it("leaves the body out of the first render, which holds the push back, and draws it on the next frame", async () => {
	const frames: FrameRequestCallback[] = [];
	vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => frames.push(callback));
	vi.stubGlobal("cancelAnimationFrame", () => {});
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<Harness />); });
	expect(seen).toEqual([false]);
	await act(async () => { frames.shift()?.(0); });
	expect(seen.at(-1)).toBe(true);
	await act(async () => renderer.unmount());
});

it("falls back to a timer where there is no animation frame", async () => {
	vi.useFakeTimers();
	vi.stubGlobal("requestAnimationFrame", undefined);
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<Harness />); });
	expect(seen).toEqual([false]);
	await act(async () => { await vi.advanceTimersByTimeAsync(1); });
	expect(seen.at(-1)).toBe(true);
	await act(async () => renderer.unmount());
});
