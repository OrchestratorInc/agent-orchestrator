import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./config", () => ({}));
vi.mock("./hosts", () => ({
	findHost: vi.fn(),
	updateHostEndpoints: vi.fn(),
	adoptHostIdentity: vi.fn(),
	touchHost: vi.fn(),
}));

import { PROBE_TIMEOUT_MS, probeEndpoint, raceWithSlowLinkRetry, SLOW_LINK_PROBE_TIMEOUT_MS } from "./connectRuntime";
import type { Endpoint } from "./endpoints";
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
