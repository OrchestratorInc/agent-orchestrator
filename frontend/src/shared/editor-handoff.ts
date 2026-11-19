export const EDITOR_IDS = [
	"cursor",
	"vscode",
	"neovim",
	"windsurf",
	"zed",
	"trae",
	"kiro",
	"positron",
	"vscodium",
	"vscode-insiders",
	"sublime",
	"intellij",
	"webstorm",
	"pycharm",
	"goland",
	"phpstorm",
	"rubymine",
	"clion",
	"rider",
	"android-studio",
	"fleet",
	"antigravity",
] as const;

export type EditorId = (typeof EDITOR_IDS)[number];
export const TERMINAL_IDS = ["terminal", "ghostty", "iterm2", "wezterm", "kitty", "alacritty", "gnome-terminal", "konsole", "xfce4-terminal", "windows-terminal"] as const;
export type TerminalId = (typeof TERMINAL_IDS)[number] | `terminal-app:${string}`;
export type OpenTargetId = EditorId | "file-manager" | TerminalId;
export type OpenTargetKind = "editor" | "file_manager" | "terminal";

export type OpenTarget = {
	id: OpenTargetId;
	name: string;
	kind: OpenTargetKind;
	/** Native app icon, without exposing its installation path to the renderer. */
	iconDataUrl?: string;
};

export type EditorHandoffStateOptions = { refreshTerminals?: boolean };

export type EditorHandoffState = {
	targets: OpenTarget[];
	preferredEditorId: EditorId;
	workspaceAvailable: boolean;
	unavailableReason?: string;
	/** The daemon's error code, when it answered; lets the renderer tell "gone" from "couldn't check". */
	unavailableCode?: string;
};

/** The installed editors and the stored default, independent of any session. */
export type EditorPreferenceState = {
	editors: OpenTarget[];
	preferredEditorId: EditorId;
};

export type OpenSessionTargetInput = {
	sessionId: string;
	targetId?: OpenTargetId;
};

export type OpenSessionTargetResult = OpenTarget;

/** Where a session's artifact directory can open: the system file manager or a terminal. */
export type OpenArtifactsTargetId = Extract<OpenTargetId, "file-manager" | "terminal">;

export type OpenSessionArtifactsInput = {
	sessionId: string;
	targetId: OpenArtifactsTargetId;
	/** One artifact, relative to the artifact directory: the file manager reveals it and a terminal opens in its folder. */
	path?: string;
};

/** One artifact to open in the system's default browser. */
export type OpenArtifactInBrowserInput = {
	sessionId: string;
	/** Relative to the session's artifact directory. */
	path: string;
};

/**
 * Files a browser renders itself: pages, SVG, and every image, video, audio
 * and PDF format the artifact viewer previews (renderer/lib/file-media.ts).
 * Anything else could hand an executable to its OS handler.
 */
const BROWSER_ARTIFACT_EXTENSIONS: ReadonlySet<string> = new Set([
	".html", ".htm", ".svg", ".pdf",
	".apng", ".avif", ".bmp", ".gif", ".ico", ".jpg", ".jpeg", ".png", ".webp",
	".m4v", ".mov", ".mp4", ".ogv", ".webm",
	".aac", ".flac", ".m4a", ".mp3", ".oga", ".ogg", ".opus", ".wav",
]);

/** Whether an artifact opens in the default browser: the one rule the Open in browser action and the main process share. */
export function isBrowserArtifactPath(path: string): boolean {
	const name = path.slice(Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\")) + 1);
	const dot = name.lastIndexOf(".");
	return dot > 0 && BROWSER_ARTIFACT_EXTENSIONS.has(name.slice(dot).toLowerCase());
}

const editorIDSet = new Set<string>(EDITOR_IDS);

export function isEditorId(value: unknown): value is EditorId {
	return typeof value === "string" && editorIDSet.has(value);
}

export function isTerminalId(value: unknown): value is TerminalId {
	return typeof value === "string" && ((TERMINAL_IDS as readonly string[]).includes(value) || /^terminal-app:[a-f0-9]{64}$/.test(value));
}

/** Explicit desktop action on a normalized chat path; relative paths use the session workspace. */
export type ChatPathActionInput = {
	sessionId: string;
	path: string;
	action: "copy" | "reveal" | "open";
	editorId?: EditorId;
};
