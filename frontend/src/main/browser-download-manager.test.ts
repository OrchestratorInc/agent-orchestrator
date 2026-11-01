import { EventEmitter } from "node:events";
import { existsSync, lstatSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createBrowserDownloadManager } from "./browser-download-manager";

const temporaryDirectories: string[] = [];

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

function setup() {
	const root = mkdtempSync(path.join(os.tmpdir(), "ao-browser-downloads-"));
	temporaryDirectories.push(root);
	const downloadsDirectory = path.join(root, "Downloads");
	const historyPath = path.join(root, "data", "browser-downloads.json");
	const notify = vi.fn();
	const openPath = vi.fn(async () => "");
	const showItemInFolder = vi.fn();
	const trashItem = vi.fn(async (filePath: string) => {
		rmSync(filePath);
	});
	let nextId = 0;
	const manager = createBrowserDownloadManager({
		downloadsDirectory,
		historyPath,
		shell: { openPath, showItemInFolder, trashItem },
		notify,
		now: () => 42,
		createId: () => `download-${++nextId}`,
	});
	const session = new EventEmitter();
	const sessionOn = vi.spyOn(session, "on");
	const removeListener = vi.spyOn(session, "removeListener");
	manager.attach(session as never);
	return {
		downloadsDirectory,
		historyPath,
		manager,
		notify,
		openPath,
		removeListener,
		session,
		sessionOn,
		showItemInFolder,
		trashItem,
		start: (item: FakeDownloadItem) => startApproved(manager, session, item),
	};
}

// Requests a download the way Chromium does, then allows it the way the user
// does from the Downloads list. Returns the first (blocked) request's event.
function startApproved(
	manager: ReturnType<typeof createBrowserDownloadManager>,
	session: EventEmitter,
	item: FakeDownloadItem,
) {
	const event = { preventDefault: vi.fn() };
	const webContents = {
		isDestroyed: () => false,
		downloadURL: vi.fn(() => {
			session.emit("will-download", { preventDefault: vi.fn() }, item, webContents);
		}),
	};
	session.emit("will-download", event, item, webContents);
	const blocked = manager.list().downloads.find((download) => download.status === "blocked");
	if (blocked) void manager.action({ id: blocked.id, action: "allow" });
	return { event, webContents };
}

class FakeDownloadItem extends EventEmitter {
	receivedBytes = 0;
	totalBytes = 100;
	paused = false;
	resumable = true;
	setSavePath = vi.fn();
	pause = vi.fn(() => { this.paused = true; });
	resume = vi.fn(() => { this.paused = false; });
	cancel = vi.fn();
	url = "https://downloads.example.test/files/report.txt";
	getFilename = () => "report.txt";
	getURL = () => this.url;
	getURLChain = () => [this.url];
	getReceivedBytes = () => this.receivedBytes;
	getTotalBytes = () => this.totalBytes;
	isPaused = () => this.paused;
	canResume = () => this.resumable;
}

describe("browser download manager", () => {
	it.each(["active", "completed", "cancelled"] as const)("preserves a newer %s download when removing older completed history", async (status) => {
		for (const reload of [false, true]) {
			const test = setup();
			const older = new FakeDownloadItem();
			older.getFilename = () => "a.txt";
			test.start(older);
			const savePath = path.join(test.downloadsDirectory, "a.txt");
			writeFileSync(savePath, "old download");
			older.emit("done", {}, "completed");
			rmSync(savePath);
			const newer = new FakeDownloadItem();
			newer.getFilename = () => "a.txt";
			test.start(newer);
			expect(newer.setSavePath).toHaveBeenCalledWith(savePath);
			writeFileSync(savePath, "new download");
			if (status !== "active") newer.emit("done", {}, status);
			let manager = test.manager;
			if (reload) {
				manager.dispose();
				manager = createBrowserDownloadManager({
					downloadsDirectory: test.downloadsDirectory,
					historyPath: test.historyPath,
					shell: { openPath: test.openPath, showItemInFolder: test.showItemInFolder, trashItem: test.trashItem },
					notify: test.notify,
				});
			}

			await manager.action({ id: "download-1", action: "remove" });

			expect(test.trashItem).not.toHaveBeenCalled();
			expect(readFileSync(savePath, "utf8")).toBe("new download");
			expect(manager.list().downloads).toMatchObject([{ id: "download-2" }]);
			expect(test.notify).toHaveBeenLastCalledWith(manager.list());
			expect(JSON.parse(readFileSync(test.historyPath, "utf8"))).toMatchObject([{ id: "download-2" }]);
		}
	});

	it("does not regain ownership after the newer cancelled history is removed and reloaded", async () => {
		const test = setup();
		const older = new FakeDownloadItem();
		test.start(older);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "old download");
		older.emit("done", {}, "completed");
		rmSync(savePath);
		const newer = new FakeDownloadItem();
		test.start(newer);
		writeFileSync(savePath, "new partial download");
		newer.emit("done", {}, "cancelled");
		await test.manager.action({ id: "download-2", action: "remove" });
		test.manager.dispose();
		const restored = createBrowserDownloadManager({
			downloadsDirectory: test.downloadsDirectory,
			historyPath: test.historyPath,
			shell: { openPath: test.openPath, showItemInFolder: test.showItemInFolder, trashItem: test.trashItem },
			notify: test.notify,
		});

		await restored.action({ id: "download-1", action: "remove" });

		expect(test.trashItem).not.toHaveBeenCalled();
		expect(readFileSync(savePath, "utf8")).toBe("new partial download");
	});

	it("protects paths reused in legacy newest-first history even after the newer row is removed", async () => {
		const test = setup();
		mkdirSync(test.downloadsDirectory, { recursive: true });
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "new partial download");
		mkdirSync(path.dirname(test.historyPath), { recursive: true });
		writeFileSync(test.historyPath, JSON.stringify([
			{ id: "new", fileName: "report.txt", savePath, status: "cancelled", receivedBytes: 10, totalBytes: 100, startedAt: 42, updatedAt: 42 },
			{ id: "old", fileName: "report.txt", savePath, status: "completed", receivedBytes: 100, totalBytes: 100, startedAt: 42, updatedAt: 99 },
		]));
		const restored = createBrowserDownloadManager({
			downloadsDirectory: test.downloadsDirectory,
			historyPath: test.historyPath,
			shell: { openPath: test.openPath, showItemInFolder: test.showItemInFolder, trashItem: test.trashItem },
			notify: test.notify,
		});
		await restored.action({ id: "new", action: "remove" });
		await restored.action({ id: "old", action: "remove" });
		expect(test.trashItem).not.toHaveBeenCalled();
		expect(readFileSync(savePath, "utf8")).toBe("new partial download");
	});

	it("reserves a completed path until asynchronous trash finishes and rejects duplicate removal", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "downloaded");
		item.emit("done", {}, "completed");
		let finish!: () => void;
		test.trashItem.mockImplementationOnce(async () => {
			rmSync(savePath);
			await new Promise<void>((resolve) => { finish = resolve; });
		});
		const removal = test.manager.action({ id: "download-1", action: "remove" });
		try {
			await expect(test.manager.action({ id: "download-1", action: "remove" })).rejects.toThrow();
			const newer = new FakeDownloadItem();
			test.start(newer);
			expect(newer.setSavePath).toHaveBeenCalledWith(path.join(test.downloadsDirectory, "report (1).txt"));
		} finally {
			finish();
			await removal;
		}
		expect(test.trashItem).toHaveBeenCalledOnce();
	});

	it("reports completed missing files as history-only without exposing private paths", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "downloaded");
		item.emit("done", {}, "completed");
		expect(test.manager.list().downloads[0]).toMatchObject({ fileRemovable: true });
		rmSync(savePath);
		expect(test.manager.list().downloads[0]).toMatchObject({ fileRemovable: false });
		expect(JSON.stringify(test.manager.list())).not.toContain(savePath);
		await test.manager.action({ id: "download-1", action: "remove" });
		expect(test.trashItem).not.toHaveBeenCalled();
	});

	it("still deletes the newest owned completed file after reloading shared-path history", async () => {
		const test = setup();
		const older = new FakeDownloadItem();
		test.start(older);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "old download");
		older.emit("done", {}, "completed");
		rmSync(savePath);
		const newer = new FakeDownloadItem();
		test.start(newer);
		writeFileSync(savePath, "new download");
		newer.emit("done", {}, "completed");
		test.manager.dispose();
		const restored = createBrowserDownloadManager({
			downloadsDirectory: test.downloadsDirectory,
			historyPath: test.historyPath,
			shell: { openPath: test.openPath, showItemInFolder: test.showItemInFolder, trashItem: test.trashItem },
			notify: test.notify,
		});
		expect(restored.list().downloads).toMatchObject([{ id: "download-2", fileRemovable: true }, { id: "download-1", fileRemovable: false }]);
		expect(JSON.stringify(restored.list())).not.toContain("superseded");
		await restored.action({ id: "download-2", action: "remove" });
		await restored.action({ id: "download-1", action: "remove" });
		expect(test.trashItem).toHaveBeenCalledExactlyOnceWith(savePath);
		expect(restored.list().downloads).toEqual([]);
	});

	it("releases the trash reservation on failure so removal can be retried", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "downloaded");
		item.emit("done", {}, "completed");
		test.trashItem.mockRejectedValueOnce(new Error("Trash unavailable"));
		await expect(test.manager.action({ id: "download-1", action: "remove" })).rejects.toThrow("Could not delete the downloaded file.");
		await test.manager.action({ id: "download-1", action: "remove" });
		expect(test.trashItem).toHaveBeenCalledTimes(2);
		expect(existsSync(savePath)).toBe(false);
	});

	it("retains the completed-file directory and symlink security checks", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		item.emit("done", {}, "completed");
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		mkdirSync(savePath);
		await expect(test.manager.action({ id: "download-1", action: "remove" })).rejects.toThrow("Could not delete the downloaded file.");
		expect(test.trashItem).not.toHaveBeenCalled();
		expect(test.manager.list().downloads).toHaveLength(1);
		rmSync(savePath, { recursive: true });
		const target = path.join(test.downloadsDirectory, "target.txt");
		writeFileSync(target, "must survive");
		symlinkSync(target, savePath);
		await test.manager.action({ id: "download-1", action: "remove" });
		expect(test.trashItem).toHaveBeenCalledExactlyOnceWith(savePath);
		expect(readFileSync(target, "utf8")).toBe("must survive");
	});

	it("retains deletion ownership for distinct case-sensitive filenames", async () => {
		const test = setup();
		const older = new FakeDownloadItem();
		test.start(older);
		const olderPath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(olderPath, "older owned file");
		older.emit("done", {}, "completed");
		const newer = new FakeDownloadItem();
		newer.getFilename = () => "Report.txt";
		test.start(newer);
		const newerPath = newer.setSavePath.mock.calls[0][0] as string;
		expect(newerPath).not.toBe(olderPath);
		writeFileSync(newerPath, "newer owned file");
		newer.emit("done", {}, "completed");

		await test.manager.action({ id: "download-1", action: "remove" });
		expect(existsSync(olderPath)).toBe(false);
		expect(readFileSync(newerPath, "utf8")).toBe("newer owned file");
		await test.manager.action({ id: "download-2", action: "remove" });
		expect(test.trashItem).toHaveBeenCalledTimes(2);
	});

	it.each([false, true])("preserves a newer completed download when removing cancelled history (reload: %s)", async (reload) => {
		const test = setup();
		const cancelled = new FakeDownloadItem();
		test.start(cancelled);
		await test.manager.action({ id: "download-1", action: "cancel" });
		cancelled.emit("done", {}, "cancelled");
		const completed = new FakeDownloadItem();
		test.start(completed);
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		expect(cancelled.setSavePath).toHaveBeenCalledWith(savePath);
		expect(completed.setSavePath).toHaveBeenCalledWith(savePath);
		writeFileSync(savePath, "new download");
		completed.emit("done", {}, "completed");
		let manager = test.manager;
		if (reload) {
			manager.dispose();
			manager = createBrowserDownloadManager({
				downloadsDirectory: test.downloadsDirectory,
				historyPath: test.historyPath,
				shell: { openPath: test.openPath, showItemInFolder: test.showItemInFolder, trashItem: test.trashItem },
				notify: test.notify,
			});
		}

		await manager.action({ id: "download-1", action: "remove" });

		expect(test.trashItem).not.toHaveBeenCalled();
		expect(readFileSync(savePath, "utf8")).toBe("new download");
		expect(manager.list().downloads).toMatchObject([{ id: "download-2", status: "completed" }]);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))).toMatchObject([{ id: "download-2" }]);
		await manager.action({ id: "download-2", action: "open" });
		expect(test.openPath).toHaveBeenCalledWith(savePath);
	});

	it.each(["cancelled", "interrupted"])("only removes %s history when its path has been manually reused", async (status) => {
		for (const replacement of ["file", "directory", "symlink", "missing"] as const) {
			const test = setup();
			const item = new FakeDownloadItem();
			test.start(item);
			item.emit("done", {}, status);
			const savePath = path.join(test.downloadsDirectory, "report.txt");
			if (replacement === "file") writeFileSync(savePath, "replacement");
			if (replacement === "directory") mkdirSync(savePath);
			if (replacement === "symlink") symlinkSync("missing-target", savePath);
			test.trashItem.mockRejectedValue(new Error("Must not trash a reused path"));

			await test.manager.action({ id: "download-1", action: "remove" });

			expect(test.trashItem).not.toHaveBeenCalled();
			expect(test.manager.list().downloads).toEqual([]);
			expect(JSON.parse(readFileSync(test.historyPath, "utf8"))).toEqual([]);
			if (replacement === "file") expect(readFileSync(savePath, "utf8")).toBe("replacement");
			if (replacement === "directory") expect(lstatSync(savePath).isDirectory()).toBe(true);
			if (replacement === "symlink") expect(lstatSync(savePath).isSymbolicLink()).toBe(true);
		}
	});

	it("contains destination setup failures and reports them without exposing the path", () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "ao-browser-download-failure-"));
		temporaryDirectories.push(root);
		const blockingFile = path.join(root, "not-a-directory");
		writeFileSync(blockingFile, "blocker");
		const notify = vi.fn();
		const manager = createBrowserDownloadManager({
			downloadsDirectory: path.join(blockingFile, "Downloads"),
			historyPath: path.join(root, "data", "browser-downloads.json"),
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify,
		});
		const session = new EventEmitter();
		manager.attach(session as never);
		const item = new FakeDownloadItem();

		expect(() => startApproved(manager, session, item)).not.toThrow();
		expect(item.cancel).toHaveBeenCalledOnce();
		expect(manager.list()).toEqual({
			downloads: [],
			error: "Could not prepare the Downloads folder.",
		});
		expect(notify).toHaveBeenCalledWith(manager.list());
		expect(JSON.stringify(manager.list())).not.toContain(blockingFile);
	});

	it("blocks a download nobody approved without saving anything", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		const event = { preventDefault: vi.fn() };
		const tab = { isDestroyed: () => false, downloadURL: vi.fn() };

		test.session.emit("will-download", event, item, tab);

		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(item.setSavePath).not.toHaveBeenCalled();
		expect(existsSync(test.downloadsDirectory)).toBe(false);
		expect(existsSync(test.historyPath)).toBe(false);
		expect(test.manager.list().downloads).toEqual([expect.objectContaining({
			id: "download-1",
			fileName: "report.txt",
			source: "downloads.example.test",
			status: "blocked",
			active: false,
		})]);
		expect(JSON.stringify(test.manager.list())).not.toContain(item.url);

		// The same link opened again in that tab stays one blocked entry.
		test.session.emit("will-download", event, item, tab);
		expect(test.manager.list().downloads).toHaveLength(1);

		await test.manager.action({ id: "download-1", action: "remove" });
		expect(test.manager.list().downloads).toEqual([]);
		expect(test.trashItem).not.toHaveBeenCalled();
		await expect(test.manager.action({ id: "download-1", action: "allow" })).rejects.toThrow("Download not found");
	});

	it("starts a blocked download only after the user allows it, and only once", () => {
		const test = setup();
		const item = new FakeDownloadItem();

		const { event, webContents } = test.start(item);

		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(webContents.downloadURL).toHaveBeenCalledWith(item.url);
		expect(item.setSavePath).toHaveBeenCalledWith(path.join(test.downloadsDirectory, "report.txt"));
		expect(test.manager.list().downloads).toEqual([expect.objectContaining({
			id: "download-1",
			source: "downloads.example.test",
			status: "progressing",
		})]);

		// The approval does not carry over to a later request for the same URL.
		const repeat = { preventDefault: vi.fn() };
		test.session.emit("will-download", repeat, new FakeDownloadItem(), webContents);
		expect(repeat.preventDefault).toHaveBeenCalledOnce();
		expect(test.manager.list().downloads.map((download) => download.status)).toEqual(["blocked", "progressing"]);
	});

	it("does not let a request that only redirects through an approved URL use the approval", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		const tab = { isDestroyed: () => false, downloadURL: vi.fn() };
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, tab);
		await test.manager.action({ id: "download-1", action: "allow" });

		const other = new FakeDownloadItem();
		other.getURLChain = () => ["https://other.example.test/start", item.url];
		const event = { preventDefault: vi.fn() };
		test.session.emit("will-download", event, other, tab);

		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(other.setSavePath).not.toHaveBeenCalled();

		// The approval is still there for the request it was given to.
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, tab);
		expect(item.setSavePath).toHaveBeenCalledOnce();
	});

	it("keeps an approval for the session and tab that asked for it", async () => {
		const test = setup();
		const otherSession = Object.assign(new EventEmitter(), { downloadURL: vi.fn() });
		test.manager.attach(otherSession as never);
		const tab = { isDestroyed: () => false, downloadURL: vi.fn() };
		const item = new FakeDownloadItem();
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, tab);
		await test.manager.action({ id: "download-1", action: "allow" });
		expect(tab.downloadURL).toHaveBeenCalledWith(item.url);

		// Another profile asks for the same URL while the approved request is
		// still on its way. It must not be saved in its place.
		const intruder = new FakeDownloadItem();
		const intruderEvent = { preventDefault: vi.fn() };
		otherSession.emit("will-download", intruderEvent, intruder, { isDestroyed: () => false, downloadURL: vi.fn() });
		expect(intruderEvent.preventDefault).toHaveBeenCalledOnce();
		expect(intruder.setSavePath).not.toHaveBeenCalled();

		// Neither must another tab of the same profile.
		const sibling = new FakeDownloadItem();
		const siblingEvent = { preventDefault: vi.fn() };
		test.session.emit("will-download", siblingEvent, sibling, { isDestroyed: () => false, downloadURL: vi.fn() });
		expect(siblingEvent.preventDefault).toHaveBeenCalledOnce();
		expect(sibling.setSavePath).not.toHaveBeenCalled();

		const approvedEvent = { preventDefault: vi.fn() };
		test.session.emit("will-download", approvedEvent, item, tab);
		expect(approvedEvent.preventDefault).not.toHaveBeenCalled();
		expect(item.setSavePath).toHaveBeenCalledOnce();
	});

	it("keeps each blocked row tied to the profile that requested its file", async () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "ao-browser-downloads-"));
		temporaryDirectories.push(root);
		let nextId = 0;
		const manager = createBrowserDownloadManager({
			downloadsDirectory: path.join(root, "Downloads"),
			historyPath: path.join(root, "data", "browser-downloads.json"),
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify: vi.fn(),
			createId: () => `download-${nextId++}`,
		});
		const sessionA = new EventEmitter();
		const sessionB = new EventEmitter();
		manager.attach(sessionA as never);
		manager.attach(sessionB as never);
		const tabA = { isDestroyed: () => false, downloadURL: vi.fn() };
		const tabB = { isDestroyed: () => false, downloadURL: vi.fn() };
		const itemA = new FakeDownloadItem();
		itemA.getFilename = () => "account-a.csv";
		const itemB = new FakeDownloadItem();
		itemB.getFilename = () => "account-b.csv";

		sessionA.emit("will-download", { preventDefault: vi.fn() }, itemA, tabA);
		sessionB.emit("will-download", { preventDefault: vi.fn() }, itemB, tabB);

		expect(manager.list().downloads.map((download) => [download.id, download.fileName, download.status])).toEqual([
			["download-1", "account-b.csv", "blocked"],
			["download-0", "account-a.csv", "blocked"],
		]);

		await manager.action({ id: "download-0", action: "allow" });
		expect(tabA.downloadURL).toHaveBeenCalledWith(itemA.url);
		expect(tabB.downloadURL).not.toHaveBeenCalled();

		sessionA.emit("will-download", { preventDefault: vi.fn() }, itemA, tabA);
		expect(manager.list().downloads.map((download) => [download.fileName, download.status])).toEqual([
			["account-a.csv", "progressing"],
			["account-b.csv", "blocked"],
		]);
	});

	it("saves an allowed download under the name the user approved", async () => {
		const test = setup();
		const tab = { isDestroyed: () => false, downloadURL: vi.fn() };
		const item = new FakeDownloadItem();
		item.url = "blob:https://app.example.test/4b6f2c1e-0d7a-4f8e-9c55-1f2a3b4c5d6e";
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, tab);
		expect(test.manager.list().downloads[0]).toMatchObject({ fileName: "report.txt", source: "app.example.test" });
		await test.manager.action({ id: "download-1", action: "allow" });

		// The replay of a blob link reports a bare UUID instead of the name.
		const replay = new FakeDownloadItem();
		replay.url = item.url;
		replay.getFilename = () => "4b6f2c1e-0d7a-4f8e-9c55-1f2a3b4c5d6e";
		test.session.emit("will-download", { preventDefault: vi.fn() }, replay, tab);

		expect(replay.setSavePath).toHaveBeenCalledWith(path.join(test.downloadsDirectory, "report.txt"));
		expect(test.manager.list().downloads[0]).toMatchObject({ fileName: "report.txt", status: "progressing" });
	});

	it("keeps a session-fallback approval inside the session that asked for it", async () => {
		const test = setup();
		const session = Object.assign(test.session, { downloadURL: vi.fn() });
		const otherSession = Object.assign(new EventEmitter(), { downloadURL: vi.fn() });
		test.manager.attach(otherSession as never);
		const item = new FakeDownloadItem();
		session.emit("will-download", { preventDefault: vi.fn() }, item, { isDestroyed: () => true, downloadURL: vi.fn() });
		await test.manager.action({ id: "download-1", action: "allow" });
		expect(session.downloadURL).toHaveBeenCalledWith(item.url);

		const intruder = new FakeDownloadItem();
		const intruderEvent = { preventDefault: vi.fn() };
		otherSession.emit("will-download", intruderEvent, intruder);
		expect(intruderEvent.preventDefault).toHaveBeenCalledOnce();
		expect(intruder.setSavePath).not.toHaveBeenCalled();

		// A live sibling tab in the same session asks for the same URL before
		// the session replay arrives. It must not be saved in its place.
		const sibling = new FakeDownloadItem();
		const siblingEvent = { preventDefault: vi.fn() };
		session.emit("will-download", siblingEvent, sibling, { isDestroyed: () => false, downloadURL: vi.fn() });
		expect(siblingEvent.preventDefault).toHaveBeenCalledOnce();
		expect(sibling.setSavePath).not.toHaveBeenCalled();

		// Electron delivers the session replay with a null WebContents.
		const replayEvent = { preventDefault: vi.fn() };
		session.emit("will-download", replayEvent, item, null);
		expect(replayEvent.preventDefault).not.toHaveBeenCalled();
		expect(item.setSavePath).toHaveBeenCalledOnce();
	});

	it("caps blocked requests without evicting real download history", async () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "ao-browser-downloads-"));
		temporaryDirectories.push(root);
		const downloadsDirectory = path.join(root, "Downloads");
		const historyPath = path.join(root, "data", "browser-downloads.json");
		mkdirSync(path.dirname(historyPath), { recursive: true });
		const history = Array.from({ length: 200 }, (_, index) => ({
			id: `done-${index}`,
			fileName: `done-${index}.txt`,
			savePath: path.join(downloadsDirectory, `done-${index}.txt`),
			receivedBytes: 1,
			totalBytes: 1,
			status: "completed",
			startedAt: 1,
			updatedAt: 1,
		}));
		writeFileSync(historyPath, JSON.stringify(history));
		let nextId = 0;
		const manager = createBrowserDownloadManager({
			downloadsDirectory,
			historyPath,
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify: vi.fn(),
			createId: () => `new-${nextId++}`,
		});
		const session = new EventEmitter();
		manager.attach(session as never);

		const first = new FakeDownloadItem();
		for (let index = 0; index < 250; index += 1) {
			const item = index === 0 ? first : new FakeDownloadItem();
			item.url = `https://downloads.example.test/files/${index}.zip`;
			session.emit("will-download", { preventDefault: vi.fn() }, item, { isDestroyed: () => false, downloadURL: vi.fn() });
		}

		const listed = manager.list().downloads;
		expect(listed.filter((download) => download.status === "blocked").map((download) => download.id))
			.toEqual(Array.from({ length: 20 }, (_, index) => `new-${249 - index}`));
		expect(listed.filter((download) => download.status === "completed")).toHaveLength(200);
		// The oldest blocked request was dropped, so it can no longer be allowed.
		await expect(manager.action({ id: "new-0", action: "allow" })).rejects.toThrow("Download not found");

		// Allowing one persists history: every real entry is still there.
		startApproved(manager, session, new FakeDownloadItem());
		const persisted = JSON.parse(readFileSync(historyPath, "utf8")) as Array<{ id: string; status: string }>;
		expect(persisted.filter((download) => download.status === "completed")).toHaveLength(199);
		expect(persisted.some((download) => download.status === "blocked")).toBe(false);
	});

	it("matches an approved download that was redirected, and expires stale approvals", async () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "ao-browser-downloads-"));
		temporaryDirectories.push(root);
		let now = 1_000;
		const manager = createBrowserDownloadManager({
			downloadsDirectory: path.join(root, "Downloads"),
			historyPath: path.join(root, "data", "browser-downloads.json"),
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify: vi.fn(),
			now: () => now,
			createId: () => "download-1",
		});
		const session = Object.assign(new EventEmitter(), { downloadURL: vi.fn() });
		manager.attach(session as never);
		const item = new FakeDownloadItem();
		item.getURLChain = () => [item.url, "https://cdn.example.test/signed"];
		item.getURL = () => "https://cdn.example.test/signed";

		// The tab that requested it is gone, so the session requests it again.
		session.emit("will-download", { preventDefault: vi.fn() }, item, { isDestroyed: () => true, downloadURL: vi.fn() });
		await manager.action({ id: "download-1", action: "allow" });
		expect(session.downloadURL).toHaveBeenCalledWith(item.url);

		now += 61_000;
		const late = { preventDefault: vi.fn() };
		session.emit("will-download", late, item);
		expect(late.preventDefault).toHaveBeenCalledOnce();
		expect(item.setSavePath).not.toHaveBeenCalled();

		await manager.action({ id: "download-1", action: "allow" });
		session.emit("will-download", { preventDefault: vi.fn() }, item);
		expect(item.setSavePath).toHaveBeenCalledOnce();
		expect(manager.list().downloads[0]).toMatchObject({ id: "download-1", status: "progressing" });
	});

	it("tracks progress, supports controls, and reveals a completed system download", async () => {
		const test = setup();
		mkdirSync(test.downloadsDirectory, { recursive: true });
		writeFileSync(path.join(test.downloadsDirectory, "report.txt"), "existing");
		const item = new FakeDownloadItem();

		test.start(item);
		const savePath = path.join(test.downloadsDirectory, "report (1).txt");
		expect(item.setSavePath).toHaveBeenCalledWith(savePath);
		expect(test.manager.list().downloads[0]).toMatchObject({ id: "download-1", fileName: "report (1).txt", status: "progressing" });

		item.receivedBytes = 25;
		item.emit("updated", {}, "progressing");
		expect(test.manager.list().downloads[0]?.receivedBytes).toBe(25);
		await test.manager.action({ id: "download-1", action: "pause" });
		expect(item.pause).toHaveBeenCalledOnce();
		await test.manager.action({ id: "download-1", action: "resume" });
		expect(item.resume).toHaveBeenCalledOnce();

		item.receivedBytes = 100;
		item.emit("done", {}, "completed");
		writeFileSync(savePath, "downloaded");
		await test.manager.action({ id: "download-1", action: "show" });
		expect(test.showItemInFolder).toHaveBeenCalledWith(savePath);
		await test.manager.action({ id: "download-1", action: "open" });
		expect(test.openPath).toHaveBeenCalledWith(savePath);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))[0]).toMatchObject({ status: "completed", savePath });

		test.manager.clear();
		expect(test.manager.list().downloads).toEqual([]);
	});

	it("moves a downloaded file to the recycle bin before removing its history", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		item.emit("done", {}, "completed");
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "downloaded");

		await test.manager.action({ id: "download-1", action: "remove" });

		expect(test.trashItem).toHaveBeenCalledWith(savePath);
		expect(existsSync(savePath)).toBe(false);
		expect(test.manager.list().downloads).toEqual([]);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))).toEqual([]);
	});

	it("keeps download history when moving the local file to the recycle bin fails", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		item.emit("done", {}, "completed");
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		writeFileSync(savePath, "downloaded");
		test.trashItem.mockRejectedValueOnce(new Error("Recycle bin unavailable"));

		await expect(test.manager.action({ id: "download-1", action: "remove" })).rejects.toThrow("Could not delete the downloaded file.");

		expect(existsSync(savePath)).toBe(true);
		expect(test.manager.list().downloads).toHaveLength(1);
	});

	it("attaches once per Electron session and restores unfinished history as interrupted", () => {
		const test = setup();
		test.manager.attach(test.session as never);
		expect(test.sessionOn).toHaveBeenCalledTimes(1);
		const item = new FakeDownloadItem();
		test.start(item);

		const restored = createBrowserDownloadManager({
			downloadsDirectory: test.downloadsDirectory,
			historyPath: test.historyPath,
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify: vi.fn(),
		});
		expect(restored.list().downloads[0]?.status).toBe("interrupted");
		expect(restored.list().downloads[0]?.resumable).toBe(false);
	});

	it("detaches session listeners before a replacement manager attaches", () => {
		const first = setup();
		first.manager.dispose();
		expect(first.removeListener).toHaveBeenCalledOnce();
		first.notify.mockImplementation(() => {
			throw new Error("destroyed shell WebContents");
		});

		const replacementNotify = vi.fn();
		const replacement = createBrowserDownloadManager({
			downloadsDirectory: first.downloadsDirectory,
			historyPath: first.historyPath,
			shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
			notify: replacementNotify,
			createId: () => "download-2",
		});
		replacement.attach(first.session as never);
		startApproved(replacement, first.session, new FakeDownloadItem());

		expect(first.manager.list().downloads).toEqual([]);
		expect(replacement.list().downloads[0]).toMatchObject({ id: "download-2", status: "progressing" });
		expect(replacementNotify).toHaveBeenCalled();
	});

	it("publishes an empty state on dispose so observers drop stale downloads", () => {
		const test = setup();
		test.start(new FakeDownloadItem());
		test.notify.mockClear();

		test.manager.dispose();

		expect(test.notify).toHaveBeenCalledOnce();
		expect(test.notify).toHaveBeenCalledWith({ downloads: [] });
	});

	it("detaches active item listeners when disposed", () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		expect(item.listenerCount("updated")).toBe(1);
		expect(item.listenerCount("done")).toBe(1);

		test.manager.dispose();

		expect(item.listenerCount("updated")).toBe(0);
		expect(item.listenerCount("done")).toBe(0);
	});

	it("keeps an interrupted item resumable until Electron reports it done", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);

		item.emit("updated", {}, "interrupted");
		expect(test.manager.list().downloads[0]).toMatchObject({ status: "interrupted", active: true, resumable: true });
		await test.manager.action({ id: "download-1", action: "resume" });
		expect(item.resume).toHaveBeenCalledOnce();

		item.emit("done", {}, "interrupted");
		expect(test.manager.list().downloads[0]).toMatchObject({ status: "interrupted", active: false, resumable: false });
	});
});
