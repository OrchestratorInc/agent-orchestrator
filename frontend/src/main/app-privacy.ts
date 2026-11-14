import { PRIVACY_PANES, type PrivacyPane } from "./privacy-guide";

// Orchestrator's own macOS privacy grants. macOS charges every process in a
// session (the agent CLI and whatever it starts) to Orchestrator, so Claude
// Code's built-in computer use in a terminal session works only once
// Orchestrator holds Accessibility and Screen Recording, whatever the error
// shown in the session names.

export type AppPrivacyGrants = Record<PrivacyPane, boolean>;

/** Runs a file with arguments and resolves to its stdout. */
export type RunFile = (file: string, args: string[]) => Promise<{ stdout: string }>;

const OSASCRIPT = "/usr/bin/osascript";

// Asked in a fresh process every time: a running process keeps the Screen
// Recording answer it first got until it restarts, so Orchestrator cannot ask
// itself. osascript is Orchestrator's child, so macOS answers for Orchestrator.
export const APP_PRIVACY_SCRIPT = [
	"ObjC.import('ApplicationServices');",
	"ObjC.bindFunction('CGPreflightScreenCaptureAccess', ['bool', []]);",
	"JSON.stringify({ accessibility: $.AXIsProcessTrusted(), screenRecording: $.CGPreflightScreenCaptureAccess() });",
].join("\n");

/** Orchestrator's current grants; undefined when they cannot be read. */
export async function readAppPrivacy(run: RunFile): Promise<AppPrivacyGrants | undefined> {
	try {
		const { stdout } = await run(OSASCRIPT, ["-l", "JavaScript", "-e", APP_PRIVACY_SCRIPT]);
		return parseAppPrivacy(stdout);
	} catch {
		return undefined;
	}
}

export function parseAppPrivacy(stdout: string): AppPrivacyGrants | undefined {
	try {
		const value = JSON.parse(stdout.trim()) as Record<string, unknown>;
		if (!value || typeof value !== "object") return undefined;
		if (typeof value.accessibility !== "boolean" || typeof value.screenRecording !== "boolean") return undefined;
		return { accessibility: value.accessibility, screenRecording: value.screenRecording };
	} catch {
		return undefined;
	}
}

export interface AppPrivacyPrompts {
	/** Shows macOS's Accessibility prompt for Orchestrator and lists it under Accessibility. */
	accessibility(): void;
	/** Shows macOS's Screen Recording prompt for Orchestrator and lists it under Screen Recording. */
	screenRecording(): Promise<void>;
}

/**
 * Asks macOS for each missing grant, which lists Orchestrator in that pane.
 * macOS shows each prompt once per app; after that the request only lists it,
 * and the user turns its switch on in System Settings.
 */
export async function requestAppPrivacy(granted: AppPrivacyGrants, prompts: AppPrivacyPrompts): Promise<void> {
	for (const pane of PRIVACY_PANES) {
		if (granted[pane]) continue;
		if (pane === "accessibility") prompts.accessibility();
		else await prompts.screenRecording();
	}
}

/** The .app bundle an executable runs from, or undefined when it is not in one. */
export function appBundlePath(executablePath: string): string | undefined {
	const marker = ".app/Contents/MacOS/";
	const index = executablePath.lastIndexOf(marker);
	return index < 0 ? undefined : executablePath.slice(0, index + ".app".length);
}
