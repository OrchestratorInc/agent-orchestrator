import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
	APP_BUNDLE_NAME,
	BACKGROUND_IMAGE,
	DMG_LAYOUT,
	buildMacDmg,
	macDmgLayout,
	readDsStoreLayout,
	validateMountedLayout,
} from "./build-mac-dmg.mjs";

// Platform-independent coverage. The end-to-end proof (a real dmg built,
// mounted and checked) runs on the macOS runner in frontend.yml's
// mac-dmg-layout job, and every `npm run make` on macOS checks its dmg in
// forge.config.ts's postMake.

const svg = readFileSync(path.join(path.dirname(BACKGROUND_IMAGE), "background.svg"), "utf8");

function pngSize(file) {
	const png = readFileSync(file);
	// IHDR is always the first chunk: width and height at bytes 16 and 20.
	return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

describe("install window layout", () => {
	it("is a compact window with large icons, app on the left and Applications on the right, aligned", () => {
		expect(DMG_LAYOUT.window).toEqual({ width: 660, height: 400 });
		expect(DMG_LAYOUT.iconSize).toBe(128);
		expect(DMG_LAYOUT.app.x).toBeLessThan(DMG_LAYOUT.applications.x);
		expect(DMG_LAYOUT.app.y).toBe(DMG_LAYOUT.applications.y);
		// Symmetric about the window's centre line.
		expect(DMG_LAYOUT.app.x + DMG_LAYOUT.applications.x).toBe(DMG_LAYOUT.window.width);
	});

	it("passes the layout to electron-builder", () => {
		expect(macDmgLayout()).toMatchObject({
			title: "${productName}",
			background: BACKGROUND_IMAGE,
			iconSize: DMG_LAYOUT.iconSize,
			contents: [
				{ x: DMG_LAYOUT.app.x, y: DMG_LAYOUT.app.y, type: "file" },
				{ x: DMG_LAYOUT.applications.x, y: DMG_LAYOUT.applications.y, type: "link", path: "/Applications" },
			],
		});
	});

	it("ships the background at exactly 1x and 2x of the window size", () => {
		const { width, height } = DMG_LAYOUT.window;
		expect(svg).toContain(`width="${width}" height="${height}"`);
		expect(pngSize(BACKGROUND_IMAGE)).toEqual({ width, height });
		expect(pngSize(BACKGROUND_IMAGE.replace(/\.png$/, "@2x.png"))).toEqual({ width: width * 2, height: height * 2 });
	});

	// The art is drawn against the icon coordinates; moving one without the
	// other leaves the arrow and label plates pointing at empty space.
	it("draws the label plates under each icon and the arrow between them", () => {
		const plates = [...svg.matchAll(/<rect x="(\d+)" y="(\d+)" width="(\d+)" height="\d+" rx="\d+" fill="#62788c"/g)].map(
			([, x, y, w]) => ({ centre: Number(x) + Number(w) / 2, top: Number(y) }),
		);
		expect(plates.map((p) => p.centre)).toEqual([DMG_LAYOUT.app.x, DMG_LAYOUT.applications.x]);
		for (const plate of plates) expect(plate.top).toBeGreaterThan(DMG_LAYOUT.app.y + DMG_LAYOUT.iconSize / 2);
		const arrow = /<path d="M(\d+) (\d+) H(\d+)"/.exec(svg);
		expect(Number(arrow[2])).toBe(DMG_LAYOUT.app.y);
		expect(Number(arrow[1])).toBeGreaterThan(DMG_LAYOUT.app.x + DMG_LAYOUT.iconSize / 2);
		expect(Number(arrow[3])).toBeLessThan(DMG_LAYOUT.applications.x - DMG_LAYOUT.iconSize / 2);
		expect(svg).toContain(">Drag to Applications</text>");
	});
});

function utf16be(text) {
	const buf = Buffer.alloc(text.length * 2);
	for (let i = 0; i < text.length; i++) buf.writeUInt16BE(text.charCodeAt(i), i * 2);
	return buf;
}

function record(filename, code, type, payload) {
	const len = Buffer.alloc(4);
	len.writeUInt32BE(filename.length);
	return Buffer.concat([len, utf16be(filename), Buffer.from(code + type, "latin1"), payload]);
}

function iloc(x, y) {
	const blob = Buffer.alloc(20);
	blob.writeUInt32BE(16, 0);
	blob.writeUInt32BE(x, 4);
	blob.writeUInt32BE(y, 8);
	blob.writeUInt32BE(0xffffffff, 12);
	return blob;
}

function blob(bytes) {
	const len = Buffer.alloc(4);
	len.writeUInt32BE(bytes.length);
	return Buffer.concat([len, bytes]);
}

describe("readDsStoreLayout", () => {
	it("reads icon positions and the root window/icon-view settings", () => {
		const store = Buffer.concat([
			Buffer.alloc(32, 0xaa),
			record(".", "bwsp", "blob", blob(Buffer.from("bwsp-plist"))),
			record(".", "icvp", "blob", blob(Buffer.from("icvp-plist"))),
			record(APP_BUNDLE_NAME, "Iloc", "blob", iloc(170, 190)),
			record("Applications", "Iloc", "blob", iloc(490, 190)),
			Buffer.alloc(32, 0xbb),
		]);
		const layout = readDsStoreLayout(store, [APP_BUNDLE_NAME, "Applications"]);
		expect(layout.positions).toEqual({
			[APP_BUNDLE_NAME]: { x: 170, y: 190 },
			Applications: { x: 490, y: 190 },
		});
		expect(layout.bwsp?.toString()).toBe("bwsp-plist");
		expect(layout.icvp?.toString()).toBe("icvp-plist");
	});

	it("reports nothing for a store without layout records", () => {
		const layout = readDsStoreLayout(Buffer.alloc(64), [APP_BUNDLE_NAME]);
		expect(layout).toEqual({ positions: {}, bwsp: null, icvp: null });
	});
});

describe("validateMountedLayout", () => {
	let volume;
	beforeEach(() => {
		volume = mkdtempSync(path.join(tmpdir(), "ao-dmg-volume-"));
	});
	afterEach(() => rmSync(volume, { recursive: true, force: true }));

	// What `hdiutil create -srcfolder` produced for every stable release up to
	// v0.14.7: the app and the link, and nothing that tells Finder how to draw.
	it("rejects a bare image with only the app and the Applications link", async () => {
		mkdirSync(path.join(volume, APP_BUNDLE_NAME));
		symlinkSync("/Applications", path.join(volume, "Applications"));
		expect(await validateMountedLayout(volume)).toEqual([
			"missing .VolumeIcon.icns",
			"expected one background image, found 0",
			"missing .DS_Store (Finder will show the default window)",
		]);
	});

	it("rejects a volume without the app or with a broken Applications link", async () => {
		writeFileSync(path.join(volume, "Applications"), "");
		const problems = await validateMountedLayout(volume);
		expect(problems).toContain(`missing ${APP_BUNDLE_NAME}`);
		expect(problems).toContain("Applications is not a symlink to /Applications");
	});

	it("rejects ambiguous backgrounds", async () => {
		mkdirSync(path.join(volume, ".background"));
		writeFileSync(path.join(volume, ".background", "a.png"), "");
		writeFileSync(path.join(volume, ".background.png"), "");
		expect(await validateMountedLayout(volume)).toContain("expected one background image, found 2");
	});
});

describe("buildMacDmg", () => {
	let work;
	let app;
	const buildForge = vi.fn();
	beforeEach(() => {
		work = mkdtempSync(path.join(tmpdir(), "ao-dmg-build-"));
		app = path.join(work, APP_BUNDLE_NAME);
		mkdirSync(app);
		buildForge.mockReset();
	});
	afterEach(() => rmSync(work, { recursive: true, force: true }));

	it("wraps the given .app without signing it, the dmg, or writing a blockmap", async () => {
		const output = path.join(work, "out", "agent-orchestrator-darwin-arm64.dmg");
		buildForge.mockResolvedValue([output]);
		expect(await buildMacDmg({ app, arch: "arm64", output }, { buildForge })).toBe(output);

		const [forge, options] = buildForge.mock.calls[0];
		expect(forge).toEqual({ dir: app });
		expect(options.mac).toEqual(["dmg:arm64"]);
		expect(options.config.publish).toBeNull();
		expect(options.config.mac.identity).toBeNull();
		expect(options.config.productName).toBe("Agent Orchestrator");
		expect(options.config.directories.output).toBe(path.dirname(output));
		expect(options.config.dmg).toMatchObject({
			...macDmgLayout(),
			sign: false,
			writeUpdateInfo: false,
			// The conductor's published asset name, unchanged.
			artifactName: "agent-orchestrator-darwin-arm64.dmg",
		});
	});

	it("fails when electron-builder does not produce the requested file", async () => {
		buildForge.mockResolvedValue(["/elsewhere.dmg"]);
		await expect(buildMacDmg({ app, arch: "x64", output: path.join(work, "a.dmg") }, { buildForge })).rejects.toThrow(
			/did not produce/,
		);
	});

	it.each([
		[{ app: "", arch: "arm64", output: "x.dmg" }, /required/],
		[{ app: "/tmp/Other.app", arch: "arm64", output: "x.dmg" }, /must be a Agent Orchestrator.app bundle/],
		[{ arch: "universal", output: "x.dmg" }, /unsupported --arch/],
		[{ arch: "arm64", output: "x.zip" }, /must end in .dmg/],
	])("rejects bad arguments %#", async (args, error) => {
		await expect(buildMacDmg({ app, ...args }, { buildForge })).rejects.toThrow(error);
		expect(buildForge).not.toHaveBeenCalled();
	});

	it("refuses to overwrite an existing output", async () => {
		const output = path.join(work, "exists.dmg");
		writeFileSync(output, "");
		await expect(buildMacDmg({ app, arch: "arm64", output }, { buildForge })).rejects.toThrow(/already exists/);
	});
});
