import { describe, expect, it } from "vitest";
import type { ServerConfig } from "./config";
import { isPairingSecret, pairDevice, releaseDeviceToken } from "./deviceToken";

const config: ServerConfig = { host: "192.168.1.42", httpPort: "3011", muxPort: "14801", secure: false, password: "Code1234", hostId: "h_paired" };
const device = { name: "Test phone", platform: "ios" };
const token = "aod_" + "a".repeat(64);

function answer(status: number, body: unknown = {}) {
	const calls: [string, RequestInit | undefined][] = [];
	const fetchImpl = (async (url: string, init?: RequestInit) => {
		calls.push([url, init]);
		return new Response(JSON.stringify(body), { status });
	}) as unknown as typeof fetch;
	return Object.assign(fetchImpl, { calls });
}

const unreachable = (async () => { throw new TypeError("Network request failed"); }) as unknown as typeof fetch;

describe("isPairingSecret", () => {
	it("is a pairing password or code, not an account token or a device token", () => {
		expect(["pairing1", "Code1234", "a".repeat(64), token, ""].map(isPairingSecret)).toEqual([true, true, false, false, false]);
	});
});

describe("pairDevice", () => {
	it("presents the secret to the expected machine and returns this phone's token", async () => {
		const fetchImpl = answer(200, { hostId: "h_paired", deviceId: "dev_1", token, credential: "pairing-code" });
		await expect(pairDevice(config, device, fetchImpl)).resolves.toEqual({ kind: "paired", token, credential: "pairing-code" });
		const [url, init] = fetchImpl.calls[0];
		expect(url).toBe("http://192.168.1.42:3011/api/v1/remote-host/devices");
		expect(init?.method).toBe("POST");
		expect(init?.headers).toMatchObject({ Authorization: "Bearer Code1234", "X-AO-Expected-Host-ID": "h_paired" });
		expect(JSON.parse(String(init?.body))).toEqual(device);
	});

	it.each([
		[401, "unauthorized"],
		[404, "unsupported"], [405, "unsupported"], [403, "unsupported"], [409, "unsupported"],
		[429, "offline"], [500, "offline"],
	])("maps %i to %s", async (status, kind) => {
		await expect(pairDevice(config, device, answer(status))).resolves.toEqual({ kind });
	});

	it("refuses an answer from another machine, or one without a device token", async () => {
		await expect(pairDevice(config, device, answer(200, { hostId: "h_other", token }))).resolves.toEqual({ kind: "offline" });
		await expect(pairDevice(config, device, answer(200, { hostId: "h_paired", token: "b".repeat(64) }))).resolves.toEqual({ kind: "offline" });
		await expect(pairDevice(config, device, unreachable)).resolves.toEqual({ kind: "offline" });
	});
});

describe("releaseDeviceToken", () => {
	it("hands the token back through the machine's unpair route, and ignores a failure", async () => {
		const fetchImpl = answer(204);
		await releaseDeviceToken({ ...config, password: token }, fetchImpl);
		const [url, init] = fetchImpl.calls[0];
		expect(url).toBe("http://192.168.1.42:3011/api/v1/push/pairings/released");
		expect(init).toMatchObject({ method: "DELETE", headers: { Authorization: `Bearer ${token}`, "X-AO-Expected-Host-ID": "h_paired" } });
		await expect(releaseDeviceToken({ ...config, password: token }, unreachable)).resolves.toBeUndefined();
	});
});
