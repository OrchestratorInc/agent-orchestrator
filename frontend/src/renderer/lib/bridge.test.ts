import { afterEach, beforeEach, expect, it, vi } from "vitest";

const { remove, get, base } = vi.hoisted(() => ({ remove: vi.fn(), get: vi.fn(), base: { value: "http://127.0.0.1:3001" } }));
vi.mock("./api-client", () => ({ apiClient: { DELETE: remove, GET: get }, getApiBaseUrl: () => base.value }));
let desktop: PropertyDescriptor | undefined;
beforeEach(() => {
	desktop = Object.getOwnPropertyDescriptor(window, "ao");
	Object.defineProperty(window, "ao", { configurable: true, value: undefined });
	vi.resetAllMocks(); vi.resetModules(); base.value = "http://127.0.0.1:3001";
	vi.stubEnv("VITE_NO_ELECTRON", "1");
});
afterEach(() => { if (desktop) Object.defineProperty(window, "ao", desktop); else Reflect.deleteProperty(window, "ao"); vi.useRealTimers(); vi.unstubAllEnvs(); vi.resetModules(); });

async function web() {
	const { aoBridge } = await import("./bridge");
	const bridge = aoBridge.sideChats;
	const close = async (requestId = "request") => {
		const state = await bridge.getCloseState("session");
		return bridge.requestClose({ sessionId: "session", sideId: "side", requestId, epoch: state.epoch, expectedVersion: state.sides.find(side => side.id === "side")?.version ?? 0 });
	};
	return { bridge, close };
}

it("uses explicit browser mode and retains 204 proof for a replacement view", async () => {
	const { bridge, close } = await web(); remove.mockResolvedValueOnce({ response: { status: 204 } });
	bridge.onCloseStateChanged(() => { throw new Error("view disposed"); });
	expect(bridge.mode).toBe("web"); expect(bridge.protocolVersion).toBe(1);
	expect((await close()).sides[0].status).toBe("closed");
	expect((await bridge.getCloseState("session")).sides[0].status).toBe("closed");
	await close("duplicate"); expect(remove).toHaveBeenCalledOnce();
});

it.each([404, 200, 202, 500])("keeps HTTP %s unconfirmed without desktop proof", async status => {
	const { close } = await web(); remove.mockResolvedValueOnce({ error: status >= 400 ? { code: "CHAT_SIDE_CLOSED" } : undefined, response: { status } });
	expect((await close()).sides[0].status).toBe("unconfirmed");
});

it("requires a fresh matching OPEN list before restoring interaction", async () => {
	const { close } = await web();
	remove.mockResolvedValue({ error: { code: "CHAT_SIDE_UNAVAILABLE" }, response: { status: 409 } });
	get.mockResolvedValueOnce({ data: { sides: [{ id: "side", sessionId: "other" }] } });
	expect((await close()).sides[0].status).toBe("unconfirmed");
	get.mockResolvedValueOnce({ data: { sides: [{ id: "side", sessionId: "session" }] } });
	expect((await close("retry")).sides[0].status).toBe("unconfirmed");
});

it("restores interaction for a directly rejected request with fresh open proof", async () => {
	const { close } = await web();
	remove.mockResolvedValueOnce({ error: { code: "CHAT_SIDE_UNAVAILABLE" }, response: { status: 409 } });
	get.mockResolvedValueOnce({ data: { sides: [{ id: "side", sessionId: "session" }] } });
	expect((await close()).sides[0].status).toBe("rejected_open");
});

it("bounds an unresponsive transport and still accepts a late valid 204", async () => {
	vi.useFakeTimers(); const { bridge, close } = await web();
	let resolve!: (value: unknown) => void; remove.mockReturnValueOnce(new Promise(yes => { resolve = yes; }));
	const closing = close(); await vi.advanceTimersByTimeAsync(30_001);
	expect((await closing).sides[0].status).toBe("unconfirmed");
	resolve({ response: { status: 204 } }); await vi.advanceTimersByTimeAsync(0);
	expect((await bridge.getCloseState("session")).sides[0].status).toBe("closed");
});

it("never substitutes browser DELETE after a desktop IPC failure", async () => {
	const requestClose = vi.fn().mockRejectedValue(new Error("IPC unavailable"));
	Object.defineProperty(window, "ao", { configurable: true, value: { sideChats: { protocolVersion: 1, mode: "desktop", requestClose } } });
	const { aoBridge } = await import("./bridge");
	await expect(aoBridge.sideChats.requestClose({ sessionId: "session", sideId: "side", requestId: "request", epoch: "desktop", expectedVersion: 0 })).rejects.toThrow("IPC unavailable");
	expect(remove).not.toHaveBeenCalled();
});

it("fails closed when Electron preload is missing outside explicit browser preview", async () => {
	vi.stubEnv("VITE_NO_ELECTRON", "0");
	const { bridge } = await web();
	expect(bridge.mode).toBe("desktop");
	await expect(bridge.getCloseState("session")).rejects.toThrow("Desktop side chat bridge is unavailable");
	await expect(bridge.requestClose({ sessionId: "session", sideId: "side", epoch: "missing", requestId: "one", expectedVersion: 0 })).rejects.toThrow("Desktop side chat bridge is unavailable");
	expect(remove).not.toHaveBeenCalled();
});
