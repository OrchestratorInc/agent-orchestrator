// @vitest-environment node
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import { collectPageFiles, isSharePage, isShareVideo, MAX_PAGE_FILES, pageFileReferences, servedPath } from "./cloud-share-page-files";

describe("pageFileReferences", () => {
	it("finds what the page's markup and styles load", () => {
		const html = `<!doctype html>
<link rel="icon" href="favicon.png"><link rel="stylesheet" href="site.css">
<style>body { background: url("bg.jpg") } .a { background-image: url(img/a.webp) }</style>
<img src="photos/team.jpg" srcset="photos/team-2x.jpg 2x, photos/team-3x.jpg 3x" alt="">
<picture><source srcset='wide.avif 1200w'><img src=narrow.png></picture>
<video src="clip.mp4" poster="clip-poster.png"><source src="clip.webm"><track src="subs.vtt"></video>
<audio src="sound.mp3"></audio>
<svg><image href="icon.svg"/><use xlink:href="sprite.svg#x"/></svg>
<table background="old.gif"><td style="background: url('cell.png')">x</td></table>
<img src="a&amp;b.png">`;
		const urls = (references: ReturnType<typeof pageFileReferences>) => references.map((reference) => reference.url);
		expect(urls(pageFileReferences(html))).toEqual([
			"bg.jpg", "img/a.webp", "favicon.png", "photos/team.jpg", "photos/team-2x.jpg", "photos/team-3x.jpg",
			"wide.avif", "narrow.png", "clip.mp4", "clip-poster.png", "clip.webm", "subs.vtt", "sound.mp3",
			"icon.svg", "sprite.svg#x", "old.gif", "cell.png", "a&b.png",
		]);
		// Only the <use> is marked: a sandboxed page cannot load another file's symbol.
		expect(pageFileReferences(html).filter((reference) => reference.svgUse).map((reference) => reference.url)).toEqual(["sprite.svg#x"]);
	});

	it("skips comments, scripts and a stylesheet link", () => {
		const html = `<!-- <img src="hidden.png"> --><script>const s = '<img src="script.png">';</script><link rel="stylesheet" href="a.css"><img src="shown.png">`;
		expect(pageFileReferences(html)).toEqual([{ url: "shown.png" }]);
	});
});

describe("servedPath", () => {
	it("serves a relative reference where the browser asks for it", () => {
		expect(servedPath("images/a.png")).toEqual({ path: "images/a.png" });
		expect(servedPath("./images/../b.png?v=2#top")).toEqual({ path: "b.png" });
		expect(servedPath("my%20photo.jpg")).toEqual({ path: "my photo.jpg" });
		expect(servedPath("sprite.svg#icon")).toEqual({ path: "sprite.svg" });
	});

	it("ignores what loads no file of the page", () => {
		for (const reference of ["", "  ", "#x", "https://cdn.test/a.png", "data:image/png;base64,AA", "//cdn.test/a.png", "blob:x", "mailto:a@b.c"]) {
			expect(servedPath(reference), reference).toBeNull();
		}
	});

	it("skips what leaves the page's folder or the share host keeps", () => {
		for (const reference of ["../a.png", "a/../../b.png", "/abs.png", "\\\\abs.png"]) {
			expect(servedPath(reference), reference).toEqual({ skip: "outside" });
		}
		for (const reference of ["raw", "_ao/x.png", "~vt/x.png", "a%2F..%2F..%2Fb", "%E0%A4%A.png", "a%5Cb.png", "a%00.png"]) {
			expect(servedPath(reference), reference).toEqual({ skip: "unservable" });
		}
	});
});

describe("page and video names", () => {
	it("tells a page and a video by extension", () => {
		expect([isSharePage("a.html"), isSharePage("A.HTM"), isSharePage("a.svg")]).toEqual([true, true, false]);
		expect([isShareVideo("a.MP4"), isShareVideo("x/a.webm"), isShareVideo("a.png")]).toEqual([true, true, false]);
	});
});

describe("collectPageFiles", () => {
	let dataDir: string;
	let artifacts: string;

	beforeEach(async () => {
		dataDir = await fs.mkdtemp(path.join(os.tmpdir(), "ao-page-files-"));
		artifacts = path.join(dataDir, "artifacts", "sess-1");
		await fs.mkdir(path.join(artifacts, "docs", "images"), { recursive: true });
		await fs.writeFile(path.join(artifacts, "docs", "images", "a.png"), "a");
		await fs.writeFile(path.join(artifacts, "docs", "b.jpg"), "bb");
		await fs.writeFile(path.join(artifacts, "docs", "clip.mp4"), "video");
		await fs.writeFile(path.join(artifacts, "top.png"), "top");
		await fs.writeFile(path.join(dataDir, "secret.png"), "secret");
		await fs.symlink(path.join(dataDir, "secret.png"), path.join(artifacts, "docs", "link.png"));
	});

	afterEach(async () => {
		await fs.rm(dataDir, { recursive: true, force: true });
	});

	it("includes the files beside the page once each and says what it leaves out", async () => {
		const html = `<img src="images/a.png"><img src="./images/a.png"><img src="b.jpg"><img src="report.html">
<video src="clip.mp4" poster="b.jpg"></video><img src="../top.png"><img src="missing.png"><img src="link.png">
<img src="https://cdn.test/x.png"><img src="_ao/x.png">`;
		const { files, skipped } = await collectPageFiles(dataDir, "sess-1", "docs/report.html", html);
		expect(files.map((file) => [file.path, file.file.relativePath, file.file.size])).toEqual([
			["images/a.png", "docs/images/a.png", 1],
			["b.jpg", "docs/b.jpg", 2],
		]);
		expect(skipped).toEqual([
			{ reference: "clip.mp4", reason: "video" },
			{ reference: "../top.png", reason: "outside" },
			{ reference: "missing.png", reason: "missing", relativePath: "docs/missing.png" },
			{ reference: "link.png", reason: "outside" },
			{ reference: "_ao/x.png", reason: "unservable" },
		]);
	});

	it("leaves out a file a symlink in the page's folder leads out of it to", async () => {
		await fs.symlink(path.join(artifacts, "top.png"), path.join(artifacts, "docs", "up.png"));
		await fs.symlink(path.join(artifacts, "docs", "b.jpg"), path.join(artifacts, "docs", "images", "same.jpg"));
		const { files, skipped } = await collectPageFiles(dataDir, "sess-1", "docs/report.html", `<img src="up.png"><img src="images/same.jpg">`);
		expect(files.map((file) => [file.path, file.file.relativePath])).toEqual([["images/same.jpg", "docs/b.jpg"]]);
		expect(skipped).toEqual([{ reference: "up.png", reason: "outside" }]);
	});

	it("leaves out another file's SVG symbol, and keeps the page's own", async () => {
		await fs.writeFile(path.join(artifacts, "docs", "sprite.svg"), "<svg/>");
		const html = `<svg><use href="sprite.svg#mark"/><use xlink:href="sprite.svg#other"/><use href="#local"/></svg><img src="sprite.svg">`;
		const { files, skipped } = await collectPageFiles(dataDir, "sess-1", "docs/report.html", html);
		// The <img> still loads it as an image.
		expect(files.map((file) => file.path)).toEqual(["sprite.svg"]);
		expect(skipped).toEqual([{ reference: "sprite.svg#mark", reason: "svg_use" }]);
	});

	it("resolves a page at the top of the artifacts folder", async () => {
		const { files } = await collectPageFiles(dataDir, "sess-1", "page.html", `<img src="top.png"><img src="docs/b.jpg">`);
		expect(files.map((file) => file.file.relativePath)).toEqual(["top.png", "docs/b.jpg"]);
	});

	it("stops at the most files a link serves", async () => {
		const many = path.join(artifacts, "many");
		await fs.mkdir(many);
		for (let index = 0; index <= MAX_PAGE_FILES; index++) await fs.writeFile(path.join(many, `${index}.png`), "x");
		const html = Array.from({ length: MAX_PAGE_FILES + 1 }, (_, index) => `<img src="many/${index}.png">`).join("");
		const { files, skipped } = await collectPageFiles(dataDir, "sess-1", "page.html", html);
		expect(files).toHaveLength(MAX_PAGE_FILES);
		expect(skipped).toEqual([{ reference: `many/${MAX_PAGE_FILES}.png`, reason: "limit" }]);
	});
});
