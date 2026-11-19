import { describe, expect, it } from "vitest";
import type { Endpoint } from "./endpoints";
import {
	LOCAL_NETWORK_RETRY_MS,
	LOCAL_NETWORK_SETTLE_MS,
	LOCAL_NETWORK_WAIT_MS,
	mayAwaitLocalNetwork,
	raceAwaitingLocalNetwork,
} from "./localNetworkWait";
import type { RaceOutcome } from "./race";

const lan: Endpoint = { kind: "lan", host: "192.168.29.189", port: 3011, secure: false };
const tunnel: Endpoint = { kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true };
const tailnet: Endpoint = { kind: "tailscale", host: "100.101.102.103", port: 3011, secure: false };

const reachedNothing: RaceOutcome = { ok: false, reason: "none-reachable" };
const answered: RaceOutcome = { ok: true, endpoint: lan, hostId: "h_1" };

// On a fresh iOS install the first request to a LAN address fails at once and
// raises the Local Network prompt (seen on an iPhone 13, iOS 27.2: the request
// never left the phone). Reporting that as an error asks the user to fix a
// problem that tapping Allow is about to fix.
describe("whether a failed scan may be waiting on the Local Network prompt", () => {
	it("applies to an iOS code with a LAN address before access is confirmed", () => {
		expect(mayAwaitLocalNetwork([lan], "ios", false)).toBe(true);
		expect(mayAwaitLocalNetwork([tunnel, lan], "ios", false)).toBe(true);
	});

	// Once a LAN request has gone through, iOS has granted access, so reaching
	// nothing really is an unreachable machine and should say so at once.
	it("does not apply once access is confirmed", () => {
		expect(mayAwaitLocalNetwork([lan], "ios", true)).toBe(false);
	});

	it("does not apply on Android, which has no such prompt", () => {
		expect(mayAwaitLocalNetwork([lan], "android", false)).toBe(false);
	});

	// Tailscale rides a VPN interface and a tunnel is public: neither is gated.
	it("does not apply to a code with no LAN address", () => {
		expect(mayAwaitLocalNetwork([tunnel, tailnet], "ios", false)).toBe(false);
	});
});

function clock() {
	let t = 0;
	return {
		now: () => t,
		sleep: async (ms: number) => {
			t += ms;
		},
	};
}

describe("racing again while the Local Network prompt is answered", () => {
	it("returns the first answer without waiting", async () => {
		const c = clock();
		let calls = 0;
		const outcome = await raceAwaitingLocalNetwork(async () => (calls++, answered), { ...c, stopped: () => false, answeredAt: () => null, onWaiting: () => {} });
		expect(outcome).toEqual(answered);
		expect(calls).toBe(1);
	});

	it("keeps racing until the user allows access, then connects", async () => {
		const c = clock();
		const results = [reachedNothing, reachedNothing, reachedNothing, answered];
		let waiting = 0;
		const outcome = await raceAwaitingLocalNetwork(async () => results.shift()!, {
			...c,
			stopped: () => false,
			answeredAt: () => null,
			onWaiting: () => waiting++,
		});
		expect(outcome).toEqual(answered);
		expect(waiting).toBeGreaterThan(0);
		expect(c.now()).toBe(3 * LOCAL_NETWORK_RETRY_MS);
	});

	// Denied, or simply not on that Wi-Fi: the error still comes, just later.
	it("gives up with the last failure once the wait runs out", async () => {
		const c = clock();
		let calls = 0;
		const outcome = await raceAwaitingLocalNetwork(async () => (calls++, reachedNothing), {
			...c,
			stopped: () => false,
			answeredAt: () => null,
			onWaiting: () => {},
		});
		expect(outcome).toEqual(reachedNothing);
		expect(c.now()).toBeGreaterThanOrEqual(LOCAL_NETWORK_WAIT_MS);
		expect(c.now()).toBeLessThan(LOCAL_NETWORK_WAIT_MS + LOCAL_NETWORK_RETRY_MS + 1);
		expect(calls).toBeGreaterThan(1);
	});

	// Waiting cannot fix an incompatible host, and each race is the same answer.
	it("does not retry a failure other than reaching nothing", async () => {
		const c = clock();
		let calls = 0;
		const incompatible: RaceOutcome = { ok: false, reason: "incompatible" };
		const outcome = await raceAwaitingLocalNetwork(async () => (calls++, incompatible), {
			...c,
			stopped: () => false,
			answeredAt: () => null,
			onWaiting: () => {},
		});
		expect(outcome).toEqual(incompatible);
		expect(calls).toBe(1);
	});

	it("stops racing once the user leaves the screen", async () => {
		const c = clock();
		let calls = 0;
		let left = false;
		const outcome = await raceAwaitingLocalNetwork(
			async () => {
				calls++;
				if (calls === 2) left = true;
				return reachedNothing;
			},
			{ ...c, stopped: () => left, answeredAt: () => null, onWaiting: () => {} },
		);
		expect(outcome).toEqual(reachedNothing);
		expect(calls).toBe(2);
	});

	// Answering the prompt sends the app inactive and straight back (8 ms apart
	// on the iPhone 13). After that, a machine that still does not answer is
	// really out of reach — not on this Wi-Fi, or access was denied — and the
	// user should hear so in seconds, not at the end of the full wait.
	it("gives up shortly after the prompt is answered", async () => {
		const c = clock();
		const answered = 4_000;
		const outcome = await raceAwaitingLocalNetwork(async () => reachedNothing, {
			...c,
			stopped: () => false,
			answeredAt: () => (c.now() >= answered ? answered : null),
			onWaiting: () => {},
		});
		expect(outcome).toEqual(reachedNothing);
		expect(c.now()).toBeGreaterThanOrEqual(answered + LOCAL_NETWORK_SETTLE_MS);
		expect(c.now()).toBeLessThan(answered + LOCAL_NETWORK_SETTLE_MS + LOCAL_NETWORK_RETRY_MS + 1);
	});
});
