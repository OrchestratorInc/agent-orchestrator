import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { disposeAgentLogin, isAgentLoginPending, pendingAgentLogins, resetAgentLoginLifecycle, runAgentLoginTask } from "./agent-login-lifecycle";

beforeEach(() => {
	vi.useFakeTimers();
	resetAgentLoginLifecycle();
});
afterEach(() => {
	resetAgentLoginLifecycle();
	vi.useRealTimers();
});

it("keeps a harness pending after a failed disposal and retries until it succeeds", async () => {
	const cancel = vi.fn().mockRejectedValueOnce(new Error("Host disconnected")).mockResolvedValue(undefined);
	await expect(disposeAgentLogin("codex", "remote-1", cancel)).rejects.toThrow("Host disconnected");
	expect(isAgentLoginPending("codex", "remote-1")).toBe(true);
	expect(pendingAgentLogins().size).toBe(1);
	expect(isAgentLoginPending("codex")).toBe(false);
	await vi.advanceTimersByTimeAsync(1_000);
	await vi.waitFor(() => expect(isAgentLoginPending("codex", "remote-1")).toBe(false));
	expect(cancel).toHaveBeenCalledTimes(2);
	expect(pendingAgentLogins().size).toBe(0);
});

it("settles a retained disposal before a later task and refuses that task while disposal fails", async () => {
	const cancel = vi.fn().mockRejectedValue(new Error("Host disconnected"));
	await expect(disposeAgentLogin("codex", "remote-1", cancel)).rejects.toThrow("Host disconnected");
	const start = vi.fn().mockResolvedValue("started");
	await expect(runAgentLoginTask("codex", "remote-1", start)).rejects.toThrow("Host disconnected");
	expect(start).not.toHaveBeenCalled();
	expect(isAgentLoginPending("codex", "remote-1")).toBe(true);
	cancel.mockResolvedValue(undefined);
	await expect(runAgentLoginTask("codex", "remote-1", start)).resolves.toBe("started");
	expect(cancel).toHaveBeenCalledTimes(3);
	expect(start).toHaveBeenCalledOnce();
	expect(isAgentLoginPending("codex", "remote-1")).toBe(false);
});
