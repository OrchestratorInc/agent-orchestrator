import { afterEach, describe, expect, it, vi } from "vitest";

const secureFetch = vi.fn(async () => ({ ok: true }));
vi.mock("./http", () => ({ secureFetch }));
vi.mock("./socket", () => ({ SecureWebSocket: class {} }));
vi.mock("../config", () => ({ authHeaders: (cfg: { password: string }) => ({ Authorization: `Bearer ${cfg.password}` }) }));

const { hostFetch, bytesToDataUri } = await import("./host");
const { hostImageSource } = await import("../hostImageSource");

const plain = { host: "192.168.1.5", httpPort: "3011", muxPort: "", secure: false, password: "aod_x" };
const pinned = { ...plain, hostKey: "JUO5L_EJVRFHatyDadtt3JM2ZaEZeN2hQE7hBmypVZ0" };

afterEach(() => vi.unstubAllGlobals());

describe("hostFetch", () => {
	it("uses the encrypted channel only for a pairing with a key", async () => {
		const native = vi.fn(async () => ({ ok: true }));
		vi.stubGlobal("fetch", native);
		await hostFetch(plain, "/api/v1/projects", { headers: { Authorization: "Bearer aod_x" } });
		expect(native).toHaveBeenCalledWith("http://192.168.1.5:3011/api/v1/projects", expect.anything());
		await hostFetch(pinned, "/api/v1/projects", { method: "POST", headers: { Authorization: "Bearer aod_x" }, body: "{}" });
		expect(native).toHaveBeenCalledTimes(1);
		expect(secureFetch).toHaveBeenCalledWith(
			expect.objectContaining({ url: "ws://192.168.1.5:3011/api/v1/e2ee", authority: "192.168.1.5:3011" }),
			{ method: "POST", path: "/api/v1/projects", headers: { Authorization: "Bearer aod_x" }, body: "{}" },
			undefined,
		);
	});

	it("refuses a saved key it cannot read rather than sending in plaintext", () => {
		vi.stubGlobal("fetch", vi.fn());
		expect(() => hostFetch({ ...plain, hostKey: "bad" }, "/x")).toThrow("Pair it again");
		expect(fetch).not.toHaveBeenCalled();
	});
});

describe("hostImageSource", () => {
	it("gives the native image loader no credential for a pinned machine", () => {
		expect(hostImageSource(plain, "http://h/i.png").headers).toEqual({ Authorization: "Bearer aod_x" });
		expect(hostImageSource(pinned, "http://h/i.png").headers).toEqual({});
	});
});

describe("bytesToDataUri", () => {
	it("keeps only the charset of the content type", () => {
		expect(bytesToDataUri(Uint8Array.of(104, 105), "text/plain; charset=utf-8; x=y")).toBe("data:text/plain;charset=utf-8;base64,aGk=");
		expect(bytesToDataUri(Uint8Array.of(104, 105), "image/png")).toBe("data:image/png;base64,aGk=");
	});
});
