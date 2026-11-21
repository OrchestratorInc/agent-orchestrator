import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./config", () => ({}));
vi.mock("./hosts", () => ({
	findHost: vi.fn(),
	updateHostEndpoints: vi.fn(),
	endpointRevisionOf: vi.fn(() => 0),
	adoptHostIdentity: vi.fn(),
	touchHost: vi.fn(),
}));

import { endpointStillAnswers, forgetSlowLinks, PROBE_TIMEOUT_MS, probeEndpoint, raceWithSlowLinkRetry, SLOW_LINK_MEMORY_MS, SLOW_LINK_PROBE_TIMEOUT_MS } from "./connectRuntime";
import { recoveryDelayMs } from "./reRace";
import type { Endpoint } from "./endpoints";
import { TUNNEL_PROBE_RETRY_DELAY_MS } from "./probeRetry";
import { IncompatibleHostVersionError, ProbeTimeoutError, raceEndpoints } from "./race";

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

	it("keeps retrying a timed-out tunnel for a caller with no slow race behind it, as pairing calls it", async () => {
		vi.useFakeTimers();
		// The first attempt runs out the quick timeout; the retry answers at once.
		const slow = slowIdentity(60_000);
		const fetchMock = vi.fn((url: string, init?: { signal?: AbortSignal }) => fetchMock.mock.calls.length === 1
			? slow(url, init)
			: Promise.resolve({ ok: true, json: async () => ({ hostId: "h_paired", apiVersion: 1 }) }));
		vi.stubGlobal("fetch", fetchMock);
		// The way app/pair.tsx races: probeEndpoint with no options.
		const outcome = raceEndpoints([tunnel], "h_paired", probeEndpoint);
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + TUNNEL_PROBE_RETRY_DELAY_MS + 100);
		await expect(outcome).resolves.toMatchObject({ ok: true, endpoint: tunnel, hostId: "h_paired" });
		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	it("ends a timed-out tunnel's attempt at once only when asked to", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("fetch", slowIdentity(60_000));
		const probe = probeEndpoint(tunnel, new AbortController().signal, { endOnTimeout: true });
		const caught = probe.catch((error: unknown) => error);
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS);
		expect(await caught).toBeInstanceOf(ProbeTimeoutError);
	});

	it("names the endpoints that timed out, and none when every one refused", async () => {
		const timeout = (endpoint: Endpoint) => Promise.reject(new ProbeTimeoutError(endpoint));
		await expect(raceEndpoints([lan, tunnel], "h_paired", timeout)).resolves.toEqual({ ok: false, reason: "none-reachable", timedOut: [lan, tunnel] });
		const refusedLan = (endpoint: Endpoint) => endpoint === lan ? Promise.reject(new Error("refused")) : timeout(endpoint);
		await expect(raceEndpoints([lan, tunnel], "h_paired", refusedLan)).resolves.toEqual({ ok: false, reason: "none-reachable", timedOut: [tunnel] });
		const refused = () => Promise.reject(new Error("refused"));
		await expect(raceEndpoints([lan, tunnel], "h_paired", refused)).resolves.toEqual({ ok: false, reason: "none-reachable" });
	});

	it("gives a slow tunnel its longer try when an old LAN address refused at once", async () => {
		vi.useFakeTimers();
		const tunnelAnswer = slowIdentity(4_000);
		const fetchMock = vi.fn((url: string, init?: { signal?: AbortSignal }) => url.includes(lan.host)
			? Promise.reject(new TypeError("Network request failed"))
			: tunnelAnswer(url, init));
		vi.stubGlobal("fetch", fetchMock);
		const outcome = raceWithSlowLinkRetry([lan, tunnel], "h_paired");
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + 4_000 + 300);
		await expect(outcome).resolves.toMatchObject({ ok: true, endpoint: tunnel, hostId: "h_paired" });
		// The refused LAN address was asked once, not again in the slow race.
		expect(fetchMock.mock.calls.filter(([url]) => url.includes(lan.host))).toHaveLength(1);
	});

	it("gives a slow compatible tunnel its longer try beside an incompatible LAN answer", async () => {
		vi.useFakeTimers();
		const tunnelAnswer = slowIdentity(4_000);
		vi.stubGlobal("fetch", vi.fn((url: string, init?: { signal?: AbortSignal }) => url.includes(lan.host)
			? Promise.resolve({ ok: true, json: async () => ({ hostId: "h_paired", apiVersion: 2 }) })
			: tunnelAnswer(url, init)));
		const outcome = raceWithSlowLinkRetry([lan, tunnel], "h_paired");
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + 4_000 + 300);
		await expect(outcome).resolves.toMatchObject({ ok: true, endpoint: tunnel, hostId: "h_paired" });
	});

	it("still reports incompatible when the slow try finds nothing either", async () => {
		const probe = vi.fn((endpoint: Endpoint) => endpoint === lan
			? Promise.reject(new IncompatibleHostVersionError("h_paired"))
			: Promise.reject(new ProbeTimeoutError(endpoint)));
		await expect(raceWithSlowLinkRetry([lan, tunnel], "h_paired", probe)).resolves.toEqual({ ok: false, reason: "incompatible" });
		expect(probe).toHaveBeenCalledTimes(3);
	});

	it("bounds a tunnel that never answers to the quick timeout plus one slow try", async () => {
		vi.useFakeTimers();
		const fetchMock = slowIdentity(120_000);
		vi.stubGlobal("fetch", fetchMock);
		let settled: unknown;
		void raceWithSlowLinkRetry([tunnel], "h_paired").then((result) => { settled = result; });
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + SLOW_LINK_PROBE_TIMEOUT_MS + 100);
		expect(settled).toMatchObject({ ok: false, reason: "none-reachable" });
		// One quick attempt and one slow attempt: no propagation retries.
		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	it("does not retry a slow answer from a different machine", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("fetch", slowIdentity(4_000, "someone-else"));
		const outcome = raceWithSlowLinkRetry([lan], "h_paired");
		await vi.advanceTimersByTimeAsync(PROBE_TIMEOUT_MS + 4_000 + 300);
		await expect(outcome).resolves.toMatchObject({ ok: false, reason: "none-reachable" });
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

	it("still retries a tunnel that fails fast while it is still resolving", async () => {
		vi.useFakeTimers();
		// Mark the machine slow: its quick probe times out and the slow one answers.
		const marking = vi.fn(async (endpoint: Endpoint, _signal: AbortSignal, options?: { timeoutMs?: number }) => {
			if (options?.timeoutMs === undefined) throw new ProbeTimeoutError(endpoint);
			return { hostId: "h_slow_tunnel" };
		});
		await raceWithSlowLinkRetry([tunnel], "h_slow_tunnel", marking);
		// The next connect races slow at once, against a tunnel whose new name
		// does not resolve on the first try.
		const fetchMock = vi.fn(async () => fetchMock.mock.calls.length === 1
			? Promise.reject(new TypeError("Network request failed"))
			: { ok: true, json: async () => ({ hostId: "h_slow_tunnel", apiVersion: 1 }) });
		vi.stubGlobal("fetch", fetchMock);
		const outcome = raceWithSlowLinkRetry([tunnel], "h_slow_tunnel");
		await vi.advanceTimersByTimeAsync(TUNNEL_PROBE_RETRY_DELAY_MS + 100);
		await expect(outcome).resolves.toMatchObject({ ok: true, endpoint: tunnel, hostId: "h_slow_tunnel" });
		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	it("still gives a timed-out endpoint only its one slow try", async () => {
		const marking = vi.fn(async (endpoint: Endpoint, _signal: AbortSignal, options?: { timeoutMs?: number }) => {
			if (options?.timeoutMs === undefined) throw new ProbeTimeoutError(endpoint);
			return { hostId: "h_slow_dead" };
		});
		await raceWithSlowLinkRetry([tunnel], "h_slow_dead", marking);
		vi.useFakeTimers();
		const fetchMock = slowIdentity(120_000, "h_slow_dead");
		vi.stubGlobal("fetch", fetchMock);
		let settled: unknown;
		void raceWithSlowLinkRetry([tunnel], "h_slow_dead").then((result) => { settled = result; });
		await vi.advanceTimersByTimeAsync(SLOW_LINK_PROBE_TIMEOUT_MS + 100);
		expect(settled).toMatchObject({ ok: false, reason: "none-reachable" });
		expect(fetchMock).toHaveBeenCalledTimes(1);
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
