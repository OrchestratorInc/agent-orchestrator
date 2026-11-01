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
const WATCH_SCRIPT = `
ObjC.import("AppKit");
ObjC.import("CoreGraphics");
function snapshot() {
	const front = $.NSWorkspace.sharedWorkspace.frontmostApplication;
	const apps = $.NSRunningApplication.runningApplicationsWithBundleIdentifier("com.apple.systempreferences");
	const pid = apps.count > 0 ? apps.objectAtIndex(0).processIdentifier : -1;
	let bounds = null;
	if (pid > 0) {
		const windows = ObjC.deepUnwrap(ObjC.castRefToObject($.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly | $.kCGWindowListExcludeDesktopElements, 0))) || [];
		const window = windows.find((w) => w.kCGWindowOwnerPID === pid && w.kCGWindowLayer === 0 && w.kCGWindowBounds.Width > 200);
		if (window) bounds = { x: window.kCGWindowBounds.X, y: window.kCGWindowBounds.Y, width: window.kCGWindowBounds.Width, height: window.kCGWindowBounds.Height };
	}
	return JSON.stringify({ frontPid: front ? front.processIdentifier : -1, settingsPid: pid, bounds: bounds });
}
function run() {
	const out = $.NSFileHandle.fileHandleWithStandardOutput;
	for (;;) {
		out.writeData($(snapshot() + "\\n").dataUsingEncoding($.NSUTF8StringEncoding));
		delay(0.15);
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
export function watchSettingsWindow(onSnapshot: (snapshot: SettingsWindowSnapshot) => void, onError: () => void): () => void {
	const child = spawn("/usr/bin/osascript", ["-l", "JavaScript", "-e", WATCH_SCRIPT], { stdio: ["ignore", "pipe", "ignore"] });
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
