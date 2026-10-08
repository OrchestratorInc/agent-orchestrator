import { mkdtemp, readFile, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
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
	it("posts only the user id to the loopback daemon", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser("user_01HABC");
		const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe(`${ORIGIN}/internal/telemetry/identity`);
		expect(JSON.parse(init.body as string)).toEqual({ cloudUserId: "user_01HABC" });
	});

	it("clears on sign-out and rejects email-shaped ids", async () => {
		const { ctl, fetcher } = await controller();
		await ctl.setCloudUser(null);
		expect(JSON.parse((fetcher.mock.calls[0] as unknown as [string, RequestInit])[1].body as string)).toEqual({ cloudUserId: "" });
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
