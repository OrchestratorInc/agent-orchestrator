import { describe, expect, it, vi } from "vitest";
vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));
import { copySharedArtifact, copyFolderName, MAX_SHARED_COPY_BYTES, MAX_SHARED_COPY_FILE_BYTES, safeName, validCopyPath } from "./shared-artifact-copy";
import { copySharedArtifactResult } from "./shared-artifact-ipc";

const PAGE = "https://link-1.share.example/raw?vt=token";

function host(files: Record<string, string | { status: number }>, options: { tokenRequired?: boolean } = {}) {
	return vi.fn(async (raw: string) => {
		const url = new URL(raw);
		if (options.tokenRequired && url.searchParams.get("vt") !== "token") return new Response("no", { status: 403 });
		const file = files[url.pathname];
		if (file === undefined) return new Response("missing", { status: 404 });
		if (typeof file !== "string") return new Response("", { status: file.status });
		return new Response(file, { status: 200 });
	});
}

function decode(data: string): string {
	return Buffer.from(data, "base64").toString("utf8");
}

describe("copySharedArtifact", () => {
	it("copies a single file into its own folder under shared/", async () => {
		const fetcher = host({ "/raw": "# notes" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "Meeting notes.md" });
		expect(copy.dir).toBe("shared/Meeting notes");
		expect(copy.entry).toBe("shared/Meeting notes/Meeting notes.md");
		expect(copy.files).toHaveLength(1);
		expect(decode(copy.files[0].data)).toBe("# notes");
		expect(copy.missing).toEqual([]);
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it("copies the files a page names by relative path, beside it, and leaves external ones alone", async () => {
		const html = '<img src="images/logo.png"><img src="https://cdn.example/x.png"><div style="background:url(bg.jpg)"></div><a href="other.html">x</a>';
		const fetcher = host({ "/raw": html, "/images/logo.png": "PNG", "/bg.jpg": "JPG" }, { tokenRequired: true });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "roadmap.html" });
		expect(copy.files.map((file) => file.path).sort()).toEqual([
			"shared/roadmap/bg.jpg",
			"shared/roadmap/images/logo.png",
			"shared/roadmap/roadmap.html",
		]);
		expect(copy.missing).toEqual([]);
		expect(fetcher.mock.calls.every(([url]) => new URL(url).origin === "https://link-1.share.example")).toBe(true);
	});

	it("falls back to asking without the token, and reports a file the link does not serve", async () => {
		const fetcher = host({ "/raw": '<img src="a.png"><img src="gone.png">', "/a.png": "A" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.files.map((file) => file.path)).toContain("shared/page/a.png");
		expect(copy.missing).toEqual(["gone.png"]);
	});

	it("gives the same fingerprint for the same bytes and a new one when they change", async () => {
		const first = await copySharedArtifact(host({ "/raw": "v1" }), { contentUrl: PAGE, name: "a.md" });
		const again = await copySharedArtifact(host({ "/raw": "v1" }), { contentUrl: PAGE, name: "a.md" });
		const changed = await copySharedArtifact(host({ "/raw": "v2" }), { contentUrl: PAGE, name: "a.md" });
		expect(again.fingerprint).toBe(first.fingerprint);
		expect(changed.fingerprint).not.toBe(first.fingerprint);
	});

	it("refuses a URL that is not https, an unreachable link, and a file over the limit", async () => {
		expect(await copySharedArtifactResult(host({}), { contentUrl: "file:///etc/passwd", name: "a" })).toMatchObject({ ok: false, code: "invalid" });
		expect(await copySharedArtifactResult(host({ "/raw": { status: 404 } }), { contentUrl: PAGE, name: "a" })).toMatchObject({ ok: false, code: "unavailable" });
		const big = vi.fn(async () => new Response("x", { status: 200, headers: { "content-length": String(MAX_SHARED_COPY_BYTES + 1) } }));
		expect(await copySharedArtifactResult(big, { contentUrl: PAGE, name: "a" })).toMatchObject({ ok: false, code: "too_large" });
	});
});

describe("copy limits and updates", () => {
	it("lists a page file over the per-file limit as missing, and refuses an entry over it", async () => {
		const big = String(MAX_SHARED_COPY_FILE_BYTES + 1);
		const fetcher = vi.fn(async (raw: string) => new URL(raw).pathname === "/raw"
			? new Response('<img src="huge.bin"><img src="ok.png">', { status: 200 })
			: new URL(raw).pathname === "/huge.bin" ? new Response("x", { status: 200, headers: { "content-length": big } }) : new Response("ok", { status: 200 }));
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.missing).toEqual(["huge.bin"]);
		expect(copy.files.map((file) => file.path)).toContain("shared/page/ok.png");
		const bigEntry = vi.fn(async () => new Response("x", { status: 200, headers: { "content-length": big } }));
		expect(await copySharedArtifactResult(bigEntry, { contentUrl: PAGE, name: "a.pdf" })).toMatchObject({ ok: false, code: "too_large" });
	});

	it("writes an update into the first copy's folder, and a rename alone is no change", async () => {
		const first = await copySharedArtifact(host({ "/raw": "same" }), { contentUrl: PAGE, name: "Report.md" });
		const renamed = await copySharedArtifact(host({ "/raw": "same" }), { contentUrl: PAGE, name: "Report v2.md", into: { dir: first.dir, entry: first.entry } });
		expect(renamed.entry).toBe("shared/Report/Report.md");
		expect(renamed.fingerprint).toBe(first.fingerprint);
		await expect(copySharedArtifact(host({ "/raw": "x" }), { contentUrl: PAGE, name: "a", into: { dir: "shared/a", entry: "../x" } })).rejects.toThrow();
	});

	it("reports, rather than drops, a page file that differs from another only in case", async () => {
		const fetcher = host({ "/raw": '<img src="Logo.png"><img src="logo.png">', "/Logo.png": "A", "/logo.png": "B" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.missing).toEqual(["logo.png"]);
	});
});

describe("names and places", () => {
	it("keeps a long non-English name within the bytes every file system allows", async () => {
		const name = `${"报".repeat(80)}.html`;
		const copy = await copySharedArtifact(host({ "/raw": "<p>x</p>" }), { contentUrl: PAGE, name });
		for (const part of copy.entry.split("/")) expect(Buffer.byteLength(part)).toBeLessThanOrEqual(120);
		expect(copy.entry.endsWith(".html")).toBe(true);
		expect(validCopyPath(copy.entry, "linux")).toBe(true);
	});

	it("never names a copy after something the daemon leaves out", async () => {
		const copy = await copySharedArtifact(host({ "/raw": "x" }), { contentUrl: PAGE, name: "node_modules" });
		expect(copy.dir).toBe("shared/artifact");
	});

	it("only updates into a folder under shared/", async () => {
		await expect(copySharedArtifact(host({ "/raw": "x" }), { contentUrl: PAGE, name: "a.md", into: { dir: "notes", entry: "notes/a.md" } })).rejects.toThrow();
	});

	it("keeps a page file's name as the page spells it", async () => {
		const decomposed = "café.png";
		const fetcher = vi.fn(async (raw: string) => new URL(raw).pathname === "/raw"
			? new Response(`<img src="${encodeURI(decomposed)}">`, { status: 200 })
			: new Response("img", { status: 200 }));
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.files.map((file) => file.path)).toContain(`shared/page/${decomposed}`);
	});

	it("reports a page file whose name is also another's folder", async () => {
		const fetcher = host({ "/raw": '<img src="a"><img src="a/b.png">', "/a": "A", "/a/b.png": "B" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.missing).toEqual(["a/b.png"]);
	});

	it("never requests a redirect to another site, and follows one on the same site", async () => {
		const moved = vi.fn(async (_url: string, _init?: { redirect?: string }) => new Response(null, { status: 302, headers: { location: "https://elsewhere.example/raw" } }));
		expect(await copySharedArtifactResult(moved, { contentUrl: PAGE, name: "a.md" })).toMatchObject({ ok: false, code: "unavailable" });
		expect(moved).toHaveBeenCalledTimes(1);
		expect(moved.mock.calls[0]?.[1]).toEqual({ redirect: "manual" });
		const sameSite = vi.fn(async (raw: string) => new URL(raw).pathname === "/raw"
			? new Response(null, { status: 307, headers: { location: "/v2/raw" } })
			: new Response("moved here", { status: 200 }));
		const copy = await copySharedArtifact(sameSite, { contentUrl: PAGE, name: "a.md" });
		expect(decode(copy.files[0].data)).toBe("moved here");
	});

	it("stops reading a reply with no length once it passes the limit", async () => {
		let pulled = 0;
		const endless = new ReadableStream<Uint8Array>({
			pull(controller) {
				pulled++;
				controller.enqueue(new Uint8Array(8 * 1024 * 1024));
			},
		});
		const fetcher = vi.fn(async () => new Response(endless, { status: 200 }));
		expect(await copySharedArtifactResult(fetcher, { contentUrl: PAGE, name: "a.bin" })).toMatchObject({ ok: false, code: "too_large" });
		expect(pulled).toBeLessThan(10);
	});

	it("treats names the daemon folds together as one, as Go lower-cases them", async () => {
		const fetcher = host({ "/raw": '<img src="%C4%B0.png"><img src="i.png">', "/%C4%B0.png": "A", "/i.png": "B" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.missing).toEqual(["i.png"]);
	});

	it("folds Greek sigma as Go does, one letter at a time", async () => {
		const fetcher = host({ "/raw": '<img src="%CE%91%CE%A3"><img src="%CE%B1%CF%83">', "/%CE%91%CE%A3": "A", "/%CE%B1%CF%83": "B" });
		const copy = await copySharedArtifact(fetcher, { contentUrl: PAGE, name: "page.html" });
		expect(copy.missing).toEqual(["%CE%B1%CF%83"]);
	});

	it("counts a page file the link does not serve in the fingerprint", async () => {
		const served = await copySharedArtifact(host({ "/raw": '<img src="a.png">', "/a.png": "A" }), { contentUrl: PAGE, name: "p.html" });
		const missing = await copySharedArtifact(host({ "/raw": '<img src="a.png">' }), { contentUrl: PAGE, name: "p.html" });
		const missingAgain = await copySharedArtifact(host({ "/raw": '<img src="a.png">' }), { contentUrl: PAGE, name: "p.html" });
		expect(missing.fingerprint).not.toBe(served.fingerprint);
		expect(missingAgain.fingerprint).toBe(missing.fingerprint);
	});

	it("takes a local http link only when allowed", async () => {
		const local = { contentUrl: "http://127.0.0.1:9/raw", name: "a.md" };
		expect(await copySharedArtifactResult(host({ "/raw": "x" }), local)).toMatchObject({ ok: false, code: "invalid" });
		expect(await copySharedArtifactResult(host({ "/raw": "x" }), local, { allowLocal: true })).toMatchObject({ ok: true });
	});
});

describe("validCopyPath", () => {
	it("refuses what the daemon refuses on every system", () => {
		for (const bad of ["shared/p/node_modules/a.js", "shared/p/.hidden", "shared/p/Thumbs.db", "shared/p/" + "a".repeat(201)]) {
			expect(validCopyPath(bad, "linux")).toBe(false);
		}
		expect(validCopyPath("shared/p/img/chart-10:00.png", "darwin")).toBe(true);
	});

	it("refuses names Windows cannot hold, only on Windows", () => {
		for (const bad of ["shared/p/dir /x.png", "shared/p/x."]) {
			// Windows drops a trailing dot or space, so the daemon refuses it everywhere.
			expect(validCopyPath(bad, "win32")).toBe(false);
			expect(validCopyPath(bad, "linux")).toBe(false);
		}
		for (const bad of ["shared/p/chart-10:00.png", "shared/p/a*b.png", "shared/p/q?.png", "shared/p/CON.png", "shared/p/lpt1", "shared/p/con .png", "shared/p/nul .txt"]) {
			expect(validCopyPath(bad, "win32")).toBe(false);
			expect(validCopyPath(bad, "linux")).toBe(true);
		}
	});
});

describe("safe names", () => {
	it("keeps names readable and safe on every file system", () => {
		expect(copyFolderName("artifacts-roadmap.html")).toBe("artifacts-roadmap");
		expect(safeName("a/b\\c:d?.html", "x")).toBe("a-b-c-d-.html");
		expect(safeName("..hidden", "x")).toBe("hidden");
		expect(safeName("CON", "fallback")).toBe("fallback");
		expect(safeName("", "fallback")).toBe("fallback");
		// Cut at 120 bytes, never leaving a dot at the end.
		expect(safeName(`${"a".repeat(119)}.b`, "x")).toBe("a".repeat(119));
	});
});
