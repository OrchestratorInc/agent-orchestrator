import { mkdtemp, readFile, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TELEMETRY_OPT_OUT_FILE, TelemetryIdentityController } from "./telemetry-identity";

const ORIGIN = "http://127.0.0.1:4010";

async function controller(fetcher = vi.fn(async () => new Response(null, { status: 204 })), origin: string | null = ORIGIN) {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	const broadcast = vi.fn();
	return { dir, fetcher, broadcast, ctl: new TelemetryIdentityController(dir, () => origin, fetcher as never, broadcast) };
}

describe("TelemetryIdentityController opt-out", () => {
	it("writes and clears the marker the daemon reads, and broadcasts", async () => {
		const { dir, ctl, broadcast } = await controller();
		expect(ctl.isOptedOut()).toBe(false);
		await ctl.setOptedOut(true);
		expect(ctl.isOptedOut()).toBe(true);
		await expect(stat(path.join(dir, TELEMETRY_OPT_OUT_FILE))).resolves.toBeTruthy();
		await ctl.setOptedOut(false);
		expect(ctl.isOptedOut()).toBe(false);
		expect(broadcast.mock.calls).toEqual([[true], [false]]);
		await expect(readFile(path.join(dir, TELEMETRY_OPT_OUT_FILE))).rejects.toThrow();
	});
});

describe("TelemetryIdentityController cloud user hand-off", () => {
	it("does not send a launch-time sign-out before the renderer resolves its session", async () => {
		const { ctl, fetcher } = await controller();
		ctl.invalidate();
		await ctl.flush();
		expect(fetcher).not.toHaveBeenCalled();
		expect(await ctl.setCloudUser("user_A")).toBe(true);
		expect(fetcher).toHaveBeenCalledTimes(1);
		expect(JSON.parse((fetcher.mock.calls[0] as unknown as [string, RequestInit])[1].body as string).cloudUserId).toBe("user_A");
	});

	it("posts only the user id to the loopback daemon", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser("user_01HABC");
		const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe(`${ORIGIN}/internal/telemetry/identity`);
		expect(JSON.parse(init.body as string)).toEqual({ cloudUserId: "user_01HABC", sequence: expect.any(Number) });
	});

	it("clears on sign-out and rejects email-shaped ids", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser(null);
		expect(JSON.parse((fetcher.mock.calls[0] as unknown as [string, RequestInit])[1].body as string)).toEqual({ cloudUserId: "", sequence: expect.any(Number) });
		await expect(ctl.setCloudUser("a@b.com")).rejects.toThrow();
	});

	it("retries on flush when the daemon was not ready, and stops after success", async () => {
		const fetcher = vi.fn(async () => new Response(null, { status: 204 }));
		let origin: string | null = null;
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => origin, fetcher as never);
		await ctl.setCloudUser("user_1");
		expect(fetcher).not.toHaveBeenCalled();
		origin = ORIGIN;
		await ctl.flush();
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it("replays the user to a restarted daemon, even on the same port", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser("user_1");
		await ctl.flush(); // ready again, nothing changed: no replay
		expect(fetcher).toHaveBeenCalledTimes(1);

		ctl.invalidate(); // daemon left ready (restart)
		await ctl.flush(); // same origin comes back
		expect(fetcher).toHaveBeenCalledTimes(2);
		expect(JSON.parse((fetcher.mock.calls[1] as unknown as [string, RequestInit])[1].body as string)).toEqual({ cloudUserId: "user_1", sequence: expect.any(Number) });
	});

	it("a response from the invalidated daemon cannot mark the new daemon as synced", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		const fetcher = vi.fn(async () => {
			if (fetcher.mock.calls.length === 1) await gate; // daemon A's request hangs
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const setUser = ctl.setCloudUser("user_1"); // in flight to daemon A
		await new Promise((resolve) => setTimeout(resolve, 0));
		ctl.invalidate(); // A leaves ready
		release(); // A's late 204 arrives before daemon B is ready
		await setUser;
		await ctl.flush(); // B ready: must still replay the user
		expect(fetcher).toHaveBeenCalledTimes(2);
		await ctl.flush(); // and B is now synced
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("never lets an older user's request land after a newer one: the daemon ends on the latest", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		let started!: () => void;
		const firstStarted = new Promise<void>((resolve) => (started = resolve));
		// A fake daemon that records the order identities were committed in.
		const committed: string[] = [];
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			const user = JSON.parse(init.body as string).cloudUserId as string;
			if (fetcher.mock.calls.length === 1) {
				started();
				await gate; // user_1's request hangs in flight
			}
			committed.push(user);
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const first = ctl.setCloudUser("user_1");
		await firstStarted; // user_1's request is genuinely in flight before user_2 arrives
		const second = ctl.setCloudUser("user_2");
		await Promise.resolve();
		expect(fetcher).toHaveBeenCalledTimes(1); // user_2 waits its turn
		release();
		await Promise.all([first, second]);

		expect(fetcher).toHaveBeenCalledTimes(2);
		expect(committed).toEqual(["user_1", "user_2"]);
		await ctl.flush(); // already synced to user_2: nothing more to send
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("a request delayed past the timeout cannot overwrite a newer identity on the daemon", async () => {
		// A fake daemon that enforces the same rule as the real route: refuse a
		// sequence older than the newest one applied.
		let daemonUser = "";
		let lastSeq = 0;
		const deliver = (init: RequestInit): Response => {
			const body = JSON.parse(init.body as string) as { cloudUserId: string; sequence: number };
			if (body.sequence < lastSeq) return new Response(null, { status: 409 });
			lastSeq = body.sequence;
			daemonUser = body.cloudUserId;
			return new Response(null, { status: 204 });
		};
		let deliverLate!: () => void;
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			if (fetcher.mock.calls.length === 1) {
				// The client gives up (timeout) but the request is still on its way.
				deliverLate = () => void deliver(init);
				throw new Error("aborted");
			}
			return deliver(init);
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		await ctl.setCloudUser("user_A"); // times out client side
		await ctl.setCloudUser("user_B"); // commits on the daemon
		expect(daemonUser).toBe("user_B");
		deliverLate(); // A finally reaches the daemon
		expect(daemonUser).toBe("user_B"); // the daemon refused it
		await ctl.flush(); // B was acknowledged: nothing to correct
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("stamps strictly increasing sequence numbers", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser("user_A");
		await ctl.setCloudUser("user_B");
		await ctl.setCloudUser(null);
		const seqs = fetcher.mock.calls.map((c) => JSON.parse((c as unknown as [string, RequestInit])[1].body as string).sequence as number);
		expect(seqs).toHaveLength(3);
		expect(seqs[1]).toBeGreaterThan(seqs[0]);
		expect(seqs[2]).toBeGreaterThan(seqs[1]);
	});

	it("a queued hand-off sends the newest state, so a stale one is never replayed", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		let started!: () => void;
		const firstStarted = new Promise<void>((resolve) => (started = resolve));
		const committed: string[] = [];
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			const user = JSON.parse(init.body as string).cloudUserId as string;
			if (fetcher.mock.calls.length === 1) {
				started();
				await gate;
			}
			committed.push(user);
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const a = ctl.setCloudUser("user_A");
		await firstStarted; // A is in flight; B and the sign-out queue behind it
		const b = ctl.setCloudUser("user_B");
		const out = ctl.setCloudUser(null);
		release();
		await Promise.all([a, b, out]);

		// A went out first; B was superseded before its turn, so only the newest
		// state (signed out) follows.
		expect(committed).toEqual(["user_A", ""]);
	});

	it("refuses a non-loopback origin", async () => {
		const { ctl, fetcher } = await controller(undefined, "http://example.com:4010");
		await ctl.setCloudUser("user_1");
		expect(fetcher).not.toHaveBeenCalled();
		expect(await ctl.githubLogin()).toBeNull();
	});
});

describe("TelemetryIdentityController githubLogin", () => {
	it("reads the daemon-resolved login", async () => {
		const fetcher = vi.fn(async () => new Response(JSON.stringify({ githubLogin: "octocat" }), { status: 200 }));
		const { ctl } = await controller(fetcher);
		expect(await ctl.githubLogin()).toBe("octocat");
		expect((fetcher.mock.calls[0] as unknown as [string])[0]).toBe(`${ORIGIN}/api/v1/telemetry/identity`);
	});

	it("is null when unresolved or the daemon is down", async () => {
		expect(await (await controller(vi.fn(async () => new Response("{}", { status: 200 })))).ctl.githubLogin()).toBeNull();
		expect(await (await controller(vi.fn(async () => { throw new Error("down"); }))).ctl.githubLogin()).toBeNull();
	});
});

describe("TelemetryIdentityController acknowledgement", () => {
	it("acknowledges only a 2xx from the daemon", async () => {
		const ok = await controller();
		expect(await ok.ctl.setCloudUser("user_A")).toBe(true);
		const failing = await controller(vi.fn(async () => new Response(null, { status: 500 })));
		expect(await failing.ctl.setCloudUser("user_A")).toBe(false);
		const unreachable = await controller(vi.fn(async () => { throw new Error("ECONNREFUSED"); }));
		expect(await unreachable.ctl.setCloudUser("user_A")).toBe(false);
		const noDaemon = await controller(undefined, null);
		expect(await noDaemon.ctl.setCloudUser("user_A")).toBe(false);
		// An older daemon without the route cannot record a binding either.
		const old = await controller(vi.fn(async () => new Response(null, { status: 404 })));
		expect(await old.ctl.setCloudUser("user_A")).toBe("unsupported");
	});

	it("A unavailable, sign-out, recovery: the daemon ends signed out and A was never acknowledged", async () => {
		let daemonUp = false;
		let daemonUser = "";
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			if (!daemonUp) return new Response(null, { status: 500 });
			daemonUser = JSON.parse(init.body as string).cloudUserId;
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		expect(await ctl.setCloudUser("user_A")).toBe(false); // renderer must not identify
		expect(await ctl.setCloudUser(null)).toBe(false); // still down
		daemonUp = true;
		await ctl.flush(); // recovery replays the newest state only
		expect(daemonUser).toBe("");
	});

	it("a superseded call is not acknowledged even if the daemon accepted its state", async () => {
		let releaseResponse!: () => void;
		const held = new Promise<void>((resolve) => (releaseResponse = resolve));
		let applied!: () => void;
		const aApplied = new Promise<void>((resolve) => (applied = resolve));
		let daemonUser = "";
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			daemonUser = JSON.parse(init.body as string).cloudUserId; // the daemon applies the state...
			if (fetcher.mock.calls.length === 1) {
				applied();
				await held; // ...but A's response is still on its way back
			}
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const first = ctl.setCloudUser("user_A");
		await aApplied;
		expect(daemonUser).toBe("user_A");
		const second = ctl.setCloudUser("user_B"); // supersedes A while A's response is held
		releaseResponse();

		expect(await first).toBe(false); // A's late 204 must not count: the state moved on
		expect(await second).toBe(true);
		expect(daemonUser).toBe("user_B");
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("pending anonymous request, A sign-in, sign-out before the queue drains: A is never acknowledged", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		let started!: () => void;
		const firstStarted = new Promise<void>((resolve) => (started = resolve));
		const sent: string[] = [];
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			if (fetcher.mock.calls.length === 1) {
				started();
				await gate; // the anonymous request is still in flight
			}
			sent.push(JSON.parse(init.body as string).cloudUserId);
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const anonymous = ctl.setCloudUser(null);
		await firstStarted;
		const signIn = ctl.setCloudUser("user_A"); // queued behind it
		const signOut = ctl.setCloudUser(null); // replaces A before the queue drains
		release();

		expect(await anonymous).toBe(false);
		expect(await signIn).toBe(false); // the renderer must not identify A
		expect(await signOut).toBe(true);
		expect(sent).toEqual(["", ""]); // the original request, then only the newest state
		expect(sent).not.toContain("user_A");
	});
});

describe("TelemetryIdentityController across app restarts", () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it("a new controller whose clock is behind the daemon's sequence catches up instead of being refused", async () => {
		// A daemon that outlived the previous app process and enforces ordering.
		let daemonUser = "";
		let lastSeq = 0;
		const daemon = vi.fn(async (_url: string, init: RequestInit) => {
			const body = JSON.parse(init.body as string) as { cloudUserId: string; sequence: number };
			if (body.sequence < lastSeq) {
				return new Response(JSON.stringify({ code: "STALE_TELEMETRY_IDENTITY", details: { lastSequence: lastSeq } }), { status: 409 });
			}
			lastSeq = body.sequence;
			daemonUser = body.cloudUserId;
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));

		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-14T12:00:00Z"));
		const before = new TelemetryIdentityController(dir, () => ORIGIN, daemon as never);
		expect(await before.setCloudUser("user_A")).toBe(true);
		expect(daemonUser).toBe("user_A");

		// Electron restarts after the system clock was corrected backward.
		vi.setSystemTime(new Date("2026-10-14T11:00:00Z"));
		const after = new TelemetryIdentityController(dir, () => ORIGIN, daemon as never);
		expect(await after.setCloudUser("user_B")).toBe(true);
		expect(daemonUser).toBe("user_B");
		expect(daemon).toHaveBeenCalledTimes(3); // A, B refused, B accepted
		await after.flush(); // acknowledged: nothing left to send
		expect(daemon).toHaveBeenCalledTimes(3);
	});

	it("gives up quietly when the 409 carries no usable sequence", async () => {
		const { ctl, fetcher } = await controller(vi.fn(async () => new Response("{}", { status: 409 })));
		expect(await ctl.setCloudUser("user_A")).toBe(false);
		expect(fetcher).toHaveBeenCalledTimes(1);
	});
});
