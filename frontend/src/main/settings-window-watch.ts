import { spawn } from "node:child_process";

export interface Rect {
	x: number;
	y: number;
	width: number;
	height: number;
}

/** One reading of System Settings: whether it runs, its main window, and which app is in front. */
export interface SettingsWindowSnapshot {
	frontPid: number;
	settingsPid: number;
	bounds: Rect | null;
}

// A single long-lived JXA process prints a JSON snapshot per tick. Window
// owners and bounds come from CGWindowList, which needs no permission (only
// window titles do), in the same top-left point space as Electron's screen.
// The process ends on its own when its stdout closes.
//
// Only while System Settings or AO is in front can the strip be shown, so only
// then does a tick list windows, and quickly enough to follow a drag. Otherwise
// it reads just the front app, once a second, until one of them comes back.
export const FAST_TICK_SECONDS = 0.15;
export const IDLE_TICK_SECONDS = 1;
const WATCH_SCRIPT = `
ObjC.import("AppKit");
ObjC.import("CoreGraphics");
function snapshot(ownPid) {
	const front = $.NSWorkspace.sharedWorkspace.frontmostApplication;
	const frontPid = front ? front.processIdentifier : -1;
	const apps = $.NSRunningApplication.runningApplicationsWithBundleIdentifier("com.apple.systempreferences");
	const pid = apps.count > 0 ? apps.objectAtIndex(0).processIdentifier : -1;
	const active = pid > 0 && (frontPid === pid || frontPid === ownPid);
	let bounds = null;
	if (active) {
		const windows = ObjC.deepUnwrap(ObjC.castRefToObject($.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly | $.kCGWindowListExcludeDesktopElements, 0))) || [];
		const window = windows.find((w) => w.kCGWindowOwnerPID === pid && w.kCGWindowLayer === 0 && w.kCGWindowBounds.Width > 200);
		if (window) bounds = { x: window.kCGWindowBounds.X, y: window.kCGWindowBounds.Y, width: window.kCGWindowBounds.Width, height: window.kCGWindowBounds.Height };
	}
	return { line: JSON.stringify({ frontPid: frontPid, settingsPid: pid, bounds: bounds }), active: active };
}
function run(argv) {
	const ownPid = Number(argv[0]);
	const out = $.NSFileHandle.fileHandleWithStandardOutput;
	for (;;) {
		const tick = snapshot(ownPid);
		out.writeData($(tick.line + "\\n").dataUsingEncoding($.NSUTF8StringEncoding));
		delay(tick.active ? ${FAST_TICK_SECONDS} : ${IDLE_TICK_SECONDS});
	}
}
`;

export function parseSettingsWindowSnapshot(line: string): SettingsWindowSnapshot | undefined {
	try {
		const value = JSON.parse(line) as Partial<SettingsWindowSnapshot>;
		if (typeof value.frontPid !== "number" || typeof value.settingsPid !== "number") return undefined;
		const bounds = value.bounds;
		const validBounds = bounds && [bounds.x, bounds.y, bounds.width, bounds.height].every((n) => typeof n === "number" && Number.isFinite(n));
		return { frontPid: value.frontPid, settingsPid: value.settingsPid, bounds: validBounds ? bounds : null };
	} catch {
		return undefined;
	}
}

/** Streams System Settings snapshots until the returned stop is called; onError fires if the watcher cannot run. */
export function watchSettingsWindow(onSnapshot: (snapshot: SettingsWindowSnapshot) => void, onError: () => void, ownPid = process.pid): () => void {
	const child = spawn("/usr/bin/osascript", ["-l", "JavaScript", "-e", WATCH_SCRIPT, String(ownPid)], { stdio: ["ignore", "pipe", "ignore"] });
	let buffered = "";
	let stopped = false;
	child.stdout.setEncoding("utf8");
	child.stdout.on("data", (chunk: string) => {
		buffered += chunk;
		let newline: number;
		while ((newline = buffered.indexOf("\n")) >= 0) {
			const snapshot = parseSettingsWindowSnapshot(buffered.slice(0, newline));
			buffered = buffered.slice(newline + 1);
			if (snapshot && !stopped) onSnapshot(snapshot);
		}
	});
	const fail = () => {
		if (!stopped) onError();
		stopped = true;
	};
	child.on("error", fail);
	child.on("exit", fail);
	return () => {
		stopped = true;
		child.kill();
	};
}
