import { expect, it, vi } from "vitest";
import { Bridge } from "./background";
import type { ChromeApi, Port } from "./chrome-api";

it("answers broker liveness pings without starting a session command", () => {
	const bridge = new Bridge({} as ChromeApi);
	const postMessage = vi.fn();
	const runtime = bridge as unknown as { port: Port; onHostMessage(message: unknown): void };
	runtime.port = { postMessage } as unknown as Port;
	runtime.onHostMessage({ type: "ping" });
	expect(postMessage).toHaveBeenCalledExactlyOnceWith({ type: "pong" });
});

it("aborts only the revoked session's admitted commands before removing approval", async () => {
	const bridge = new Bridge({} as ChromeApi);
	const canceled = new AbortController();
	const other = new AbortController();
	const runtime = bridge as unknown as {
		inFlight: Map<string, { controller: AbortController; sessionId: string }>;
		handleUi(message: unknown): Promise<unknown>;
	};
	runtime.inFlight.set("one", { controller: canceled, sessionId: "s1" });
	runtime.inFlight.set("two", { controller: other, sessionId: "s2" });
	const revoke = vi.spyOn(bridge.consent, "revoke").mockImplementation(async () => {
		expect(canceled.signal.aborted).toBe(true);
		expect(other.signal.aborted).toBe(false);
	});
	await expect(runtime.handleUi({ type: "ao-ui-revoke", sessionId: "s1" })).resolves.toEqual({ revoked: true });
	expect(revoke).toHaveBeenCalledWith("s1");
});
