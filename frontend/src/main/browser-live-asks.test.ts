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
/** The chooser id the last ask carried, which every response must name. */
const chooserId = () => (sink.ask.mock.lastCall![0] as { id: string }).id;

describe("file choosers", () => {
	it("asks the phone, then gives the page the files it sends", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const setFiles = vi.fn(async (_paths: string[]) => undefined);
		asks.chooserOpened({ setFiles }, { multiple: true, accept: "image/*", folder: false });
		expect(sink.ask).toHaveBeenCalledWith({ kind: "filechooser", id: expect.stringMatching(/^[0-9a-f]{16}$/), multiple: true, accept: "image/*", folder: false });
		const id = chooserId();

		await asks.receiveFiles({ action: "chunk", chooser: id, file: 0, name: "a.txt", offset: 0, data: b64("hel") });
		await asks.receiveFiles({ action: "chunk", chooser: id, file: 0, name: "a.txt", offset: 3, data: b64("lo") });
		await asks.receiveFiles({ action: "chunk", chooser: id, file: 1, name: "../b.txt", offset: 0, data: b64("bee") });
		await asks.receiveFiles({ action: "done", chooser: id, count: 2 });

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
		await asks.receiveFiles({ action: "cancel", chooser: chooserId() });
		await asks.receiveFiles({ action: "done", chooser: chooserId(), count: 0 });
		expect(setFiles).not.toHaveBeenCalled();
	});

	it("refuses files with no chooser open, out-of-order chunks and more than the cap", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		await expect(asks.receiveFiles({ action: "chunk", chooser: "x", file: 0, name: "a", offset: 0, data: b64("x") })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });

		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		await expect(asks.receiveFiles({ action: "chunk", chooser: chooserId(), file: 0, name: "a", offset: 5, data: b64("x") })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });

		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		const big = Buffer.alloc(32 << 10).toString("base64");
		let offset = 0;
		let refused: unknown;
		try {
			while (offset <= MAX_UPLOAD_BYTES) {
				await asks.receiveFiles({ action: "chunk", chooser: chooserId(), file: 0, name: "big", offset, data: big });
				offset += 32 << 10;
			}
		} catch (error) {
			refused = error;
		}
		expect(refused).toMatchObject({ code: "UPLOAD_TOO_LARGE" });
		expect(sink.notice).toHaveBeenCalledWith(expect.stringContaining("too large"));
	});

	it("never gives a newer chooser the files picked for an older one", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const first = vi.fn(async () => undefined);
		const second = vi.fn(async () => undefined);
		asks.chooserOpened({ setFiles: first }, { multiple: false, accept: "", folder: false });
		const older = chooserId();
		// The phone is still reading A's file when chooser B opens.
		asks.chooserOpened({ setFiles: second }, { multiple: false, accept: "", folder: false });
		const newer = chooserId();
		expect(newer).not.toBe(older);
		await asks.receiveFiles({ action: "chunk", chooser: older, file: 0, name: "old", offset: 0, data: b64("o") });
		await asks.receiveFiles({ action: "done", chooser: older, count: 1 });
		await asks.receiveFiles({ action: "cancel", chooser: older });
		expect(first).not.toHaveBeenCalled();
		expect(second).not.toHaveBeenCalled();
		await asks.receiveFiles({ action: "chunk", chooser: newer, file: 0, name: "new", offset: 0, data: b64("n") });
		await asks.receiveFiles({ action: "done", chooser: newer, count: 1 });
		expect((second.mock.calls[0] as unknown as [string[]])[0].map((p) => path.basename(p))).toEqual(["new"]);
	});

	it("keeps every file, whatever names they share", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		const setFiles = vi.fn(async (_paths: string[]) => undefined);
		asks.chooserOpened({ setFiles }, { multiple: true, accept: "", folder: false });
		const id = chooserId();
		const names = ["2-a.txt", "a.txt", "a.txt"];
		for (const [file, name] of names.entries()) await asks.receiveFiles({ action: "chunk", chooser: id, file, name, offset: 0, data: b64(`file ${file}`) });
		await asks.receiveFiles({ action: "done", chooser: id, count: 3 });
		const [paths] = setFiles.mock.calls[0]!;
		expect(new Set(paths).size).toBe(3);
		expect(paths.map((p) => path.basename(p))).toEqual(names);
		expect(await Promise.all(paths.map((p) => readFile(p, "utf8")))).toEqual(["file 0", "file 1", "file 2"]);
	});

	it("removes its files when it is disposed", async () => {
		const asks = new LiveAsks({ uploadDir: dir }, sink);
		asks.chooserOpened({ setFiles: vi.fn(async () => undefined) }, { multiple: false, accept: "", folder: false });
		await asks.receiveFiles({ action: "chunk", chooser: chooserId(), file: 0, name: "a", offset: 0, data: b64("a") });
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
