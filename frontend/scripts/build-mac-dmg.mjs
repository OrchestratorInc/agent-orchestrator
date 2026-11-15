#!/usr/bin/env node
import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { lstat, mkdtemp, readFile, readdir, readlink, realpath, rm, rmdir, stat, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const frontendDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");

export const APP_NAME = "Agent Orchestrator";
export const APP_BUNDLE_NAME = `${APP_NAME}.app`;
export const VOLUME_NAME = "Orchestrator";

export const DMG_LAYOUT = Object.freeze({
	window: Object.freeze({ width: 660, height: 400 }),
	iconSize: 128,
	iconTextSize: 12,
	app: Object.freeze({ x: 170, y: 184 }),
	applications: Object.freeze({ x: 490, y: 184 }),
});

export const BACKGROUND_IMAGE = path.join(frontendDir, "assets/dmg/background.png");
export const VOLUME_ICON = path.join(frontendDir, "assets/icon.icns");

export function macDmgLayout() {
	return {
		title: VOLUME_NAME,
		background: BACKGROUND_IMAGE,
		icon: VOLUME_ICON,
		iconSize: DMG_LAYOUT.iconSize,
		iconTextSize: DMG_LAYOUT.iconTextSize,
		contents: [
			{ x: DMG_LAYOUT.app.x, y: DMG_LAYOUT.app.y, type: "file" },
			{ x: DMG_LAYOUT.applications.x, y: DMG_LAYOUT.applications.y, type: "link", path: "/Applications" },
		],
	};
}

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

function utf16be(text) {
	const buf = Buffer.alloc(text.length * 2);
	for (let i = 0; i < text.length; i++) buf.writeUInt16BE(text.charCodeAt(i), i * 2);
	return buf;
}

function findRecord(store, filename, code, type) {
	const name = Buffer.alloc(4);
	name.writeUInt32BE(filename.length);
	const key = Buffer.concat([name, utf16be(filename), Buffer.from(code + type, "latin1")]);
	const at = store.indexOf(key);
	return at < 0 ? -1 : at + key.length;
}

export function readDsStoreLayout(store, names) {
	const positions = {};
	for (const name of names) {
		const at = findRecord(store, name, "Iloc", "blob");
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

export async function validateMountedLayout(volume) {
	const problems = [];
	const entries = new Set(await readdir(volume));
	if (!entries.has(APP_BUNDLE_NAME)) problems.push(`missing ${APP_BUNDLE_NAME}`);
	if (!entries.has("Applications")) problems.push("missing Applications link");
	else if ((await readlink(path.join(volume, "Applications")).catch(() => null)) !== "/Applications") {
		problems.push("Applications is not a symlink to /Applications");
	}
	if (!entries.has(".VolumeIcon.icns")) problems.push("missing .VolumeIcon.icns");
	const image = /\.(tiff?|png)$/i;
	const backgrounds = [
		...[...entries].filter((f) => f.startsWith(".background.") && image.test(f)),
		...(entries.has(".background")
			? (await readdir(path.join(volume, ".background"))).filter((f) => image.test(f)).map((f) => `.background/${f}`)
			: []),
	];
	if (backgrounds.length !== 1) problems.push(`expected one background image, found ${backgrounds.length}`);
	else if (/\.tiff?$/i.test(backgrounds[0])) {
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

export async function checkMacDmgLayout(dmg) {
	const dmgPath = path.resolve(dmg);
	if (!(await stat(dmgPath).catch(() => null))?.isFile()) throw new Error(`${dmgPath} is not a file`);
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
			await checkMacDmgLayout(output);
			await unlink(`${output}.blockmap`).catch(() => undefined);
			console.log(output);
		}
	} catch (error) {
		console.error(error instanceof Error ? error.message : error);
		process.exit(1);
	}
}
