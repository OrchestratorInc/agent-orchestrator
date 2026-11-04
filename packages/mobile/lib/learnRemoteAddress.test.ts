import { expect, it, vi } from "vitest";
import type { Endpoint } from "./endpoints";
import { knowsRemoteAddress, learnRemoteAddress } from "./learnRemoteAddress";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: false };
const tunnel: Endpoint = { kind: "tunnel", host: "h-abc.useao.dev", port: 443, secure: true };

it("knows a remote address only from a tunnel or relay", () => {
	expect(knowsRemoteAddress([lan])).toBe(false);
	expect(knowsRemoteAddress([lan, tunnel])).toBe(true);
});

it("waits while the machine advertises only this network", async () => {
	const save = vi.fn(async () => {});
	expect(await learnRemoteAddress([lan], async () => [lan], save)).toBe(false);
	expect(save).not.toHaveBeenCalled();
});

// Scanned before the internet address was ready, the phone connected locally;
// once the machine advertises it, the phone saves it and keeps the LAN.
it("saves the internet address once the machine advertises it", async () => {
	const save = vi.fn(async () => {});
	expect(await learnRemoteAddress([lan], async () => [lan, tunnel], save)).toBe(true);
	expect(save).toHaveBeenCalledWith([lan, tunnel]);
});
