import { afterEach, describe, expect, it, vi } from "vitest";
import { createGitHubConnectionAttempt } from "./githubConnection";
afterEach(() => vi.useRealTimers());
describe("GitHub connection attempt", () => {
	it("joins foreground refresh and drops late completion after Stop waiting", async () => {
		vi.useFakeTimers();
		let finish!: () => void;
		const refresh = vi.fn((_signal: AbortSignal) => new Promise<void>((resolve) => { finish = resolve; }));
		const onError = vi.fn();
		const attempt = createGitHubConnectionAttempt({ refresh, onError, isCurrent: () => true });
		attempt.start();
		const foreground = attempt.refresh();
		expect(refresh).toHaveBeenCalledOnce();
		const signal = refresh.mock.calls[0]![0] as AbortSignal;
		attempt.stop(); finish(); await foreground;
		await vi.advanceTimersByTimeAsync(10_000);
		expect(signal.aborted).toBe(true);
		expect(refresh).toHaveBeenCalledOnce();
		expect(onError).not.toHaveBeenCalled();
		expect(vi.getTimerCount()).toBe(0);
	});
	it("enforces the total deadline even when an HTTP request never finishes", async () => {
		vi.useFakeTimers();
		const refresh = vi.fn(async (_signal: AbortSignal) => new Promise<void>(() => {}));
		const onError = vi.fn();
		const attempt = createGitHubConnectionAttempt({ refresh, onError, isCurrent: () => true });
		attempt.start();
		await vi.advanceTimersByTimeAsync(5 * 60_000);
		expect(refresh.mock.calls[0]![0].aborted).toBe(true);
		expect(onError).toHaveBeenCalledOnce();
	});
	it("does not refresh or report errors for a replaced account", async () => {
		const refresh = vi.fn(async () => {});
		const onError = vi.fn();
		const attempt = createGitHubConnectionAttempt({ refresh, onError, isCurrent: () => false });
		attempt.start(); await attempt.refresh(); attempt.stop();
		expect(refresh).not.toHaveBeenCalled();
		expect(onError).not.toHaveBeenCalled();
	});
});
