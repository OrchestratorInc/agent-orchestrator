import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type ArtifactSnapshotJob, artifactSnapshotUrl, parseArtifactSnapshotUrl } from "../shared/artifact-snapshot";
import {
	artifactSnapshotKey,
	SnapshotCancelledError,
	createArtifactSnapshotStore,
	registerArtifactSnapshotIpc,
	registerArtifactSnapshotProtocol,
	SNAPSHOT_DROPPED,
	SNAPSHOT_MAX_QUEUED,
	SnapshotUnavailableError,
} from "./artifact-snapshots";

const job = (overrides: Partial<ArtifactSnapshotJob> = {}): ArtifactSnapshotJob => ({
	sessionId: "s1",
	path: "report.html",
	name: "report.html",
	kind: "html",
	updatedAt: "2026-11-10T10:00:00Z",
	sessionTitle: "Session",
	theme: "dark",
	styleTheme: "",
	...overrides,
});

let directory: string;
beforeEach(async () => {
	directory = await fs.mkdtemp(path.join(os.tmpdir(), "ao-snapshots-"));
});
afterEach(async () => {
	await fs.rm(directory, { force: true, recursive: true });
});

describe("artifactSnapshotKey", () => {
	it("changes with the file version and the theme, not with the URLs", () => {
		const base = artifactSnapshotKey(job());
		expect(artifactSnapshotKey(job({ updatedAt: "2026-11-10T11:00:00Z" }))).not.toBe(base);
		expect(artifactSnapshotKey(job({ theme: "light" }))).not.toBe(base);
		expect(artifactSnapshotKey(job({ styleTheme: "nord" }))).not.toBe(base);
		expect(artifactSnapshotKey(job({ hostId: "local" }))).not.toBe(base);
		expect(artifactSnapshotKey(job({ rawUrl: "http://127.0.0.1:9/x", inlineUrl: "http://a.localhost/y" }))).toBe(base);
	});
});

describe("artifact snapshot store", () => {
	it("renders a missing preview once, caches it on disk and serves it without rendering again", async () => {
		const render = vi.fn(async () => Buffer.from("webp-bytes"));
		const store = createArtifactSnapshotStore({ directory, render });
		expect(await store.get(job())).toBeNull();
		expect((await store.request(job()))?.toString()).toBe("webp-bytes");
		expect((await store.get(job()))?.toString()).toBe("webp-bytes");
		expect((await store.request(job()))?.toString()).toBe("webp-bytes");
		expect(render).toHaveBeenCalledTimes(1);
		const reopened = createArtifactSnapshotStore({ directory, render });
		expect((await reopened.get(job()))?.toString()).toBe("webp-bytes");
	});

	it("renders one artifact once however many cards ask at the same time", async () => {
		let release: (() => void) | undefined;
		const render = vi.fn(() => new Promise<Buffer>((resolve) => {
			release = () => resolve(Buffer.from("x"));
		}));
		const store = createArtifactSnapshotStore({ directory, render });
		const first = store.request(job());
		const second = store.request(job());
		await vi.waitFor(() => expect(render).toHaveBeenCalled());
		release?.();
		expect((await first)?.toString()).toBe("x");
		expect((await second)?.toString()).toBe("x");
		expect(render).toHaveBeenCalledTimes(1);
	});

	it("renders one preview at a time", async () => {
		let active = 0;
		let peak = 0;
		const render = vi.fn(async () => {
			active += 1;
			peak = Math.max(peak, active);
			await new Promise((resolve) => setTimeout(resolve, 5));
			active -= 1;
			return Buffer.from("x");
		});
		const store = createArtifactSnapshotStore({ directory, render });
		await Promise.all([1, 2, 3, 4, 5].map((n) => store.request(job({ path: `f${n}.html` }))));
		expect(render).toHaveBeenCalledTimes(5);
		expect(peak).toBe(1);
	});

	it("does not retry a failed render until the retry delay has passed", async () => {
		let clock = 1_000;
		const render = vi.fn<() => Promise<Buffer>>().mockRejectedValueOnce(new Error("boom")).mockResolvedValue(Buffer.from("ok"));
		const store = createArtifactSnapshotStore({ directory, render, now: () => clock });
		expect(await store.request(job())).toBeNull();
		expect(await store.request(job())).toBeNull();
		expect(render).toHaveBeenCalledTimes(1);
		clock += 61_000;
		expect((await store.request(job()))?.toString()).toBe("ok");
	});

	it("never retries a file with nothing to draw", async () => {
		const render = vi.fn<() => Promise<Buffer>>().mockRejectedValue(new SnapshotUnavailableError("no preview"));
		let clock = 1_000;
		const store = createArtifactSnapshotStore({ directory, render, now: () => clock });
		expect(await store.request(job())).toBeNull();
		clock += 24 * 60 * 60 * 1000;
		expect(await store.request(job())).toBeNull();
		expect(render).toHaveBeenCalledTimes(1);
	});

	it("renders the newest request first, and tells a dropped request apart from a failure", async () => {
		const order: string[] = [];
		const gates: Array<() => void> = [];
		const render = vi.fn((request: ArtifactSnapshotJob) => new Promise<Buffer>((resolve) => {
			order.push(request.path);
			gates.push(() => resolve(Buffer.from("x")));
		}));
		const store = createArtifactSnapshotStore({ directory, render, gapMs: 0 });
		const first = store.request(job({ path: "first.html" }));
		await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(1));
		// Each request reads the disk cache before it joins the queue: let both
		// reads settle and both requests join before the first render ends, so
		// the queue holds both when it picks the next one.
		const reads = vi.spyOn(fs, "readFile");
		const older = store.request(job({ path: "older.html" }));
		const newest = store.request(job({ path: "newest.html" }));
		await Promise.allSettled(reads.mock.results.map((result) => result.value));
		await new Promise((resolve) => setTimeout(resolve, 0));
		reads.mockRestore();
		gates.shift()?.();
		await first;
		await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(2));
		expect(order[1]).toBe("newest.html");
		// A queue over its limit drops its oldest request, and says so.
		const fillers = Array.from({ length: SNAPSHOT_MAX_QUEUED + 1 }, (_, n) => store.request(job({ path: `fill-${n}.html` })));
		expect(await older).toBe(SNAPSHOT_DROPPED);
		// Let every remaining render finish, so nothing is still writing when the directory goes.
		const settled = Promise.all([newest, ...fillers]);
		const opener = setInterval(() => gates.splice(0).forEach((open) => open()), 2);
		await settled;
		clearInterval(opener);
	});

	it("drops every waiting request on cancel, renders nothing more, and does not hold a cancelled render against its file", async () => {
		const gates: Array<() => void> = [];
		const render = vi.fn((request: ArtifactSnapshotJob) => new Promise<Buffer>((resolve, reject) => {
			if (request.path === "cancelled.html") reject(new SnapshotCancelledError("window closed"));
			else gates.push(() => resolve(Buffer.from("x")));
		}));
		const store = createArtifactSnapshotStore({ directory, render, gapMs: 0 });
		const first = store.request(job({ path: "first.html" }));
		await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(1));
		const waitingRequests = [1, 2, 3].map((n) => store.request(job({ path: `later-${n}.html` })));
		// A request reads the disk cache before it joins the queue; let all three get there.
		await new Promise((resolve) => setTimeout(resolve, 50));
		store.cancel();
		expect(await Promise.all(waitingRequests)).toEqual([SNAPSHOT_DROPPED, SNAPSHOT_DROPPED, SNAPSHOT_DROPPED]);
		gates.shift()?.();
		expect(await first).not.toBeNull();
		// Nothing waiting was started after the render under way ended.
		expect(render).toHaveBeenCalledTimes(1);
		// A render that was cancelled is not retried only after the delay: the file asks again at once.
		expect(await store.request(job({ path: "cancelled.html" }))).toBeNull();
		expect(await store.request(job({ path: "cancelled.html" }))).toBeNull();
		expect(render.mock.calls.filter(([request]) => request.path === "cancelled.html")).toHaveLength(2);
	});

	describe("Cloud snapshots", () => {
		const cloudJob = job({ path: "cloud.html", cloud: { orgId: "org-1" } });
		const store = (render = async () => Buffer.from("x")) => createArtifactSnapshotStore({ directory, render, gapMs: 0 });

		it("are made and served only for the signed-in account, from their own folder", async () => {
			const cache = store();
			expect(await cache.request(cloudJob)).toBeNull();
			await cache.bindCloudAccount("user-1");
			await cache.request(job({ path: "local.html" }));
			expect(await cache.request(cloudJob)).not.toBeNull();
			expect(await fs.readdir(path.join(directory, "cloud"))).toHaveLength(1);
			expect(await cache.get(cloudJob)).not.toBeNull();
		});

		it("are removed, and only they, on sign-out", async () => {
			const cache = store();
			await cache.bindCloudAccount("user-1");
			await cache.request(job({ path: "local.html" }));
			await cache.request(cloudJob);
			await cache.clearCloud();
			expect(await cache.get(cloudJob)).toBeNull();
			expect(await cache.get(job({ path: "local.html" }))).not.toBeNull();
		});

		it("go when another account signs in or this one is lost, even across a restart", async () => {
			const first = store();
			await first.bindCloudAccount("user-1");
			await first.request(cloudJob);
			// The same account again keeps them, across a restart too.
			const second = store();
			await second.bindCloudAccount("user-1");
			expect(await second.get(cloudJob)).not.toBeNull();
			// Another account clears them before it can be served any.
			const third = store();
			await third.bindCloudAccount("user-2");
			expect(await third.get(cloudJob)).toBeNull();
			expect(await fs.readdir(path.join(directory, "cloud")).catch(() => [])).toHaveLength(0);
			// No account at all (a session that expired for good) clears them as well.
			await third.request(cloudJob);
			const fourth = store();
			await fourth.bindCloudAccount(null);
			expect(await fourth.get(cloudJob)).toBeNull();
			expect(await fs.readdir(path.join(directory, "cloud")).catch(() => [])).toHaveLength(0);
		});

		it("keep being served, and a render under way keeps going, when the same account is bound again", async () => {
			let finish: (() => void) | undefined;
			const render = vi.fn(() => new Promise<Buffer>((resolve) => {
				finish = () => resolve(Buffer.from("x"));
			}));
			const cache = createArtifactSnapshotStore({ directory, render, gapMs: 0 });
			await cache.bindCloudAccount("user-1");
			const pending = cache.request(cloudJob);
			await vi.waitFor(() => expect(render).toHaveBeenCalled());
			// A session refresh tells main the same account is still signed in.
			await cache.bindCloudAccount("user-1");
			finish?.();
			expect(await pending).not.toBeNull();
			expect(await cache.get(cloudJob)).not.toBeNull();
		});

		it("are not served until a failed delete has been retried", async () => {
			const first = store();
			await first.bindCloudAccount("user-1");
			await first.request(cloudJob);
			const second = store();
			const remove = vi.spyOn(fs, "rm").mockRejectedValueOnce(new Error("busy"));
			await expect(second.bindCloudAccount("user-2")).rejects.toThrow("busy");
			remove.mockRestore();
			expect(await second.get(cloudJob)).toBeNull();
			// The old owner is still on record, so the next bind deletes them.
			await second.bindCloudAccount("user-2");
			expect(await fs.readdir(path.join(directory, "cloud")).catch(() => [])).toHaveLength(0);
			expect(await second.request(cloudJob)).not.toBeNull();
		});

		it("are not written by a render that finished after a sign-out", async () => {
			let finish: (() => void) | undefined;
			const render = vi.fn(() => new Promise<Buffer>((resolve) => {
				finish = () => resolve(Buffer.from("x"));
			}));
			const cache = createArtifactSnapshotStore({ directory, render, gapMs: 0 });
			await cache.bindCloudAccount("user-1");
			const pending = cache.request(cloudJob);
			await vi.waitFor(() => expect(render).toHaveBeenCalled());
			await cache.clearCloud();
			finish?.();
			expect(await pending).toBeNull();
			expect(await fs.readdir(path.join(directory, "cloud")).catch(() => [])).toHaveLength(0);
		});

		it("are not left behind by a write that was already under way when the account was lost", async () => {
			const cache = store();
			await cache.bindCloudAccount("user-1");
			const realMkdir = fs.mkdir.bind(fs);
			let release: (() => void) | undefined;
			const held = new Promise<void>((resolve) => {
				release = resolve;
			});
			const mkdir = vi.spyOn(fs, "mkdir").mockImplementation((async (...args: Parameters<typeof fs.mkdir>) => {
				await held;
				return realMkdir(...args);
			}) as unknown as typeof fs.mkdir);
			const pending = cache.request(cloudJob);
			await vi.waitFor(() => expect(mkdir).toHaveBeenCalled());
			const clearing = cache.clearCloud();
			release?.();
			await Promise.all([pending, clearing]);
			mkdir.mockRestore();
			expect(await fs.readdir(path.join(directory, "cloud")).catch(() => [])).toHaveLength(0);
		});
	});

	it("prunes the oldest-read files once the cache is over its limit", async () => {
		const store = createArtifactSnapshotStore({ directory, render: async () => Buffer.alloc(10) });
		await fs.mkdir(directory, { recursive: true });
		for (let n = 0; n < 4_010; n++) {
			const file = path.join(directory, `${String(n).padStart(40, "0")}.webp`);
			await fs.writeFile(file, "x");
			await fs.utimes(file, new Date(1_000 + n), new Date(1_000 + n));
		}
		await store.prune();
		const left = await fs.readdir(directory);
		expect(left.length).toBe(4_000);
		expect(left).not.toContain(`${"0".padStart(40, "0")}.webp`);
		expect(left).toContain(`${"4009".padStart(40, "0")}.webp`);
	});
});

describe("registerArtifactSnapshotIpc", () => {
	it("renders only well-formed jobs, and answers whether the snapshot is now cached", async () => {
		const handlers = new Map<string, (event: unknown, value: unknown) => unknown>();
		const store = { request: vi.fn<(request: ArtifactSnapshotJob) => Promise<Buffer | null | typeof SNAPSHOT_DROPPED>>(async () => Buffer.from("b")) };
		registerArtifactSnapshotIpc({ ipcMain: { handle: (channel, handler) => void handlers.set(channel, handler as never) }, store });
		const request = handlers.get("artifactSnapshot:request")!;
		expect(await request({}, { nope: true })).toBe(false);
		// Main refuses another host's artifact whatever the renderer decided.
		expect(await request({}, job({ hostId: "host-2" }))).toBe(false);
		expect(store.request).not.toHaveBeenCalled();
		expect(await request({}, job())).toBe(true);
		store.request.mockResolvedValueOnce(SNAPSHOT_DROPPED).mockResolvedValueOnce(null);
		expect(await request({}, job())).toBe(false);
		expect(await request({}, job())).toBe(false);
	});
});

describe("snapshot URLs", () => {
	it("name the same snapshot as the job they were made from", () => {
		const cloudJob = job({ hostId: "local", cloud: { orgId: "org-1" }, path: "dir/a b&c=?.html", styleTheme: "nord", theme: "light" });
		const parsed = parseArtifactSnapshotUrl(artifactSnapshotUrl(cloudJob));
		expect(parsed).toBeDefined();
		expect(artifactSnapshotKey(parsed!)).toBe(artifactSnapshotKey(cloudJob));
		expect(artifactSnapshotKey(parseArtifactSnapshotUrl(`${artifactSnapshotUrl(job())}&r=1`)!)).toBe(artifactSnapshotKey(job()));
	});

	it("refuse another host's artifact and any other URL", () => {
		expect(parseArtifactSnapshotUrl(artifactSnapshotUrl(job({ hostId: "host-2" })))).toBeUndefined();
		expect(parseArtifactSnapshotUrl(artifactSnapshotUrl(job()).replace("://cache/", "://other/"))).toBeUndefined();
		expect(parseArtifactSnapshotUrl("https://example.com/?s=s1&p=a&u=1&t=dark&y=")).toBeUndefined();
		expect(parseArtifactSnapshotUrl("not a url")).toBeUndefined();
	});
});

describe("registerArtifactSnapshotProtocol", () => {
	it("serves a cached snapshot as a WebP and a missing one as an uncacheable 404", async () => {
		let handler: ((request: Request) => Promise<Response>) | undefined;
		const store = createArtifactSnapshotStore({ directory, render: async () => Buffer.from("webp-bytes") });
		registerArtifactSnapshotProtocol({ protocol: { handle: (_scheme, handle) => void (handler = handle as never) }, store });
		const missing = await handler!(new Request(artifactSnapshotUrl(job())));
		expect(missing.status).toBe(404);
		expect(missing.headers.get("cache-control")).toBe("no-store");
		await store.request(job());
		const found = await handler!(new Request(artifactSnapshotUrl(job())));
		expect(found.status).toBe(200);
		expect(found.headers.get("content-type")).toBe("image/webp");
		expect(Buffer.from(await found.arrayBuffer()).toString()).toBe("webp-bytes");
		expect((await handler!(new Request(artifactSnapshotUrl(job({ hostId: "host-2" }))))).status).toBe(404);
	});
});
