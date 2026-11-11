import { describe, expect, it, vi } from "vitest";
import type { ArtifactSnapshotJob } from "../shared/artifact-snapshot";
import { createArtifactSnapshotRenderer } from "./artifact-snapshot-window";
import { createArtifactSnapshotStore, SNAPSHOT_DROPPED, SnapshotCancelledError } from "./artifact-snapshots";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const job = (name: string): ArtifactSnapshotJob => ({
	sessionId: "s1",
	path: name,
	name,
	kind: "html",
	updatedAt: "2026-11-10T10:00:00Z",
	sessionTitle: "Session",
	theme: "dark",
	styleTheme: "",
});

describe("the snapshot renderer without the app window", () => {
	it("opens no window while the app window is closed, and says why", async () => {
		const created = vi.fn();
		class FakeWindow {
			constructor() {
				created();
			}
		}
		const renderer = createArtifactSnapshotRenderer({ BrowserWindow: FakeWindow as never, pageUrl: () => "app://renderer/artifact-snapshot.html", preload: "preload.js", shellOpen: () => false });
		await expect(renderer.render(job("a.html"))).rejects.toBeInstanceOf(SnapshotCancelledError);
		expect(created).not.toHaveBeenCalled();
	});

	it("opens no window for the requests still queued when the app window closes", async () => {
		const directory = await fs.mkdtemp(path.join(os.tmpdir(), "ao-snapshot-window-"));
		try {
			let shell = true;
			const created = vi.fn();
			// A window whose page never answers, so the first render stays under way while the shell closes.
			class FakeWindow {
				webContents = {
					setWindowOpenHandler: () => undefined,
					on: () => undefined,
					setAudioMuted: () => undefined,
					setFrameRate: () => undefined,
					loadURL: () => new Promise<void>(() => undefined),
				};
				constructor() {
					created();
				}
				once() {}
				isDestroyed() {
					return false;
				}
				destroy() {}
			}
			const renderer = createArtifactSnapshotRenderer({ BrowserWindow: FakeWindow as never, pageUrl: () => "app://renderer/artifact-snapshot.html", preload: "preload.js", shellOpen: () => shell });
			const store = createArtifactSnapshotStore({ directory, render: renderer.render, gapMs: 0 });
			void store.request(job("first.html"));
			await vi.waitFor(() => expect(created).toHaveBeenCalledTimes(1));
			const queued = [1, 2, 3].map((n) => store.request(job(`queued-${n}.html`)));
			// What main does when the shell window closes.
			shell = false;
			store.cancel();
			renderer.close();
			expect(await Promise.all(queued)).toEqual([SNAPSHOT_DROPPED, SNAPSHOT_DROPPED, SNAPSHOT_DROPPED]);
			// Later requests, with the shell still closed, open nothing either.
			expect(await store.request(job("after.html"))).toBeNull();
			expect(created).toHaveBeenCalledTimes(1);
		} finally {
			await fs.rm(directory, { recursive: true, force: true });
		}
	});
});
