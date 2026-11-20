#!/usr/bin/env node
// `npm run device`: put this checkout's mobile app on a connected iPhone, fast.
//
// A Debug build loads its JavaScript from Metro on this Mac (ip.txt in the app
// holds the Mac's LAN address), so the native app only has to change when the
// native inputs do. Those are exactly what Expo's iOS fingerprint hashes (the
// app already uses it as its runtime version), so:
//
//   1. node_modules: cloned (APFS clonefile) from a store keyed by the lockfile
//      and patches, or a real `npm ci` that is then stored.
//   2. Native app: a Debug build cached per fingerprint. A miss runs prebuild,
//      pod install and xcodebuild (several minutes); a hit costs nothing, so a
//      branch that only changes JS reuses the app another branch built.
//   3. Install on the phone only when the fingerprint or the Mac's IP changed.
//   4. Metro from this checkout on :8081 in tmux, then launch the app.
//
// The app installs under its own bundle id ("AO Dev"), next to any store or
// Release build, and pairs with the desktop separately (with its in-app QR
// scanner). It registers dev-only URL schemes, so links and the pairing QR
// opened from outside the app keep going to the store app. Push is left out by
// default: personal (free) teams cannot sign it and dev builds don't need it.
//
// One run at a time per machine: runs from other checkouts wait on a lock, and
// Metro on :8081 serves one checkout, so a run says whose Metro it replaces.
//
// Usage: node scripts/run-device.mjs [run|stop|status] [--rebuild] [--reinstall] [--no-launch]
// Env:   AO_MOBILE_UDID       device to use (default: the one connected iPhone)
//        AO_MOBILE_TEAM       signing team (default: from the Apple Development certificate)
//        AO_MOBILE_BUNDLE_ID  bundle id (default: aoagents.ao.dev.<team>)
//        AO_MOBILE_KEEP_PUSH  1 keeps the aps-environment entitlement
//        AO_MOBILE_DEV_DIR    cache directory (default: ~/.ao/dev/mobile-device)
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, renameSync, rmSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const MOBILE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const STORE = process.env.AO_MOBILE_DEV_DIR ?? path.join(homedir(), ".ao/dev/mobile-device");
const DISPLAY_NAME = "AO Dev";
// RCT_METRO_PORT's default: the Debug app looks for Metro here.
const PORT = 8081;
const TMUX = ["-L", "ao-mobile-device"];
// Bump when the build steps below change, so older cached apps are not reused.
const RECIPE = 2;
const KEEP = 4;
// Registered instead of the store app's aomobile / ao-app, which stay the store app's.
const DEV_URL_SCHEMES = ["aomobile-dev", "ao-app-dev"];
const LOCK = path.join(STORE, "lock");
const METRO_STATE = path.join(STORE, "metro.json");

const args = process.argv.slice(2);
const command = args.find((a) => !a.startsWith("--")) ?? "run";
const flag = (name) => args.includes(`--${name}`);

function log(msg) {
	console.log(`[device] ${msg}`);
}
function fail(msg) {
	console.error(`[device] ERROR: ${msg}`);
	process.exit(1);
}
function run(cmd, cmdArgs, opts = {}) {
	const r = spawnSync(cmd, cmdArgs, { stdio: opts.capture ? "pipe" : "inherit", encoding: "utf8", ...opts });
	if (r.error) {
		if (r.error.code === "ENOENT") fail(`${cmd} is not installed`);
		throw r.error;
	}
	if (r.status !== 0 && !opts.allowFail) {
		fail(`${cmd} ${cmdArgs.join(" ")} exited ${r.status}${opts.capture ? `\n${r.stderr || r.stdout}` : ""}`);
	}
	return r;
}
const out = (cmd, cmdArgs, opts = {}) => run(cmd, cmdArgs, { capture: true, ...opts }).stdout.trim();
const sha = (...parts) => createHash("sha256").update(parts.join("\0")).digest("hex");
const readJSON = (file, fallback) => {
	try {
		return JSON.parse(readFileSync(file, "utf8"));
	} catch {
		return fallback;
	}
};
const writeJSON = (file, value) => {
	mkdirSync(path.dirname(file), { recursive: true });
	writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
};
// APFS clone: near-instant and copy-on-write, so a stored copy never changes.
function clone(src, dst) {
	mkdirSync(path.dirname(dst), { recursive: true });
	rmSync(dst, { recursive: true, force: true });
	run("cp", ["-c", "-R", src, dst]);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// prune() keeps the most recently used entries, so mark an entry used on reuse.
function touch(dir) {
	const now = new Date();
	utimesSync(dir, now, now);
}
function prune(dir) {
	if (!existsSync(dir)) return;
	const entries = readdirSync(dir)
		.map((name) => ({ name, mtime: statSync(path.join(dir, name)).mtimeMs }))
		.sort((a, b) => b.mtime - a.mtime);
	for (const { name } of entries.slice(KEEP)) rmSync(path.join(dir, name), { recursive: true, force: true });
}
function stopMetro() {
	run("tmux", [...TMUX, "kill-server"], { capture: true, allowFail: true });
	rmSync(METRO_STATE, { force: true });
}
const portHolder = () => out("lsof", ["-nP", `-iTCP:${PORT}`, "-sTCP:LISTEN", "-t"], { allowFail: true });

if (process.platform !== "darwin") fail("iOS device runs need macOS with Xcode");
if (command === "stop") {
	stopMetro();
	log("Metro stopped.");
	process.exit(0);
}

const repo = out("git", ["-C", MOBILE, "rev-parse", "--show-toplevel"]);
const branch = out("git", ["-C", repo, "rev-parse", "--abbrev-ref", "HEAD"]);
const statePath = path.join(STORE, "installed.json");

if (command === "status") {
	const metroPid = portHolder() || null;
	const metroServes = metroPid ? readJSON(METRO_STATE, null) : null;
	console.log(JSON.stringify({ repo, branch, installed: readJSON(statePath, {}), metroPid, metroServes, lockedBy: readJSON(path.join(LOCK, "owner.json"), null) }, null, 2));
	process.exit(0);
}
if (command !== "run") fail(`unknown command ${command}`);

// Builds share derived data and the phone, so runs take turns. mkdir is atomic;
// a lock whose owner process is gone is stale.
async function acquireLock() {
	const owner = path.join(LOCK, "owner.json");
	mkdirSync(STORE, { recursive: true });
	for (let i = 0; ; i += 1) {
		try {
			mkdirSync(LOCK);
			writeJSON(owner, { pid: process.pid, checkout: repo, branch, startedAt: new Date().toISOString() });
			break;
		} catch (error) {
			if (error.code !== "EEXIST") throw error;
		}
		const held = readJSON(owner, {});
		let alive = false;
		if (held.pid) {
			try {
				process.kill(held.pid, 0);
				alive = true;
			} catch (error) {
				alive = error.code === "EPERM";
			}
		}
		// No owner file yet means another run is between mkdir and writing it.
		if (!alive && (held.pid || i >= 10)) {
			// Only clear the lock this run judged stale, and move it aside atomically,
			// so two waiters can't both clear it and then both take it.
			if (readJSON(owner, {}).pid === held.pid) {
				const aside = `${LOCK}.stale-${process.pid}`;
				try {
					renameSync(LOCK, aside);
					rmSync(aside, { recursive: true, force: true });
				} catch {}
			}
			continue;
		}
		if (i % 30 === 0) log(`waiting for another run to finish (${held.checkout ?? "unknown checkout"} on ${held.branch ?? "?"}, pid ${held.pid ?? "?"})`);
		await sleep(1000);
	}
	process.on("exit", () => {
		if (readJSON(owner, {}).pid === process.pid) rmSync(LOCK, { recursive: true, force: true });
	});
}

// Configuration ---------------------------------------------------------------
function listDevices() {
	const file = path.join(mkdtempSync(path.join(tmpdir(), "ao-device-")), "devices.json");
	run("xcrun", ["devicectl", "list", "devices", "--json-output", file], { capture: true });
	return (readJSON(file, {}).result?.devices ?? []).filter(
		(d) => d.hardwareProperties?.platform === "iOS" && d.hardwareProperties?.reality === "physical",
	);
}

function pickDevice() {
	const devices = listDevices();
	const wanted = process.env.AO_MOBILE_UDID;
	const name = (d) => d.deviceProperties?.name ?? d.hardwareProperties?.udid;
	const reachable = (d) => d.connectionProperties?.tunnelState !== "unavailable";
	let device;
	if (wanted) {
		device = devices.find((d) => d.hardwareProperties?.udid === wanted);
		if (!device) fail(`no paired iPhone with UDID ${wanted}`);
	} else {
		const candidates = devices.filter(reachable);
		if (candidates.length === 0) fail("no iPhone is reachable: unlock it and connect it by cable (or the same Wi-Fi with wireless pairing)");
		if (candidates.length > 1) {
			fail(`several iPhones are reachable; set AO_MOBILE_UDID to one of:\n${candidates.map((d) => `  ${d.hardwareProperties.udid}  ${name(d)}`).join("\n")}`);
		}
		device = candidates[0];
	}
	if (!reachable(device)) fail(`${name(device)} is unavailable: unlock it and connect it by cable or the same Wi-Fi`);
	return { udid: device.hardwareProperties.udid, name: name(device) };
}

// The team is the OU of a valid Apple Development identity in the keychain.
// Expired certificates and ones without a private key are not identities, so
// they don't count.
function signingTeam() {
	if (process.env.AO_MOBILE_TEAM) return process.env.AO_MOBILE_TEAM;
	const identities = out("security", ["find-identity", "-v", "-p", "codesigning"], { allowFail: true });
	const valid = new Set([...identities.matchAll(/^\s*\d+\)\s+([0-9A-F]{40})\s+"Apple Development:/gm)].map((m) => m[1]));
	const certs = out("security", ["find-certificate", "-a", "-c", "Apple Development", "-Z", "-p"], { allowFail: true });
	const teams = new Set();
	for (const [, hash, pem] of certs.matchAll(/SHA-1 hash:\s*([0-9A-F]{40})[\s\S]*?(-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----)/g)) {
		if (!valid.has(hash)) continue;
		const subject = run("openssl", ["x509", "-noout", "-subject"], { capture: true, input: pem, allowFail: true }).stdout;
		const ou = subject.match(/OU\s*=\s*([A-Z0-9]{10})/)?.[1];
		if (ou) teams.add(ou);
	}
	if (teams.size === 1) return [...teams][0];
	if (teams.size === 0) fail("no valid Apple Development identity found; sign in to Xcode with your Apple ID, or set AO_MOBILE_TEAM");
	fail(`several signing teams found (${[...teams].join(", ")}); set AO_MOBILE_TEAM`);
}

// 1. node_modules -------------------------------------------------------------
function deps() {
	const lock = readFileSync(path.join(MOBILE, "package-lock.json"), "utf8");
	const patchDir = path.join(MOBILE, "patches");
	const patches = existsSync(patchDir)
		? readdirSync(patchDir).sort().map((f) => `${f}\n${readFileSync(path.join(patchDir, f), "utf8")}`)
		: [];
	const key = sha(lock, ...patches, process.version, out("npm", ["--version"])).slice(0, 16);
	const nm = path.join(MOBILE, "node_modules");
	const stamp = path.join(nm, ".ao-device-warm.json");
	if (readJSON(stamp, {}).key === key) return log(`node_modules current (${key})`);
	const stored = path.join(STORE, "node_modules", key);
	if (existsSync(path.join(stored, ".ao-device-warm.json"))) {
		log(`node_modules: cloning stored install ${key}`);
		touch(stored);
		clone(stored, nm);
		return;
	}
	log("node_modules: npm ci (no stored install for this lockfile)");
	rmSync(nm, { recursive: true, force: true });
	run("npm", ["ci", "--no-audit", "--no-fund"], { cwd: MOBILE });
	writeJSON(stamp, { key });
	clone(nm, stored);
	prune(path.join(STORE, "node_modules"));
}

// 2. Native build -------------------------------------------------------------
function fingerprint(team, bundleId, keepPush) {
	const script = 'require("@expo/fingerprint").createFingerprintAsync(".",{platforms:["ios"]}).then(f=>console.log(f.hash))';
	const hash = out("node", ["-e", script], { cwd: MOBILE });
	return { hash, key: sha(hash, RECIPE, bundleId, team, keepPush).slice(0, 16) };
}

// The provisioning profile signed into the app: when it expires (0 when
// unreadable) and which devices it lets the app run on.
function profileInfo(app) {
	const profile = path.join(app, "embedded.mobileprovision");
	const plist = existsSync(profile) ? (run("security", ["cms", "-D", "-i", profile], { capture: true, allowFail: true }).stdout ?? "") : "";
	const date = plist.match(/<key>ExpirationDate<\/key>\s*<date>([^<]+)<\/date>/)?.[1];
	const list = plist.match(/<key>ProvisionedDevices<\/key>\s*<array>([\s\S]*?)<\/array>/)?.[1] ?? "";
	return { expires: date ? Date.parse(date) || 0 : 0, devices: [...list.matchAll(/<string>([^<]+)<\/string>/g)].map((m) => m[1]) };
}

// Why an app can't run on this device, or null when it can. Free-team profiles
// last seven days, and a profile only covers devices registered when it was made.
function profileProblem(app, udid) {
	const { expires, devices } = profileInfo(app);
	if (!expires) return "has no readable provisioning profile";
	if (expires <= Date.now()) return `has a provisioning profile that expired ${new Date(expires).toISOString()}`;
	if (!devices.includes(udid)) return "has a provisioning profile that doesn't include this iPhone";
	return null;
}

function build({ key, hash, team, bundleId, keepPush, udid }) {
	const dir = path.join(STORE, "builds", key);
	const app = path.join(dir, "Orchestrator.app");
	if (existsSync(app) && !flag("rebuild")) {
		const problem = profileProblem(app, udid);
		if (!problem) {
			touch(dir);
			log(`native build reused (fingerprint ${hash.slice(0, 10)}, profile valid until ${new Date(profileInfo(app).expires).toISOString()})`);
			return { app, dir, built: false };
		}
		log(`cached app ${problem}; rebuilding`);
	}
	log(`native build needed (fingerprint ${hash.slice(0, 10)}); this takes several minutes the first time`);
	const before = out("git", ["-C", repo, "status", "--porcelain"]);
	run("npx", ["expo", "prebuild", "--platform", "ios", "--no-install", "--clean"], { cwd: MOBILE, env: { ...process.env, CI: "1" } });
	if (out("git", ["-C", repo, "status", "--porcelain"]) !== before) fail("expo prebuild changed tracked files; check `git status`");

	const ios = path.join(MOBILE, "ios");
	const appDir = path.join(ios, "Orchestrator");
	if (!keepPush) {
		run("/usr/libexec/PlistBuddy", ["-c", "Delete :aps-environment", path.join(appDir, "Orchestrator.entitlements")], { capture: true, allowFail: true });
	}
	const plist = path.join(appDir, "Info.plist");
	const buddy = (cmd, allowFail = false) => run("/usr/libexec/PlistBuddy", ["-c", cmd, plist], { capture: true, allowFail });
	buddy("Delete :CFBundleDisplayName", true);
	buddy(`Add :CFBundleDisplayName string ${DISPLAY_NAME}`);
	// The store app owns aomobile:// (the desktop's pairing QR) and ao-app://.
	// Sign-in still works: the auth session catches its own ao-app callback.
	buddy("Delete :CFBundleURLTypes", true);
	buddy("Add :CFBundleURLTypes array");
	buddy("Add :CFBundleURLTypes:0 dict");
	buddy("Add :CFBundleURLTypes:0:CFBundleURLSchemes array");
	DEV_URL_SCHEMES.forEach((scheme, i) => buddy(`Add :CFBundleURLTypes:0:CFBundleURLSchemes:${i} string ${scheme}`));
	const pbx = path.join(ios, "Orchestrator.xcodeproj/project.pbxproj");
	const source = readFileSync(pbx, "utf8");
	const patched = source.replace(/PRODUCT_BUNDLE_IDENTIFIER = "?aoagents\.ao"?;/g, `PRODUCT_BUNDLE_IDENTIFIER = "${bundleId}";`);
	if (patched === source) fail("bundle identifier not found in project.pbxproj");
	writeFileSync(pbx, patched);

	run("pod", ["install"], { cwd: ios, env: { ...process.env, LANG: "en_US.UTF-8" } });
	const derived = path.join(STORE, "derived");
	run(
		"xcodebuild",
		[
			"-workspace", "Orchestrator.xcworkspace",
			"-scheme", "Orchestrator",
			"-configuration", "Debug",
			// The selected iPhone, registered with the team if it isn't yet, so the
			// profile Xcode makes covers it.
			"-destination", `id=${udid}`,
			"-derivedDataPath", derived,
			"-allowProvisioningUpdates",
			"-allowProvisioningDeviceRegistration",
			"-quiet",
			`DEVELOPMENT_TEAM=${team}`,
		],
		// JS comes from Metro, so skip embedding a bundle; ip.txt is still written.
		{ cwd: ios, env: { ...process.env, SENTRY_DISABLE_AUTO_UPLOAD: "true", SKIP_BUNDLING: "1" } },
	);
	const product = path.join(derived, "Build/Products/Debug-iphoneos/Orchestrator.app");
	if (!existsSync(product)) fail(`xcodebuild finished but ${product} is missing`);
	const problem = profileProblem(product, udid);
	if (problem) fail(`the app xcodebuild just signed ${problem}; check the team's provisioning in Xcode`);
	clone(product, app);
	writeJSON(path.join(dir, "meta.json"), { fingerprint: hash, branch, sha: out("git", ["-C", repo, "rev-parse", "HEAD"]), builtAt: new Date().toISOString() });
	prune(path.join(STORE, "builds"));
	return { app, dir, built: true };
}

// 3. Install ------------------------------------------------------------------
function lanIP() {
	for (let n = 0; n <= 8; n += 1) {
		const ip = out("ipconfig", ["getifaddr", `en${n}`], { allowFail: true });
		if (ip) return ip;
	}
	fail("no LAN address; the phone reaches Metro over Wi-Fi");
}

// Debug builds read the Mac's address from ip.txt. If it moved, rewrite it in a
// copy and re-sign with the same identity and entitlements.
function withIP(app, ip) {
	const current = existsSync(path.join(app, "ip.txt")) ? readFileSync(path.join(app, "ip.txt"), "utf8").trim() : "";
	if (current === ip) return { path: app, cleanup: () => {} };
	log(`Mac address changed (${current || "none"} -> ${ip}); re-signing with the new ip.txt`);
	const tmp = mkdtempSync(path.join(tmpdir(), "ao-device-app-"));
	const cleanup = () => rmSync(tmp, { recursive: true, force: true });
	const copy = path.join(tmp, "Orchestrator.app");
	clone(app, copy);
	writeFileSync(path.join(copy, "ip.txt"), `${ip}\n`);
	// Sign by the leaf certificate's SHA-1: a name can match several identities.
	const certPrefix = path.join(tmp, "cert");
	run("codesign", ["-d", `--extract-certificates=${certPrefix}`, app], { capture: true, allowFail: true });
	const identity = existsSync(`${certPrefix}0`) ? createHash("sha1").update(readFileSync(`${certPrefix}0`)).digest("hex").toUpperCase() : null;
	if (!identity) {
		cleanup();
		fail("could not read the signing certificate of the cached app");
	}
	const ents = path.join(tmp, "ents.plist");
	writeFileSync(ents, run("codesign", ["-d", "--entitlements", "-", "--xml", app], { capture: true }).stdout);
	const signed = run("codesign", ["-f", "-s", identity, "--entitlements", ents, copy], { allowFail: true });
	if (signed.status !== 0) {
		cleanup();
		fail("re-signing the app with the new ip.txt failed");
	}
	return { path: copy, cleanup };
}

// A rebuild for the same fingerprint (a renewed profile) has a new builtAt, so
// the phone gets it even though the key and IP are unchanged.
function install(app, dir, key, udid, force = false) {
	const ip = lanIP();
	const builtAt = readJSON(path.join(dir, "meta.json"), {}).builtAt ?? null;
	const state = readJSON(statePath, {});
	const prev = state[udid];
	if (!force && prev?.key === key && prev?.ip === ip && prev?.builtAt === builtAt && !flag("reinstall")) {
		log("app on the phone already matches; no install");
		return ip;
	}
	log("installing on the phone");
	const staged = withIP(app, ip);
	const result = run("xcrun", ["devicectl", "device", "install", "app", "--device", udid, staged.path], { capture: true, allowFail: true });
	staged.cleanup();
	if (result.status !== 0) {
		const output = `${result.stdout}\n${result.stderr}`;
		if (output.includes("maximum number of installed apps")) {
			fail(`a free developer profile allows 3 apps on a phone and all are in use; delete one from the phone and rerun.\n${output.match(/\{\([\s\S]*?\)\}/)?.[0] ?? ""}`);
		}
		fail(`install failed\n${output}`);
	}
	state[udid] = { key, ip, builtAt, branch, installedAt: new Date().toISOString() };
	writeJSON(statePath, state);
	return ip;
}

// 4. Metro + launch -----------------------------------------------------------
async function metro() {
	const serving = portHolder() ? readJSON(METRO_STATE, null) : null;
	if (serving && (serving.checkout !== repo || serving.branch !== branch)) {
		log(`replacing Metro serving ${serving.checkout} (${serving.branch}); the phone will load this checkout instead`);
	}
	stopMetro();
	for (let i = 0; i < 20 && portHolder(); i += 1) await new Promise((r) => setTimeout(r, 250));
	const holder = portHolder();
	if (holder) {
		const pid = holder.split("\n")[0];
		fail(`port ${PORT} is held by pid ${pid} (${out("ps", ["-o", "command=", "-p", pid], { allowFail: true })}); stop it first`);
	}
	const logFile = path.join(STORE, "metro.log");
	mkdirSync(STORE, { recursive: true });
	writeFileSync(logFile, "");
	run("tmux", [...TMUX, "new-session", "-d", "-s", "metro", "-c", MOBILE, `npx expo start --port ${PORT} 2>&1 | tee '${logFile}'`]);
	for (let i = 0; i < 240; i += 1) {
		try {
			const res = await fetch(`http://127.0.0.1:${PORT}/status`);
			if ((await res.text()).includes("packager-status:running")) {
				writeJSON(METRO_STATE, { checkout: repo, branch, startedAt: new Date().toISOString() });
				return logFile;
			}
		} catch {}
		await new Promise((r) => setTimeout(r, 250));
	}
	stopMetro();
	fail(`Metro did not come up in 60 s; see ${logFile}`);
}

const started = Date.now();
await acquireLock();
const device = pickDevice();
const team = signingTeam();
const bundleId = process.env.AO_MOBILE_BUNDLE_ID ?? `aoagents.ao.dev.${team.toLowerCase()}`;
const keepPush = process.env.AO_MOBILE_KEEP_PUSH === "1";
deps();
const { hash, key } = fingerprint(team, bundleId, keepPush);
const { app, dir, built } = build({ key, hash, team, bundleId, keepPush, udid: device.udid });
const ip = install(app, dir, key, device.udid);
const logFile = await metro();
const launch = () => run("xcrun", ["devicectl", "device", "process", "launch", "--device", device.udid, "--terminate-existing", bundleId], { capture: true, allowFail: true });
if (!flag("no-launch") && launch().status !== 0) {
	// Most often the app was deleted from the phone while installed.json still matches.
	log("launch failed; reinstalling the app and trying again");
	install(app, dir, key, device.udid, true);
	const retry = launch();
	if (retry.status !== 0) fail(`launching ${bundleId} failed\n${retry.stderr || retry.stdout}`);
}
log(
	[
		`ready in ${Math.round((Date.now() - started) / 1000)} s on ${device.name}`,
		`branch ${branch}`,
		`native ${built ? "rebuilt" : "cached"} (${hash.slice(0, 10)}), ${bundleId}`,
		`Metro http://${ip}:${PORT} (log ${logFile}; tmux -L ao-mobile-device attach -t metro)`,
		flag("no-launch") ? "app not launched (--no-launch)" : `launched ${DISPLAY_NAME}`,
	].join("\n  "),
);
