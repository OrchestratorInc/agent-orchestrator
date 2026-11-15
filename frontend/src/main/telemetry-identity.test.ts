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
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(1);

		ctl.invalidate();
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(2);
		expect(JSON.parse((fetcher.mock.calls[1] as unknown as [string, RequestInit])[1].body as string)).toEqual({ cloudUserId: "user_1", sequence: expect.any(Number) });
	});

	it("a response from the invalidated daemon cannot mark the new daemon as synced", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		const fetcher = vi.fn(async () => {
			if (fetcher.mock.calls.length === 1) await gate;
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const setUser = ctl.setCloudUser("user_1");
		await new Promise((resolve) => setTimeout(resolve, 0));
		ctl.invalidate();
		release();
		await setUser;
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(2);
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("never lets an older user's request land after a newer one: the daemon ends on the latest", async () => {
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

		const first = ctl.setCloudUser("user_1");
		await firstStarted;
		const second = ctl.setCloudUser("user_2");
		await Promise.resolve();
		expect(fetcher).toHaveBeenCalledTimes(1);
		release();
		await Promise.all([first, second]);

		expect(fetcher).toHaveBeenCalledTimes(2);
		expect(committed).toEqual(["user_1", "user_2"]);
		await ctl.flush();
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it("a request delayed past the timeout cannot overwrite a newer identity on the daemon", async () => {
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
				deliverLate = () => void deliver(init);
				throw new Error("aborted");
			}
			return deliver(init);
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		await ctl.setCloudUser("user_A");
		await ctl.setCloudUser("user_B");
		expect(daemonUser).toBe("user_B");
		deliverLate();
		expect(daemonUser).toBe("user_B");
		await ctl.flush();
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
		await firstStarted;
		const b = ctl.setCloudUser("user_B");
		const out = ctl.setCloudUser(null);
		release();
		await Promise.all([a, b, out]);

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

		expect(await ctl.setCloudUser("user_A")).toBe(false);
		expect(await ctl.setCloudUser(null)).toBe(false);
		daemonUp = true;
		await ctl.flush();
		expect(daemonUser).toBe("");
	});

	it("a superseded call is not acknowledged even if the daemon accepted its state", async () => {
		let releaseResponse!: () => void;
		const held = new Promise<void>((resolve) => (releaseResponse = resolve));
		let applied!: () => void;
		const aApplied = new Promise<void>((resolve) => (applied = resolve));
		let daemonUser = "";
		const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
			daemonUser = JSON.parse(init.body as string).cloudUserId;
			if (fetcher.mock.calls.length === 1) {
				applied();
				await held;
			}
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const first = ctl.setCloudUser("user_A");
		await aApplied;
		expect(daemonUser).toBe("user_A");
		const second = ctl.setCloudUser("user_B");
		releaseResponse();

		expect(await first).toBe(false);
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
				await gate;
			}
			sent.push(JSON.parse(init.body as string).cloudUserId);
			return new Response(null, { status: 204 });
		});
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		const ctl = new TelemetryIdentityController(dir, () => ORIGIN, fetcher as never);

		const anonymous = ctl.setCloudUser(null);
		await firstStarted;
		const signIn = ctl.setCloudUser("user_A");
		const signOut = ctl.setCloudUser(null);
		release();

		expect(await anonymous).toBe(false);
		expect(await signIn).toBe(false);
		expect(await signOut).toBe(true);
		expect(sent).toEqual(["", ""]);
		expect(sent).not.toContain("user_A");
	});
});

describe("TelemetryIdentityController across app restarts", () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it("a new controller whose clock is behind the daemon's sequence catches up instead of being refused", async () => {
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

		vi.setSystemTime(new Date("2026-10-14T11:00:00Z"));
		const after = new TelemetryIdentityController(dir, () => ORIGIN, daemon as never);
		expect(await after.setCloudUser("user_B")).toBe(true);
		expect(daemonUser).toBe("user_B");
		expect(daemon).toHaveBeenCalledTimes(3);
		await after.flush();
		expect(daemon).toHaveBeenCalledTimes(3);
	});

	it("gives up quietly when the 409 carries no usable sequence", async () => {
		const { ctl, fetcher } = await controller(vi.fn(async () => new Response("{}", { status: 409 })));
		expect(await ctl.setCloudUser("user_A")).toBe(false);
		expect(fetcher).toHaveBeenCalledTimes(1);
	});
});

describe("TelemetryIdentityController sign-out without the daemon", () => {
	it("retires the binding itself, rotating the anonymous ID first", async () => {
		const { mkdir, writeFile, access } = await import("node:fs/promises");
		const { ctl, dir } = await controller(vi.fn(async () => { throw new Error("connection refused"); }));
		await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");
		await writeFile(path.join(dir, "telemetry_anonymous_id"), "anon_linked\n");
		await mkdir(path.join(dir, "telemetry"), { recursive: true });
		await writeFile(path.join(dir, "telemetry", "cli-usage-daily.json"), "{}\n");

		expect(await ctl.setCloudUser(null)).toBe(false);

		await expect(access(path.join(dir, "telemetry_bound_user"))).rejects.toThrow();
		await expect(access(path.join(dir, "telemetry", "cli-usage-daily.json"))).rejects.toThrow();
		const anonymous = (await readFile(path.join(dir, "telemetry_anonymous_id"), "utf8")).trim();
		expect(anonymous).toMatch(/^anon_[0-9a-f-]{36}$/);
		expect(anonymous).not.toBe("anon_linked");
	});

	it("leaves an unbound install's anonymous ID alone", async () => {
		const { writeFile } = await import("node:fs/promises");
		const { ctl, dir } = await controller();
		await writeFile(path.join(dir, "telemetry_anonymous_id"), "anon_unlinked\n");
		await ctl.setCloudUser(null);
		expect((await readFile(path.join(dir, "telemetry_anonymous_id"), "utf8")).trim()).toBe("anon_unlinked");
	});
});

describe("TelemetryIdentityController sign-out then sign-in", () => {
	it("never removes the binding of an account that signed in after the sign-out", async () => {
		const { writeFile, access } = await import("node:fs/promises");
		const { ctl, dir } = await controller();
		await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");
		const signOut = ctl.setCloudUser(null);
		const signIn = ctl.setCloudUser("user_B");
		await writeFile(path.join(dir, "telemetry_bound_user"), "user_B\n");
		await Promise.all([signOut, signIn]);
		await expect(access(path.join(dir, "telemetry_bound_user"))).resolves.toBeUndefined();
	});
});

describe("TelemetryIdentityController after the app quit mid sign-out", () => {
  it("a fresh controller retires a binding the daemon never cleared", async () => {
    const { writeFile, access } = await import("node:fs/promises");
    const { dir } = await controller(vi.fn(async () => { throw new Error("connection refused"); }));
    await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");
    await writeFile(path.join(dir, "telemetry_anonymous_id"), "anon_linked\n");
    const relaunched = new TelemetryIdentityController(dir, () => null);
    await relaunched.retireSignedOutBinding();
    await expect(access(path.join(dir, "telemetry_bound_user"))).rejects.toThrow();
    expect((await readFile(path.join(dir, "telemetry_anonymous_id"), "utf8")).trim()).not.toBe("anon_linked");
  });
});
