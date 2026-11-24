import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { LiveAsks, MAX_UPLOAD_BYTES } from "./browser-live-asks";
import type { BrowserLiveAsk } from "./browser-live-types";

let dir: string;
let sink: { ask: Mock<(ask: BrowserLiveAsk) => void>; askClosed: Mock<() => void>; notice: Mock<(message: string) => void> };

beforeEach(async () => {
	dir = await mkdtemp(path.join(tmpdir(), "ao-live-asks-"));
	sink = { ask: vi.fn<(ask: BrowserLiveAsk) => void>(), askClosed: vi.fn<() => void>(), notice: vi.fn<(message: string) => void>() };
});
afterEach(async () => {
	vi.useRealTimers();
	await rm(dir, { recursive: true, force: true });
});

const b64 = (text: string) => Buffer.from(text).toString("base64");

describe("file choosers", () => {
	it("asks the phone, then gives the page the files it sends", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const setFiles = vi.fn(async (_paths: string[]) => undefined);
		asks.chooserOpened({ setFiles }, { multiple: true, accept: "image/*", folder: false });
		expect(sink.ask).toHaveBeenCalledWith({ kind: "filechooser", multiple: true, accept: "image/*", folder: false });

		await asks.receiveFiles({ action: "chunk", file: 0, name: "a.txt", offset: 0, data: b64("hel") });
		await asks.receiveFiles({ action: "chunk", file: 0, name: "a.txt", offset: 3, data: b64("lo") });
		await asks.receiveFiles({ action: "chunk", file: 1, name: "../b.txt", offset: 0, data: b64("bee") });
		await asks.receiveFiles({ action: "done", count: 2 });

		const [paths] = setFiles.mock.calls[0]!;
		expect(paths.map((p) => path.basename(p))).toEqual(["a.txt", "b.txt"]);
		for (const p of paths) expect(p.startsWith(dir + path.sep)).toBe(true);
		expect(await readFile(paths[0]!, "utf8")).toBe("hello");
		expect(await readFile(paths[1]!, "utf8")).toBe("bee");
	});

	it("leaves the input alone for a cancelled chooser", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const setFiles = vi.fn(async () => undefined);
		asks.chooserOpened({ setFiles }, { multiple: false, accept: "", folder: false });
		await asks.receiveFiles({ action: "cancel" });
		await asks.receiveFiles({ action: "done", count: 0 });
		expect(setFiles).not.toHaveBeenCalled();
	});

	it("refuses files with no chooser open, out-of-order chunks and more than the cap", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		await expect(asks.receiveFiles({ action: "chunk", file: 0, name: "a", offset: 0, data: b64("x") })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });

		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		await expect(asks.receiveFiles({ action: "chunk", file: 0, name: "a", offset: 5, data: b64("x") })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });

		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		const big = Buffer.alloc(32 << 10).toString("base64");
		let offset = 0;
		let refused: unknown;
		try {
			while (offset <= MAX_UPLOAD_BYTES) {
				await asks.receiveFiles({ action: "chunk", file: 0, name: "big", offset, data: big });
				offset += 32 << 10;
			}
		} catch (error) {
			refused = error;
		}
		expect(refused).toMatchObject({ code: "UPLOAD_TOO_LARGE" });
		expect(sink.notice).toHaveBeenCalledWith(expect.stringContaining("too large"));
	});

	it("drops a chooser's unfinished files when a newer chooser opens", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const first = vi.fn(async () => undefined);
		const second = vi.fn(async () => undefined);
		asks.chooserOpened({ setFiles: first }, { multiple: false, accept: "", folder: false });
		await asks.receiveFiles({ action: "chunk", file: 0, name: "old", offset: 0, data: b64("o") });
		asks.chooserOpened({ setFiles: second }, { multiple: false, accept: "", folder: false });
		await asks.receiveFiles({ action: "chunk", file: 0, name: "new", offset: 0, data: b64("n") });
		await asks.receiveFiles({ action: "done", count: 1 });
		expect(first).not.toHaveBeenCalled();
		expect((second.mock.calls[0] as unknown as [string[]])[0].map((p) => path.basename(p))).toEqual(["new"]);
	});

	it("removes its files when it is disposed", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		await asks.receiveFiles({ action: "chunk", file: 0, name: "a", offset: 0, data: b64("a") });
		await asks.dispose();
		expect(await readdir(dir)).toEqual([]);
	});
});

describe("sign-ins", () => {
	it("asks the phone and answers the page with what it sends", () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const answer = vi.fn();
		asks.authRequired({ origin: "https://intranet.example", realm: "Staff" }, answer);
		expect(sink.ask).toHaveBeenCalledWith({ kind: "auth", origin: "https://intranet.example", realm: "Staff", failed: false });
		asks.answerAuth({ origin: "https://intranet.example", username: "ada", password: "pw" });
		expect(answer).toHaveBeenCalledWith("ada", "pw");
	});

	it("says a second request for the same origin follows a failed sign-in", () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		asks.authRequired({ origin: "https://a.example" }, vi.fn());
		asks.answerAuth({ origin: "https://a.example", username: "u", password: "wrong" });
		asks.authRequired({ origin: "https://a.example" }, vi.fn());
		expect(sink.ask).toHaveBeenLastCalledWith({ kind: "auth", origin: "https://a.example", failed: true });
	});

	it("cancels on the phone's cancel, an answer for another origin, a timeout and disposal", async () => {
		vi.useFakeTimers();
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const cancelled = vi.fn();
		asks.authRequired({ origin: "https://a.example" }, cancelled);
		asks.answerAuth({ origin: "https://other.example", username: "u", password: "p" });
		expect(cancelled).not.toHaveBeenCalled();
		asks.answerAuth({ origin: "https://a.example", cancel: true });
		expect(cancelled).toHaveBeenCalledWith();

		const timedOut = vi.fn();
		asks.authRequired({ origin: "https://b.example" }, timedOut);
		vi.advanceTimersByTime(120_000);
		expect(timedOut).toHaveBeenCalledWith();
		expect(sink.askClosed).toHaveBeenCalled();

		const disposed = vi.fn();
		asks.authRequired({ origin: "https://c.example" }, disposed);
		await asks.dispose();
		expect(disposed).toHaveBeenCalledWith();
	});
});
