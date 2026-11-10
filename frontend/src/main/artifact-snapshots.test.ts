import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtifactSnapshotJob } from "../shared/artifact-snapshot";
import { artifactSnapshotKey, createArtifactSnapshotStore, registerArtifactSnapshotIpc, SNAPSHOT_DROPPED, SNAPSHOT_MAX_QUEUED, SnapshotUnavailableError } from "./artifact-snapshots";

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

	it("renders cards on screen before the newest request, and tells a dropped request apart from a failure", async () => {
		const order: string[] = [];
		const gates: Array<() => void> = [];
		const render = vi.fn((request: ArtifactSnapshotJob) => new Promise<Buffer>((resolve) => {
			order.push(request.path);
			gates.push(() => resolve(Buffer.from("x")));
		}));
		const store = createArtifactSnapshotStore({ directory, render, gapMs: 0 });
		const first = store.request(job({ path: "first.html" }));
		await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(1));
		const below = store.request(job({ path: "below.html" }), 0);
		const newestBelow = store.request(job({ path: "newest-below.html" }), 0);
		const onScreen = store.request(job({ path: "on-screen.html" }), 1);
		gates.shift()?.();
		await first;
		await vi.waitFor(() => expect(render).toHaveBeenCalledTimes(2));
		expect(order[1]).toBe("on-screen.html");
		// A queue over its limit drops its lowest-priority, oldest request, and says so.
		const fillers = Array.from({ length: SNAPSHOT_MAX_QUEUED + 1 }, (_, n) => store.request(job({ path: `fill-${n}.html` }), 0));
		expect(await below).toBe(SNAPSHOT_DROPPED);
		gates.splice(0).forEach((open) => open());
		void [newestBelow, onScreen, fillers];
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
	it("answers only well-formed jobs", async () => {
		const handlers = new Map<string, (event: unknown, value: unknown) => unknown>();
		const store = { get: vi.fn(async () => Buffer.from("a")), request: vi.fn(async () => Buffer.from("b")) };
		registerArtifactSnapshotIpc({ ipcMain: { handle: (channel, handler) => void handlers.set(channel, handler as never) }, store });
		expect(await handlers.get("artifactSnapshot:get")?.({}, { nope: true })).toBeNull();
		// Main refuses another host's artifact whatever the renderer decided.
		expect(await handlers.get("artifactSnapshot:request")?.({}, job({ hostId: "host-2" }))).toBeNull();
		expect(store.request).not.toHaveBeenCalled();
		expect(store.get).not.toHaveBeenCalled();
		const bytes = (await handlers.get("artifactSnapshot:request")?.({}, job())) as Uint8Array;
		expect(Buffer.from(bytes).toString()).toBe("b");
	});
});
