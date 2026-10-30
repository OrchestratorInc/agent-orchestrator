#!/usr/bin/env node
// The macOS install window: one layout definition, a builder that wraps an
// existing .app in a dmg with that layout, and a check that a finished dmg
// really carries it.
//
//   node scripts/build-mac-dmg.mjs --app <path/Agent Orchestrator.app> --arch <arm64|x64> --output <file.dmg>
//   node scripts/build-mac-dmg.mjs --check <file.dmg>
//
// Why this exists: the published stable dmg is not made by
// makers/maker-dmg.ts. The public build-artifacts workflow ships only the
// unsigned .zip; the private release conductor signs, notarizes and staples the
// app and then wraps it with a bare `hdiutil create -srcfolder`. hdiutil copies
// a folder verbatim, so the image had no .DS_Store and no .background, and
// Finder opened it as a large plain window with two small icons in a corner.
// The dmg has to be built after the app is stapled (a read-only image cannot be
// re-signed), so the conductor needs a builder it can call on its stapled app;
// --app is that builder, and --check is the gate that keeps the layout from
// silently disappearing again. MakerDMG (local `npm run make`) uses the same
// macDmgLayout() so both paths produce the same window.
//
// The layout is written by electron-builder's dmg target (app-builder-lib's
// bundled dmgbuild), which hdiutil-creates the image, mounts it, writes the
// .DS_Store records Finder reads and copies the background into .background/.
// It needs no Finder, AppleScript or GUI session, so it works on headless CI.
import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { lstat, mkdtemp, readFile, readdir, readlink, realpath, rm, rmdir, stat, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const frontendDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");

// The bundle keeps its current name: `ao start`, the updater, stale-copy cleanup
// and the repair installer look it up by this exact name.
export const APP_NAME = "Agent Orchestrator";
export const APP_BUNDLE_NAME = `${APP_NAME}.app`;
// The mounted volume: Finder's window title and sidebar entry. Nothing looks
// it up by name.
export const VOLUME_NAME = "Orchestrator";

// Window and icon geometry in Finder points. Positions are icon centres. The
// window is the window FRAME (Finder's title bar takes ~32pt of it), so the
// icons sit in the middle of the ~368pt that is actually visible. The
// background art (assets/dmg/background.svg) places the drag cue between these
// exact coordinates and keeps its harness tiles clear of them, so change both
// together (build-mac-dmg.test.mjs checks the pairing).
export const DMG_LAYOUT = Object.freeze({
	window: Object.freeze({ width: 660, height: 400 }),
	iconSize: 128,
	iconTextSize: 12,
	app: Object.freeze({ x: 170, y: 184 }),
	applications: Object.freeze({ x: 490, y: 184 }),
});

// The 1x file. electron-builder finds background@2x.png beside it and merges the
// pair into one HiDPI TIFF, so Retina displays get the sharp version.
export const BACKGROUND_IMAGE = path.join(frontendDir, "assets/dmg/background.png");
// Becomes .VolumeIcon.icns: the mounted volume shows the app icon in the window
// title bar, Finder's sidebar and on the desktop instead of a generic disk.
export const VOLUME_ICON = path.join(frontendDir, "assets/icon.icns");

/**
 * electron-builder `dmg` options for the install window. Spread into the dmg
 * config by both MakerDMG and buildMacDmg; signing-related options stay with
 * each caller.
 */
export function macDmgLayout() {
	return {
		// Volume name. Without this electron-builder names the volume
		// "<productName> <version>-<arch>".
		title: VOLUME_NAME,
		background: BACKGROUND_IMAGE,
		icon: VOLUME_ICON,
		iconSize: DMG_LAYOUT.iconSize,
		iconTextSize: DMG_LAYOUT.iconTextSize,
		// The window frame takes the background image's size (660x400 points);
		// electron-builder ignores `window.width/height` when a background is set.
		contents: [
			// No `path`: electron-builder substitutes the .app it was given.
			{ x: DMG_LAYOUT.app.x, y: DMG_LAYOUT.app.y, type: "file" },
			{ x: DMG_LAYOUT.applications.x, y: DMG_LAYOUT.applications.y, type: "link", path: "/Applications" },
		],
	};
}

/**
 * buildMacDmg wraps an existing, already signed/notarized/stapled .app in a dmg
 * with the install window layout, without touching the bundle. The dmg itself
 * is left unsigned: the caller signs, notarizes and staples it afterwards, the
 * same contract as MakerDMG + sealDmg in forge.config.ts.
 */
export async function buildMacDmg({ app, arch, output }, { buildForge } = {}) {
	if (!app || !arch || !output) throw new Error("--app, --arch and --output are required");
	if (!["arm64", "x64"].includes(arch)) throw new Error(`unsupported --arch ${arch} (expected arm64 or x64)`);
	const appPath = path.resolve(app);
	const outputPath = path.resolve(output);
	if (path.basename(appPath) !== APP_BUNDLE_NAME) throw new Error(`--app must be a ${APP_BUNDLE_NAME} bundle`);
	const appStat = await lstat(appPath).catch(() => null);
	if (!appStat?.isDirectory()) throw new Error(`${appPath} is not a directory`);
	if (!outputPath.endsWith(".dmg")) throw new Error("--output must end in .dmg");
	if (existsSync(outputPath)) throw new Error(`${outputPath} already exists`);

	buildForge ??= (await import("app-builder-lib")).buildForge;
	// Same electron-builder settings as MakerDMG (see makers/maker-dmg.ts for
	// the reasoning behind each): `dir` is the .app itself, electron-builder
	// never signs the app (identity: null) or the dmg (sign: false), writes no
	// blockmap sidecar and publishes nothing.
	const artifacts = await buildForge(
		{ dir: appPath },
		{
			mac: [`dmg:${arch}`],
			projectDir: frontendDir,
			config: {
				appId: "dev.agent-orchestrator.desktop",
				productName: APP_NAME,
				directories: { output: path.dirname(outputPath) },
				publish: null,
				mac: { identity: null },
				dmg: {
					...macDmgLayout(),
					sign: false,
					writeUpdateInfo: false,
					artifactName: path.basename(outputPath),
				},
			},
		},
	);
	if (!artifacts.includes(outputPath)) {
		throw new Error(`electron-builder did not produce ${outputPath} (got ${artifacts.join(", ") || "nothing"})`);
	}
	return outputPath;
}

// ---- layout check -----------------------------------------------------------

function utf16be(text) {
	const buf = Buffer.alloc(text.length * 2);
	for (let i = 0; i < text.length; i++) buf.writeUInt16BE(text.charCodeAt(i), i * 2);
	return buf;
}

// A .DS_Store record is: filename length (u32 BE, in UTF-16 units), the
// filename in UTF-16BE, a four-char record code and a four-char type. Records
// never span B-tree nodes, so a byte search finds them whole.
function findRecord(store, filename, code, type) {
	const name = Buffer.alloc(4);
	name.writeUInt32BE(filename.length);
	const key = Buffer.concat([name, utf16be(filename), Buffer.from(code + type, "latin1")]);
	const at = store.indexOf(key);
	return at < 0 ? -1 : at + key.length;
}

/**
 * readDsStoreLayout extracts the records Finder uses to draw the window from a
 * .DS_Store buffer: each item's icon position (Iloc), and the volume root's
 * window settings (bwsp) and icon-view settings (icvp) as raw binary plists.
 */
export function readDsStoreLayout(store, names) {
	const positions = {};
	for (const name of names) {
		const at = findRecord(store, name, "Iloc", "blob");
		// blob length (16), then x and y as u32 BE.
		if (at >= 0 && at + 12 <= store.length) positions[name] = { x: store.readUInt32BE(at + 4), y: store.readUInt32BE(at + 8) };
	}
	const blob = (code) => {
		const at = findRecord(store, ".", code, "blob");
		if (at < 0 || at + 4 > store.length) return null;
		const length = store.readUInt32BE(at);
		return store.subarray(at + 4, at + 4 + length);
	};
	return { positions, bwsp: blob("bwsp"), icvp: blob("icvp") };
}

async function plistToXml(buf) {
	const dir = await mkdtemp(path.join(tmpdir(), "ao-dmg-plist-"));
	try {
		const file = path.join(dir, "blob.plist");
		await writeFile(file, buf);
		// plutil's json converter rejects <data> values (icvp's backgroundImageAlias),
		// so read the XML form and pick out the scalar keys.
		const { stdout } = await run("plutil", ["-convert", "xml1", "-o", "-", file]);
		return stdout;
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
}

function plistValue(xml, key) {
	const match = new RegExp(`<key>${key}</key>\\s*<(integer|real|string|true|false)\\s*/?>([^<]*)`).exec(xml);
	if (!match) return undefined;
	if (match[1] === "true" || match[1] === "false") return match[1] === "true";
	return match[1] === "string" ? match[2] : Number(match[2]);
}

/**
 * validateMountedLayout asserts the layout on a mounted volume and returns the
 * list of problems (empty when the window is right).
 */
export async function validateMountedLayout(volume) {
	const problems = [];
	const entries = new Set(await readdir(volume));
	if (!entries.has(APP_BUNDLE_NAME)) problems.push(`missing ${APP_BUNDLE_NAME}`);
	if (!entries.has("Applications")) problems.push("missing Applications link");
	else if ((await readlink(path.join(volume, "Applications")).catch(() => null)) !== "/Applications") {
		problems.push("Applications is not a symlink to /Applications");
	}
	if (!entries.has(".VolumeIcon.icns")) problems.push("missing .VolumeIcon.icns");
	// dmgbuild stores the picture as a hidden .background.<ext> at the volume
	// root; other dmg tools use a .background/ folder. Accept either, once.
	const image = /\.(tiff?|png)$/i;
	const backgrounds = [
		...[...entries].filter((f) => f.startsWith(".background.") && image.test(f)),
		...(entries.has(".background")
			? (await readdir(path.join(volume, ".background"))).filter((f) => image.test(f)).map((f) => `.background/${f}`)
			: []),
	];
	if (backgrounds.length !== 1) problems.push(`expected one background image, found ${backgrounds.length}`);
	else if (/\.tiff?$/i.test(backgrounds[0])) {
		// The @2x art merged in: without it Retina displays upscale the 1x image.
		const { stdout } = await run("tiffutil", ["-info", path.join(volume, backgrounds[0])]);
		const want = `Image Width: ${DMG_LAYOUT.window.width * 2} Image Length: ${DMG_LAYOUT.window.height * 2}`;
		if (!stdout.includes(want)) problems.push(`background has no ${DMG_LAYOUT.window.width * 2}x${DMG_LAYOUT.window.height * 2} Retina representation`);
	}
	if (!entries.has(".DS_Store")) {
		problems.push("missing .DS_Store (Finder will show the default window)");
		return problems;
	}

	const store = await readFile(path.join(volume, ".DS_Store"));
	const { positions, bwsp, icvp } = readDsStoreLayout(store, [APP_BUNDLE_NAME, "Applications"]);
	for (const [name, want] of [
		[APP_BUNDLE_NAME, DMG_LAYOUT.app],
		["Applications", DMG_LAYOUT.applications],
	]) {
		const got = positions[name];
		if (!got || got.x !== want.x || got.y !== want.y) {
			problems.push(`${name} icon at ${got ? `${got.x},${got.y}` : "no position"}, expected ${want.x},${want.y}`);
		}
	}
	if (!icvp) problems.push("no icon view settings (icvp) in .DS_Store");
	else {
		const xml = await plistToXml(icvp);
		// backgroundType 2 = picture.
		if (plistValue(xml, "backgroundType") !== 2) problems.push("icon view has no background picture");
		const iconSize = plistValue(xml, "iconSize");
		if (iconSize !== DMG_LAYOUT.iconSize) problems.push(`icon size ${iconSize}, expected ${DMG_LAYOUT.iconSize}`);
	}
	if (!bwsp) problems.push("no window settings (bwsp) in .DS_Store");
	else {
		const bounds = plistValue(await plistToXml(bwsp), "WindowBounds") ?? "";
		const size = /\{\{[-\d.]+, [-\d.]+\}, \{([\d.]+), ([\d.]+)\}\}/.exec(bounds);
		if (!size || Number(size[1]) !== DMG_LAYOUT.window.width || Number(size[2]) !== DMG_LAYOUT.window.height) {
			problems.push(`window bounds ${bounds || "missing"}, expected ${DMG_LAYOUT.window.width}x${DMG_LAYOUT.window.height}`);
		}
	}
	return problems;
}

/**
 * checkMacDmgLayout mounts a dmg read-only and hidden from Finder, asserts the
 * install window layout, and always detaches. Throws listing every problem.
 */
export async function checkMacDmgLayout(dmg) {
	const dmgPath = path.resolve(dmg);
	if (!(await stat(dmgPath).catch(() => null))?.isFile()) throw new Error(`${dmgPath} is not a file`);
	// realpath: hdiutil reports /private/var/..., not the /var/... tmpdir() gives.
	const mountRoot = await realpath(await mkdtemp(path.join(tmpdir(), "ao-dmg-check-")));
	const { stdout } = await run("hdiutil", [
		"attach",
		"-readonly",
		"-nobrowse",
		"-noautoopen",
		"-noverify",
		"-mountroot",
		mountRoot,
		dmgPath,
	]);
	// Detach by the whole-image device (first /dev/diskN line), so a mount this
	// code fails to locate is still never left attached.
	const device = /^(\/dev\/disk\d+)\s/m.exec(stdout)?.[1];
	const volume = stdout
		.split("\n")
		.map((line) => line.split("\t").at(-1)?.trim())
		.find((mount) => mount?.startsWith(`${mountRoot}/`));
	try {
		if (!volume) throw new Error(`could not find the mounted volume for ${dmgPath}:\n${stdout}`);
		const problems = await validateMountedLayout(volume);
		if (problems.length) {
			throw new Error(`${dmgPath} has no usable install window layout:\n  - ${problems.join("\n  - ")}`);
		}
		console.log(`[dmg] layout ok: ${dmgPath}`);
	} finally {
		const target = device ?? volume;
		if (target) {
			await run("hdiutil", ["detach", target, "-quiet"]).catch(() => run("hdiutil", ["detach", target, "-force", "-quiet"]));
		}
		await rmdir(mountRoot).catch(() => undefined);
	}
}

function parseArgs(argv) {
	const args = {};
	for (let i = 0; i < argv.length; i++) {
		const flag = argv[i];
		if (!["--app", "--arch", "--output", "--check"].includes(flag) || argv[i + 1] === undefined) {
			throw new Error(`usage: build-mac-dmg.mjs --app <.app> --arch <arm64|x64> --output <.dmg> | --check <.dmg>`);
		}
		args[flag.slice(2)] = argv[++i];
	}
	return args;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	try {
		const args = parseArgs(process.argv.slice(2));
		if (args.check) await checkMacDmgLayout(args.check);
		else {
			const output = await buildMacDmg(args);
			// Fail here, not at the user's first open, if the layout did not land.
			await checkMacDmgLayout(output);
			// Keep only the dmg: no blockmap sidecar (writeUpdateInfo is off; this
			// covers a future default change).
			await unlink(`${output}.blockmap`).catch(() => undefined);
			console.log(output);
		}
	} catch (error) {
		console.error(error instanceof Error ? error.message : error);
		process.exit(1);
	}
}
