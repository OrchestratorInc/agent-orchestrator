import { describe, expect, it, vi } from "vitest";

vi.mock("./hosts", () => ({ updateHostToken: vi.fn() }));
vi.mock("./deviceToken", () => ({ pairDevice: vi.fn(), releaseDeviceToken: vi.fn(async () => {}) }));

import type { ServerConfig } from "./config";
import { pairDevice, releaseDeviceToken } from "./deviceToken";
import { pairThisDevice, releaseThisDevice } from "./deviceTokenRuntime";

const config: ServerConfig = { host: "192.168.1.42", httpPort: "3011", muxPort: "14801", secure: false, password: "Code1234", hostId: "h_paired" };
const token = "aod_" + "a".repeat(64);

describe("pairThisDevice", () => {
	it("gives a retry with the same spent code the token it issued, until that token is handed back", async () => {
		vi.mocked(pairDevice).mockResolvedValue({ kind: "paired", token, credential: "pairing-code" });
		const first = await pairThisDevice(config, { fresh: true });
		await expect(pairThisDevice(config, { fresh: true })).resolves.toEqual(first);
		expect(pairDevice).toHaveBeenCalledOnce();
		await releaseThisDevice({ ...config, password: token });
		expect(releaseDeviceToken).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
		await pairThisDevice(config, { fresh: true });
		expect(pairDevice).toHaveBeenCalledTimes(2);
	});

	it("does not give a machine without an id the token another address was issued", async () => {
		vi.mocked(pairDevice).mockReset().mockResolvedValue({ kind: "paired", token, credential: "pairing-code" });
		const first = { ...config, hostId: undefined, password: "Code5678" };
		await pairThisDevice(first);
		await pairThisDevice({ ...first, host: "192.168.1.99" });
		await pairThisDevice({ ...first, host: "192.168.1.99", hostId: "http://192.168.1.42:3011" });
		expect(pairDevice).toHaveBeenCalledTimes(3);
	});

	it("pairs again for a password, which a retry can still use", async () => {
		vi.mocked(pairDevice).mockReset().mockResolvedValue({ kind: "paired", token, credential: "password" });
		const password = { ...config, password: "pairing1" };
		await pairThisDevice(password);
		await pairThisDevice(password);
		expect(pairDevice).toHaveBeenCalledTimes(2);
	});
});
