import { describe, expect, it, vi } from "vitest";
import type { BrowserDialog } from "../browserTransport";
import { answerAuth, answerDialog, answerSelect, askTitle, downloadLabel, pickFiles, pickerFor, saveDownload } from "./browserAsk";

const select: Extract<BrowserDialog, { kind: "select" }> = {
	kind: "select", id: "s1", ctx: "S:1", selected: 1,
	options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }],
};

describe("answers", () => {
	it("answers a page dialog, with the prompt's text", () => {
		expect(answerDialog(true)).toEqual({ type: "dialog", payload: { accept: true } });
		expect(answerDialog(false)).toEqual({ type: "dialog", payload: { accept: false } });
		expect(answerDialog(true, "Ada")).toEqual({ type: "dialog", payload: { accept: true, promptText: "Ada" } });
	});

	it("answers a sign-in, or cancels it", () => {
		const auth = { kind: "auth", origin: "https://a.example", failed: false } as const;
		expect(answerAuth(auth, { username: "ada", password: "pw" })).toEqual({ type: "auth", payload: { origin: "https://a.example", username: "ada", password: "pw" } });
		expect(answerAuth(auth, null)).toEqual({ type: "auth", payload: { origin: "https://a.example", cancel: true } });
	});

	it("answers a select with the option picked, never a disabled or missing one", () => {
		expect(answerSelect(select, 2)).toEqual({ type: "select", payload: { id: "s1", index: 2, ctx: "S:1" } });
		expect(answerSelect(select, 1)).toBeNull();
		expect(answerSelect(select, 7)).toBeNull();
		expect(answerSelect(select, -1)).toBeNull();
	});

	it("titles each ask in the page's words, as plain text", () => {
		expect(askTitle({ kind: "beforeunload", message: "" })).toBe("Leave this page?");
		expect(askTitle({ kind: "auth", origin: "https://intranet.example", failed: false })).toBe("Sign in to intranet.example");
		expect(askTitle({ kind: "auth", origin: "not a url", failed: true })).toBe("Sign in to not a url");
		expect(askTitle({ kind: "filechooser", multiple: true, accept: "", folder: false })).toBe("The page asks for files");
	});

	it("labels a download with its size", () => {
		expect(downloadLabel({ id: "1", name: "a.pdf", size: 2_500_000 })).toBe("a.pdf · 2.4 MB");
		expect(downloadLabel({ id: "1", name: "b.txt", size: 900 })).toBe("b.txt · 900 B");
		expect(downloadLabel({ id: "1", name: "c", size: 0 })).toBe("c");
	});
});

describe("file pickers", () => {
	it("offers Photos only for an input that takes images or video", () => {
		expect(pickerFor("")).toEqual({ photos: true, mediaTypes: ["images", "videos"], documentTypes: "*/*" });
		expect(pickerFor("image/*")).toEqual({ photos: true, mediaTypes: ["images"], documentTypes: ["image/*"] });
		expect(pickerFor("image/png,.jpg")).toEqual({ photos: true, mediaTypes: ["images"], documentTypes: ["image/png", "image/jpeg"] });
		expect(pickerFor("application/pdf")).toEqual({ photos: false, mediaTypes: [], documentTypes: ["application/pdf"] });
		expect(pickerFor(".csv, text/plain")).toEqual({ photos: false, mediaTypes: [], documentTypes: ["text/csv", "text/plain"] });
		expect(pickerFor("video/mp4")).toEqual({ photos: true, mediaTypes: ["videos"], documentTypes: ["video/mp4"] });
	});

	it("picks from Photos, one or several as the input takes", async () => {
		const launchImageLibraryAsync = vi.fn(async () => ({ canceled: false, assets: [{ uri: "file:///p.heic", fileName: "p.heic", mimeType: "image/heic" }, { uri: "file:///q.jpg", fileName: null, mimeType: undefined }] }));
		const files = await pickFiles("photos", { kind: "filechooser", multiple: true, accept: "image/*", folder: false }, { launchImageLibraryAsync, getDocumentAsync: vi.fn() });
		expect(launchImageLibraryAsync).toHaveBeenCalledWith(expect.objectContaining({ mediaTypes: ["images"], allowsMultipleSelection: true }));
		expect(files).toEqual([{ uri: "file:///p.heic", name: "p.heic", type: "image/heic" }, { uri: "file:///q.jpg", name: "q.jpg" }]);
	});

	it("picks from Files, and returns nothing when cancelled", async () => {
		const getDocumentAsync = vi.fn(async () => ({ canceled: true, assets: null }));
		const files = await pickFiles("files", { kind: "filechooser", multiple: false, accept: ".pdf", folder: false }, { launchImageLibraryAsync: vi.fn(), getDocumentAsync });
		expect(getDocumentAsync).toHaveBeenCalledWith({ multiple: false, copyToCacheDirectory: true, type: ["application/pdf"] });
		expect(files).toEqual([]);
	});
});

describe("saving a download", () => {
	it("fetches it with the sign-in, then hands it to the phone", async () => {
		const download = vi.fn(async () => ({ uri: "file:///cache/a.pdf" }));
		const keep = vi.fn(async () => undefined);
		await saveDownload({ id: "g1", name: "a.pdf", size: 1 }, async () => ({ url: "https://x/download/g1", headers: { Authorization: "Bearer t" } }), { download, keep });
		expect(download).toHaveBeenCalledWith("https://x/download/g1", { Authorization: "Bearer t" }, "a.pdf");
		expect(keep).toHaveBeenCalledWith("file:///cache/a.pdf", "a.pdf");
	});

	it("never saves under a name that leaves the folder", async () => {
		const download = vi.fn(async () => ({ uri: "file:///cache/x" }));
		await saveDownload({ id: "g1", name: "../../etc/passwd", size: 1 }, async () => ({ url: "u", headers: {} }), { download, keep: vi.fn(async () => undefined) });
		expect(download).toHaveBeenCalledWith("u", {}, "passwd");
	});
});
