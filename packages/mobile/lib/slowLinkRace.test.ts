import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./config", () => ({}));
vi.mock("./hosts", () => ({
	findHost: vi.fn(),
	updateHostEndpoints: vi.fn(),
	adoptHostIdentity: vi.fn(),
	touchHost: vi.fn(),
}));

import { endpointStillAnswers, forgetSlowLinks, PROBE_TIMEOUT_MS, probeEndpoint, raceWithSlowLinkRetry, SLOW_LINK_MEMORY_MS, SLOW_LINK_PROBE_TIMEOUT_MS } from "./connectRuntime";
import { recoveryDelayMs } from "./reRace";
import type { Endpoint } from "./endpoints";
import { ProbeTimeoutError, raceEndpoints } from "./race";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: false };
const tunnel: Endpoint = { kind: "tunnel", host: "abc.example.com", port: 443, secure: true };

/** fetch that answers the identity probe after `delayMs`, or never if the signal aborts first. */
function slowIdentity(delayMs: number, hostId = "h_paired") {
	return vi.fn((_url: string, init?: { signal?: AbortSignal }) => new Promise((resolve, reject) => {
		const timer = setTimeout(() => resolve({ ok: true, json: async () => ({ hostId, apiVersion: 1 }) }), delayMs);
		init?.signal?.addEventListener("abort", () => {
			clearTimeout(timer);
			reject(Object.assign(new Error("Aborted"), { name: "AbortError" }));
		});
	}));
}

afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
	forgetSlowLinks();
});

describe("a slow link", () => {
	it("reports a probe that ran out its own time as a timeout", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("fetch", slowIdentity(60_000));
		const probe = probeEndpoint(lan, new AbortController().signal, { retryTunnel: false });
		const caught = probe.catch((error: unknown) => error);
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS);
		expect(await caught).toBeInstanceOf(ProbeTimeoutError);
	});

	it("is marked when every endpoint timed out, and not when one refused", async () => {
		const timeout = (endpoint: Endpoint) => Promise.reject(new ProbeTimeoutError(endpoint));
		await expect(raceEndpoints([lan, tunnel], "h_paired", timeout)).resolves.toEqual({ ok: false, reason: "none-reachable", allTimedOut: true });
		const refusedLan = (endpoint: Endpoint) => endpoint === lan ? Promise.reject(new Error("refused")) : timeout(endpoint);
		await expect(raceEndpoints([lan, tunnel], "h_paired", refusedLan)).resolves.toEqual({ ok: false, reason: "none-reachable" });
	});

	it("connects on the second race when answers take longer than the quick timeout", async () => {
		vi.useFakeTimers();
		// A 4 s round trip: past the quick race, well inside an ordinary request.
		vi.stubGlobal("fetch", slowIdentity(4_000));
		const outcome = raceWithSlowLinkRetry([lan], "h_paired");
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + 4_000 + 300);
		await expect(outcome).resolves.toMatchObject({ ok: true, endpoint: lan, hostId: "h_paired" });
	});

	it("still gives up once even the slow race runs out", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("fetch", slowIdentity(60_000));
		const outcome = raceWithSlowLinkRetry([lan], "h_paired");
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + SLOW_LINK_PROBE_TIMEOUT_MS + 100);
		await expect(outcome).resolves.toMatchObject({ ok: false, reason: "none-reachable" });
	});

	it("does not race again when the machine refused or someone else answered", async () => {
		const probe = vi.fn((endpoint: Endpoint) => endpoint === lan
			? Promise.reject(new Error("refused"))
			: Promise.resolve({ hostId: "someone-else" }));
		await expect(raceWithSlowLinkRetry([lan, tunnel], "h_paired", probe)).resolves.toMatchObject({ ok: false });
		expect(probe).toHaveBeenCalledTimes(2);
	});
});

describe("a link known to be slow", () => {
	it("goes straight to the slow race on the next connect, for a while", async () => {
		const timeouts: Array<number | undefined> = [];
		let calls = 0;
		const probe = vi.fn(async (endpoint: Endpoint, _signal: AbortSignal, options?: { timeoutMs?: number }) => {
			timeouts.push(options?.timeoutMs);
			calls += 1;
			// The first (quick) probe times out; every slow one answers.
			if (calls === 1) throw new ProbeTimeoutError(endpoint);
			return { hostId: "h_paired" };
		});
		let now = 0;
		await raceWithSlowLinkRetry([lan], "h_paired", probe, () => now);
		await raceWithSlowLinkRetry([lan], "h_paired", probe, () => now);
		expect(timeouts).toEqual([undefined, SLOW_LINK_PROBE_TIMEOUT_MS, SLOW_LINK_PROBE_TIMEOUT_MS]);
		now = SLOW_LINK_MEMORY_MS;
		await raceWithSlowLinkRetry([lan], "h_paired", probe, () => now);
		expect(timeouts.at(-1)).toBeUndefined();
	});
});

describe("the connection in use", () => {
	const working = { hostId: "h_paired", host: "192.168.1.42", httpPort: "3011", password: "t", secure: false, endpointKind: "lan" } as never;

	it("still answers when its address says it is the same machine", async () => {
		vi.stubGlobal("fetch", slowIdentity(4_000));
		vi.useFakeTimers();
		const check = endpointStillAnswers(working);
		await vi.advanceTimersByTimeAsync(4_100);
		expect(await check).toBe(true);
	});

	it("does not when another machine or nothing answers there", async () => {
		vi.stubGlobal("fetch", slowIdentity(10, "someone-else"));
		expect(await endpointStillAnswers(working)).toBe(false);
		vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("Network request failed"); }));
		expect(await endpointStillAnswers(working)).toBe(false);
	});
});

describe("reconnecting after a failure", () => {
	it("tries again soon, then less often", () => {
		expect([0, 1, 2, 3, 9].map(recoveryDelayMs)).toEqual([2_000, 4_000, 8_000, 15_000, 15_000]);
	});
});
