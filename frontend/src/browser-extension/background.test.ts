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
