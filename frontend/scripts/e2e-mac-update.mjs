import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join } from "node:path";

const DEFAULTS = {
	channel: "latest",
	downloadTimeoutMs: 20 * 60 * 1000,
	swapTimeoutMs: 5 * 60 * 1000,
	launchTimeoutMs: 3 * 60 * 1000,
	pollIntervalMs: 2000,
};

class UsageError extends Error {}

export function parseArgs(argv) {
	const opts = { ...DEFAULTS };
	for (let i = 0; i < argv.length; i += 1) {
		const flag = argv[i];
		const value = argv[i + 1];
		const needsValue = () => {
			if (value === undefined || value.startsWith("--")) throw new UsageError(`${flag} needs a value`);
			i += 1;
			return value;
		};
		switch (flag) {
			case "--app":
				opts.app = needsValue();
				break;
			case "--expect-version":
				opts.expectVersion = needsValue();
				break;
			case "--state-dir":
				opts.stateDir = needsValue();
				break;
			case "--run-file":
				opts.runFile = needsValue();
				break;
			case "--channel":
				opts.channel = needsValue();
				if (opts.channel !== "latest" && opts.channel !== "nightly") {
					throw new UsageError(`--channel must be latest or nightly, got ${opts.channel}`);
				}
				break;
			case "--download-timeout":
				opts.downloadTimeoutMs = positiveSeconds(flag, needsValue());
				break;
			case "--swap-timeout":
				opts.swapTimeoutMs = positiveSeconds(flag, needsValue());
				break;
			case "--launch-timeout":
				opts.launchTimeoutMs = positiveSeconds(flag, needsValue());
				break;
			default:
				throw new UsageError(`unknown flag: ${flag}`);
		}
	}
	if (!opts.app) throw new UsageError("--app is required");
	if (!opts.expectVersion) throw new UsageError("--expect-version is required");
	if (!opts.app.endsWith(".app")) throw new UsageError(`--app must point at a .app bundle, got ${opts.app}`);
	opts.stateDir ??= join(homedir(), ".ao");
	if (opts.runFile !== undefined) {
		if (!isAbsolute(opts.runFile)) throw new UsageError(`--run-file must be an absolute path, got ${opts.runFile}`);
	}
	if (!isAbsolute(opts.stateDir)) throw new UsageError(`--state-dir must be an absolute path, got ${opts.stateDir}`);
	opts.runFile ??= join(opts.stateDir, "running.json");
	opts.settingsDir = dirname(opts.runFile);
	opts.dataDir = join(opts.stateDir, "data");
	opts.appName = basename(opts.app, ".app");
	return opts;
}

export function launchEnv(opts, sentinel, baseEnv = process.env) {
	return {
		...baseEnv,
		AO_E2E_UPDATE_SENTINEL: sentinel,
		AO_RUN_FILE: opts.runFile,
		AO_DATA_DIR: opts.dataDir,
	};
}

const SENTINEL_ENV = "AO_E2E_UPDATE_SENTINEL";

export function assertSentinelCapable(appPath) {
	const asar = join(appPath, "Contents", "Resources", "app.asar");
	if (!existsSync(asar)) {
		throw new UsageError(`cannot check sentinel support: no ${asar}. Is ${appPath} a packaged build?`);
	}
	if (!readFileSync(asar).includes(SENTINEL_ENV)) {
		throw new UsageError(
			`baseline at ${appPath} has no ${SENTINEL_ENV} listener, so it can never signal that an update staged. ` +
				`Running anyway would just wait out the download timeout and report a false failure. Two different ` +
				`causes look identical here, so check both: the baseline may predate the listener (#3288 workstream 0), ` +
				`or the listener may have been removed from src/main/auto-updater.ts, in which case EVERY baseline ` +
				`built after that point fails this check and picking a newer one makes it worse. That second case is ` +
				`real: #3012 deleted it on 2026-07-31 and run 30758412650 failed here on a 2026-08-01 baseline while ` +
				`a 2026-07-30 baseline passed. Confirm the listener still exists in the app before changing baselines.`,
		);
	}
}

function positiveSeconds(flag, raw) {
	const seconds = Number(raw);
	if (!Number.isFinite(seconds) || seconds <= 0) throw new UsageError(`${flag} must be a positive number of seconds`);
	return seconds * 1000;
}

export function bundleVersion(appPath) {
	const plist = join(appPath, "Contents", "Info.plist");
	return execFileSync("plutil", ["-extract", "CFBundleShortVersionString", "raw", "-o", "-", plist], {
		encoding: "utf8",
	}).trim();
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(label, timeoutMs, check) {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		let result;
		try {
			result = await check();
		} catch {
			result = false;
		}
		if (result) return result;
		if (Date.now() >= deadline) throw new Error(`timed out after ${Math.round(timeoutMs / 1000)}s waiting for ${label}`);
		await sleep(DEFAULTS.pollIntervalMs);
	}
}

export function seedUpdateSettings(settingsDir, channel) {
	mkdirSync(settingsDir, { recursive: true });
	const settings = { enabled: true, channel, nightlyAck: channel === "nightly", feature: null };
	writeFileSync(join(settingsDir, "update-settings.json"), `${JSON.stringify(settings, null, 2)}\n`, { mode: 0o600 });
}

export function removeRunFile(runFile) {
	if (!existsSync(runFile)) return;
	let info;
	try {
		info = JSON.parse(readFileSync(runFile, "utf8"));
	} catch {
		throw new UsageError(`refusing to remove ${runFile}: existing file is not an AO running.json handshake`);
	}
	if (
		typeof info !== "object" ||
		info === null ||
		!Number.isInteger(info.pid) ||
		info.pid <= 0 ||
		!Number.isInteger(info.port) ||
		info.port < 1 ||
		info.port > 65535 ||
		typeof info.startedAt !== "string" ||
		Number.isNaN(Date.parse(info.startedAt))
	) {
		throw new UsageError(`refusing to remove ${runFile}: existing file is not an AO running.json handshake`);
	}
	rmSync(runFile);
}

function quitApp(appName) {
	try {
		execFileSync("osascript", ["-e", `tell application "${appName}" to quit`], { stdio: "ignore" });
	} catch {
	}
}

async function isDaemonAlive(runFile) {
	if (!existsSync(runFile)) return false;
	const { port } = JSON.parse(readFileSync(runFile, "utf8"));
	if (!port) return false;
	const res = await fetch(`http://127.0.0.1:${port}/healthz`, { signal: AbortSignal.timeout(5000) });
	return res.ok;
}

async function run(opts) {
	if (process.platform !== "darwin") throw new UsageError(`macOS only; host is ${process.platform}`);
	if (!existsSync(opts.app)) throw new UsageError(`no such app bundle: ${opts.app}`);

	const startVersion = bundleVersion(opts.app);
	console.log(`installed baseline: ${startVersion}`);
	if (startVersion === opts.expectVersion) {
		throw new UsageError(`baseline is already ${opts.expectVersion}; --expect-version must be the NEW version`);
	}
	assertSentinelCapable(opts.app);

	quitApp(opts.appName);

	const sentinel = join(tmpdir(), `ao-e2e-update-sentinel-${process.pid}.json`);
	rmSync(sentinel, { force: true });
	removeRunFile(opts.runFile);
	seedUpdateSettings(opts.settingsDir, opts.channel);

	const env = launchEnv(opts, sentinel);
	console.log(`launching ${opts.app} (channel: ${opts.channel}, run file: ${opts.runFile})`);
	const child = spawn(join(opts.app, "Contents", "MacOS", readExecutableName(opts.app)), [], {
		env,
		stdio: "inherit",
		detached: false,
	});
	const exited = new Promise((resolve) => child.once("exit", resolve));

	try {
		await waitFor("the native updater to stage the update", opts.downloadTimeoutMs, () => existsSync(sentinel));
		console.log(`update staged: ${readFileSync(sentinel, "utf8").trim()}`);
	} finally {
		quitApp(opts.appName);
	}

	await Promise.race([exited, sleep(60_000)]);

	await waitFor(`the ShipIt swap to land ${opts.expectVersion}`, opts.swapTimeoutMs, () => {
		const current = bundleVersion(opts.app);
		if (current !== startVersion) console.log(`bundle version now: ${current}`);
		return current === opts.expectVersion;
	});
	console.log(`installed bundle is now ${opts.expectVersion}`);

	console.log("relaunching the updated app");
	removeRunFile(opts.runFile);
	spawn(join(opts.app, "Contents", "MacOS", readExecutableName(opts.app)), [], {
		env,
		stdio: "ignore",
		detached: true,
	}).unref();

	await waitFor("the relaunched app's daemon to answer /healthz", opts.launchTimeoutMs, () =>
		isDaemonAlive(opts.runFile),
	);

	const finalVersion = bundleVersion(opts.app);
	if (finalVersion !== opts.expectVersion) {
		throw new Error(`relaunched bundle reports ${finalVersion}, expected ${opts.expectVersion}`);
	}

	quitApp(opts.appName);
	rmSync(sentinel, { force: true });
	console.log(`PASS: ${startVersion} updated to ${finalVersion}, relaunched, daemon alive`);
}

function readExecutableName(appPath) {
	const plist = join(appPath, "Contents", "Info.plist");
	return execFileSync("plutil", ["-extract", "CFBundleExecutable", "raw", "-o", "-", plist], {
		encoding: "utf8",
	}).trim();
}

if (import.meta.url === `file://${process.argv[1]}`) {
	let opts;
	try {
		opts = parseArgs(process.argv.slice(2));
	} catch (err) {
		process.stderr.write(`${err.message}\n`);
		process.stderr.write("usage: node e2e-mac-update.mjs --app <bundle.app> --expect-version <version>\n");
		process.exit(2);
	}
	run(opts).catch((err) => {
		process.stderr.write(`${err.stack || err}\n`);
		process.exit(err instanceof UsageError ? 2 : 1);
	});
}
