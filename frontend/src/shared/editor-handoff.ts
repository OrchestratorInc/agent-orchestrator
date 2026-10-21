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

const editorIDSet = new Set<string>(EDITOR_IDS);

export function isEditorId(value: unknown): value is EditorId {
	return typeof value === "string" && editorIDSet.has(value);
}

export function isTerminalId(value: unknown): value is TerminalId {
	return typeof value === "string" && ((TERMINAL_IDS as readonly string[]).includes(value) || /^terminal-app:[a-f0-9]{64}$/.test(value));
}
