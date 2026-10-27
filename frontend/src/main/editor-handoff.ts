import { createHash } from "node:crypto";
import { constants, statSync } from "node:fs";
import path from "node:path";
import {
	isEditorId,
	isTerminalId,
	type EditorHandoffState,
	type EditorHandoffStateOptions,
	type EditorId,
	type OpenSessionArtifactsInput,
	type OpenSessionTargetInput,
	type OpenSessionTargetResult,
	type OpenTarget,
	type TerminalId,
} from "../shared/editor-handoff";
import type { MacTerminalApp } from "./mac-terminal-apps";

type Platform = NodeJS.Platform;

type ResolvedCommand = {
	command: string;
	iconPath?: string;
	argsForWorkspace: (workspacePath: string) => string[];
};

type EditorCandidate = {
	id: EditorId;
	name: string;
	commands: string[];
	macApps?: string[];
	requiresTerminal?: boolean;
	/**
	 * Windows-only fallback install locations, relative to a resolved root
	 * (e.g. `%LOCALAPPDATA%\Programs\Cursor\bin`). These are probed only on
	 * win32 and only AFTER a PATH scan fails, so an explicit PATH install
	 * always wins. Paths are built from environment variables at resolve time,
	 * never hard-coded to a specific machine.
	 */
	winInstallDirs?: string[][];
};

const PROGRAM_FILES = "%ProgramFiles%";
const PROGRAM_FILES_X86 = "%ProgramFiles(x86)%";
const LOCAL_APPDATA_PROGRAMS = "%LOCALAPPDATA%\\Programs";

const EDITOR_CANDIDATES: EditorCandidate[] = [
	{ id: "cursor", name: "Cursor", commands: ["cursor"], macApps: ["Cursor"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Cursor", "resources", "app", "bin"], [PROGRAM_FILES, "Cursor", "bin"]] },
	{ id: "vscode", name: "VS Code", commands: ["code"], macApps: ["Visual Studio Code"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Microsoft VS Code", "bin"], [PROGRAM_FILES, "Microsoft VS Code", "bin"]] },
	{ id: "neovim", name: "Neovim", commands: ["nvim"], requiresTerminal: true },
	{ id: "windsurf", name: "Windsurf", commands: ["windsurf"], macApps: ["Windsurf"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Windsurf", "resources", "app", "bin"], [PROGRAM_FILES, "Windsurf", "bin"]] },
	{ id: "zed", name: "Zed", commands: ["zed"], macApps: ["Zed"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Zed", "bin"]] },
	{ id: "trae", name: "Trae", commands: ["trae"], macApps: ["Trae"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Trae", "resources", "app", "bin"], [PROGRAM_FILES, "Trae", "bin"]] },
	{ id: "kiro", name: "Kiro", commands: ["kiro"], macApps: ["Kiro"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Kiro", "bin"]] },
	{ id: "positron", name: "Positron", commands: ["positron"], macApps: ["Positron"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Positron", "resources", "app", "bin"]] },
	{ id: "vscodium", name: "VSCodium", commands: ["codium"], macApps: ["VSCodium"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "VSCodium", "resources", "app", "bin"], [PROGRAM_FILES, "VSCodium", "bin"]] },
	{ id: "vscode-insiders", name: "VS Code Insiders", commands: ["code-insiders"], macApps: ["Visual Studio Code - Insiders"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Microsoft VS Code Insiders", "bin"], [PROGRAM_FILES, "Microsoft VS Code Insiders", "bin"]] },
	{ id: "sublime", name: "Sublime Text", commands: ["subl"], macApps: ["Sublime Text"], winInstallDirs: [[PROGRAM_FILES, "Sublime Text"], [PROGRAM_FILES, "Sublime Text 3"], [LOCAL_APPDATA_PROGRAMS, "Sublime Text"]] },
	{ id: "intellij", name: "IntelliJ IDEA", commands: ["idea"], macApps: ["IntelliJ IDEA", "IntelliJ IDEA CE"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "IntelliJ IDEA", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "IntelliJ IDEA", "bin"]] },
	{ id: "webstorm", name: "WebStorm", commands: ["webstorm"], macApps: ["WebStorm"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "WebStorm", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "WebStorm", "bin"]] },
	{ id: "pycharm", name: "PyCharm", commands: ["pycharm"], macApps: ["PyCharm", "PyCharm CE"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "PyCharm", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "PyCharm", "bin"]] },
	{ id: "goland", name: "GoLand", commands: ["goland"], macApps: ["GoLand"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "GoLand", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "GoLand", "bin"]] },
	{ id: "phpstorm", name: "PhpStorm", commands: ["phpstorm"], macApps: ["PhpStorm"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "PhpStorm", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "PhpStorm", "bin"]] },
	{ id: "rubymine", name: "RubyMine", commands: ["rubymine"], macApps: ["RubyMine"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "RubyMine", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "RubyMine", "bin"]] },
	{ id: "clion", name: "CLion", commands: ["clion"], macApps: ["CLion"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "CLion", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "CLion", "bin"]] },
	{ id: "rider", name: "Rider", commands: ["rider"], macApps: ["Rider"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "Rider", "bin"], [LOCAL_APPDATA_PROGRAMS, "JetBrains", "Rider", "bin"]] },
	{ id: "android-studio", name: "Android Studio", commands: ["studio"], macApps: ["Android Studio"], winInstallDirs: [[PROGRAM_FILES, "Android", "Android Studio", "bin"]] },
	{ id: "fleet", name: "Fleet", commands: ["fleet"], macApps: ["Fleet"], winInstallDirs: [[PROGRAM_FILES, "JetBrains", "Fleet", "bin"]] },
	{ id: "antigravity", name: "Antigravity IDE", commands: ["antigravity-ide", "antigravity"], macApps: ["Antigravity IDE", "Antigravity"], winInstallDirs: [[LOCAL_APPDATA_PROGRAMS, "Antigravity IDE", "bin"], [PROGRAM_FILES, "Antigravity IDE", "bin"]] },
];

const WIN_INSTALL_ROOTS: Record<string, (env: NodeJS.ProcessEnv) => string | undefined> = {
	[LOCAL_APPDATA_PROGRAMS]: (env) => {
		const localAppData = env.LOCALAPPDATA;
		return localAppData ? path.win32.join(localAppData, "Programs") : undefined;
	},
	[PROGRAM_FILES]: (env) => env.ProgramFiles || env.PROGRAMFILES,
	[PROGRAM_FILES_X86]: (env) => env["ProgramFiles(x86)"] || env.PROGRAMFILES_X86,
};

export type EditorHandoffDeps = {
	platform: Platform;
	env: NodeJS.ProcessEnv;
	homeDir: string;
	resolveWorkspace: (sessionId: string) => Promise<string>;
	/** The session's artifact directory, which lives outside its workspace. */
	resolveArtifactDir: (sessionId: string) => Promise<string>;
	readPreference: () => Promise<EditorId>;
	writePreference: (editorId: EditorId) => Promise<void>;
	launch: (command: string, args: readonly string[], cwd: string) => Promise<void>;
	openDirectory: (workspacePath: string) => Promise<void>;
	/** Shows one file selected in the system file manager. */
	revealPath: (filePath: string) => Promise<void>;
	/** Resolves symlinks in an existing path. */
	realPath: (candidatePath: string) => Promise<string>;
	/** Puts text on the system clipboard. The workspace path never reaches the renderer. */
	copyText?: (text: string) => void | Promise<void>;
	/** Reads a native app icon as a data URL; installation paths stay in main. */
	readIcon?: (appPath: string) => Promise<string>;
	/** Registered macOS shell apps that accept folder-open events. */
	discoverTerminalApps?: () => Promise<MacTerminalApp[]>;
	isExecutable?: (candidatePath: string) => boolean;
	isDirectory?: (candidatePath: string) => boolean;
	logError?: (message: string, error: unknown) => void;
};

export type EditorHandoff = {
	getState(sessionId: string, options?: EditorHandoffStateOptions): Promise<EditorHandoffState>;
	open(input: OpenSessionTargetInput): Promise<OpenSessionTargetResult>;
	openArtifacts(input: OpenSessionArtifactsInput): Promise<OpenSessionTargetResult>;
	copyWorkspacePath(sessionId: string): Promise<void>;
};

function defaultIsExecutable(candidatePath: string, platform: Platform): boolean {
	try {
		const stat = statSync(candidatePath);
		return stat.isFile() && (platform === "win32" || (stat.mode & constants.X_OK) !== 0);
	} catch {
		return false;
	}
}

function defaultIsDirectory(candidatePath: string): boolean {
	try {
		return statSync(candidatePath).isDirectory();
	} catch {
		return false;
	}
}

function pathAPI(platform: Platform) {
	return platform === "win32" ? path.win32 : path.posix;
}

function isInside(api: typeof path.posix, dir: string, candidate: string): boolean {
	const relative = api.relative(dir, candidate);
	return Boolean(relative) && relative !== ".." && !relative.startsWith(`..${api.sep}`) && !api.isAbsolute(relative);
}

// An artifact's absolute path with symlinks resolved, which must stay inside
// the artifact directory both as written and once resolved.
async function artifactFilePath(
	api: typeof path.posix,
	realPath: (candidatePath: string) => Promise<string>,
	artifactDir: string,
	relativePath: string,
): Promise<string> {
	if (typeof relativePath !== "string" || !relativePath.trim() || api.isAbsolute(relativePath)) {
		throw new Error("That artifact is not valid.");
	}
	const filePath = api.resolve(artifactDir, relativePath);
	if (!isInside(api, artifactDir, filePath)) throw new Error("That artifact is not valid.");
	const realDir = await realPath(artifactDir);
	const realFile = await realPath(filePath).catch(() => {
		throw new Error("That artifact no longer exists.");
	});
	if (!isInside(api, realDir, realFile)) throw new Error("That artifact is not valid.");
	return realFile;
}

function executableNames(command: string, platform: Platform, env: NodeJS.ProcessEnv): string[] {
	if (platform !== "win32" || pathAPI(platform).extname(command)) return [command];
	const extensions = (env.PATHEXT || ".COM;.EXE;.BAT;.CMD").split(";").filter(Boolean);
	const extended = [
		...extensions.map((extension) => command + extension.toLowerCase()),
		...extensions.map((extension) => command + extension.toUpperCase()),
	];
	// On win32, probe Windows-native launchers (.exe/.cmd/.bat/etc.) before the
	// bare, extension-less name. Editors like VS Code and Cursor ship a `.cmd`
	// batch shim and a bare `#!/usr/bin/env sh` script side by side; returning
	// the bare script makes spawn fail because Windows cannot execute an `sh`
	// script directly. Preferring the extension shims keeps the real launcher.
	return [...extended, command];
}

function commandSearchDirs(platform: Platform, env: NodeJS.ProcessEnv): string[] {
	const fromPath = (env.PATH || "").split(pathAPI(platform).delimiter).filter(Boolean);
	if (platform === "darwin") return [...fromPath, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"];
	if (platform === "linux") return [...fromPath, "/usr/local/bin", "/usr/bin"];
	return fromPath;
}

function resolveOnPath(
	command: string,
	platform: Platform,
	env: NodeJS.ProcessEnv,
	isExecutable: (candidatePath: string) => boolean,
): string | undefined {
	for (const directory of commandSearchDirs(platform, env)) {
		for (const name of executableNames(command, platform, env)) {
			const candidatePath = pathAPI(platform).join(directory, name);
			if (isExecutable(candidatePath)) return candidatePath;
		}
	}
	return undefined;
}

function shellQuote(value: string): string {
	return `'${value.replaceAll("'", `'\\''`)}'`;
}

function appleScriptString(value: string): string {
	return `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}

function windowsCommandArg(value: string): string {
	return `"${value.replaceAll('"', '""')}"`;
}

const LINUX_TERMINAL_LAUNCHERS = [
	{ command: "x-terminal-emulator", argsBeforeCommand: ["-e"] },
	{ command: "gnome-terminal", argsBeforeCommand: ["--"] },
	{ command: "konsole", argsBeforeCommand: ["-e"] },
	{ command: "xfce4-terminal", argsBeforeCommand: ["--execute"] },
	{ command: "kitty", argsBeforeCommand: [] },
	{ command: "alacritty", argsBeforeCommand: ["-e"] },
] as const;

function resolveTerminalEditor(editorCommand: string, deps: EditorHandoffDeps, isExecutable: (candidatePath: string) => boolean): ResolvedCommand | undefined {
	if (deps.platform === "darwin") {
		return {
			command: "/usr/bin/osascript",
			argsForWorkspace: (workspacePath) => {
				const commandLine = `exec ${shellQuote(editorCommand)} ${shellQuote(workspacePath)}`;
				const script = `tell application "Terminal"\nactivate\ndo script ${appleScriptString(commandLine)}\nend tell`;
				return ["-e", script];
			},
		};
	}
	if (deps.platform === "win32") {
		return {
			command: deps.env.ComSpec || deps.env.COMSPEC || "cmd.exe",
			argsForWorkspace: () => {
				// launch() sets cwd to the workspace, so opening "." avoids sending the
				// workspace path through cmd.exe variable expansion (for example %TEMP%).
				const commandLine = [editorCommand, "."].map(windowsCommandArg).join(" ");
				// /s strips the first and last quotes around the command string. The
				// extra outer pair preserves the quotes around both executable paths.
				return ["/d", "/s", "/v:off", "/k", `"${commandLine}"`];
			},
		};
	}
	for (const launcher of LINUX_TERMINAL_LAUNCHERS) {
		const resolved = resolveOnPath(launcher.command, deps.platform, deps.env, isExecutable);
		if (resolved) {
			return {
				command: resolved,
				argsForWorkspace: (workspacePath) => [...launcher.argsBeforeCommand, editorCommand, workspacePath],
			};
		}
	}
	return undefined;
}

// Expands a candidate's Windows install locations into real paths. Each entry
// is a sequence of path segments whose root is one of the WIN_INSTALL_ROOTS
// keys (resolved from env) followed by literal segments. Returns the concrete
// directories, skipping any whose root env var is unset.
function winInstallDirsFor(candidate: EditorCandidate, env: NodeJS.ProcessEnv): string[] {
	const dirs: string[] = [];
	for (const segments of candidate.winInstallDirs ?? []) {
		const [rootToken, ...rest] = segments;
		const resolver = WIN_INSTALL_ROOTS[rootToken];
		if (!resolver) continue;
		const root = resolver(env);
		if (!root) continue;
		dirs.push(path.win32.join(root, ...rest));
	}
	return dirs;
}

function resolveInDirs(
	command: string,
	platform: Platform,
	env: NodeJS.ProcessEnv,
	dirs: readonly string[],
	isExecutable: (candidatePath: string) => boolean,
): string | undefined {
	for (const directory of dirs) {
		for (const name of executableNames(command, platform, env)) {
			const candidatePath = pathAPI(platform).join(directory, name);
			if (isExecutable(candidatePath)) return candidatePath;
		}
	}
	return undefined;
}

function resolveEditor(
	candidate: EditorCandidate,
	deps: EditorHandoffDeps,
	isExecutable: (candidatePath: string) => boolean,
	isDirectory: (candidatePath: string) => boolean,
): ResolvedCommand | undefined {
	for (const command of candidate.commands) {
		const resolved = resolveOnPath(command, deps.platform, deps.env, isExecutable);
		if (resolved) {
			if (candidate.requiresTerminal) return resolveTerminalEditor(resolved, deps, isExecutable);
			return {
				command: resolved,
				argsForWorkspace: (workspacePath) => [workspacePath],
			};
		}
	}
	// Windows-only fallback: probe standard per-user and system install locations
	// after PATH misses. Keep this on the PATH-first path so an explicit PATH
	// install always wins, and gate strictly on win32 so macOS/Linux are untouched.
	if (deps.platform === "win32") {
		const installDirs = winInstallDirsFor(candidate, deps.env);
		for (const command of candidate.commands) {
			const resolved = resolveInDirs(command, deps.platform, deps.env, installDirs, isExecutable);
			if (resolved) return { command: resolved, argsForWorkspace: (workspacePath) => [workspacePath] };
		}
	}
	if (deps.platform !== "darwin") return undefined;
	for (const appName of candidate.macApps ?? []) {
		for (const root of ["/Applications", path.posix.join(deps.homeDir, "Applications")]) {
			if (isDirectory(path.posix.join(root, `${appName}.app`))) {
				return {
					command: "/usr/bin/open",
					argsForWorkspace: (workspacePath) => ["-a", appName, workspacePath],
				};
			}
		}
	}
	return undefined;
}

function resolveTerminal(
	deps: EditorHandoffDeps,
	isExecutable: (candidatePath: string) => boolean,
): { target: OpenTarget; command: ResolvedCommand } | undefined {
	if (deps.platform === "darwin") {
		return {
			target: { id: "terminal", name: "Terminal", kind: "terminal" },
			command: {
				command: "/usr/bin/open",
				iconPath: "/System/Applications/Utilities/Terminal.app",
				argsForWorkspace: (workspacePath) => ["-a", "Terminal", workspacePath],
			},
		};
	}
	if (deps.platform === "win32") {
		return {
			target: { id: "terminal", name: "Command Prompt", kind: "terminal" },
			command: {
				command: deps.env.ComSpec || deps.env.COMSPEC || "cmd.exe",
				argsForWorkspace: () => [],
			},
		};
	}
	for (const launcher of LINUX_TERMINAL_LAUNCHERS) {
		const resolved = resolveOnPath(launcher.command, deps.platform, deps.env, isExecutable);
		if (resolved) {
			return {
				target: { id: "terminal", name: "Terminal", kind: "terminal" },
				command: { command: resolved, argsForWorkspace: () => [] },
			};
		}
	}
	return undefined;
}

// Keep per-terminal directory arguments explicit: a GUI terminal may reuse an
// existing process, in which case the launcher's cwd alone is not enough.
const TERMINAL_CANDIDATES: {
	id: TerminalId;
	name: string;
	command: string;
	macApp?: string;
	platforms: Platform[];
	args: (directory: string) => string[];
}[] = [
	{ id: "ghostty", name: "Ghostty", command: "ghostty", macApp: "Ghostty", platforms: ["darwin", "linux"], args: (dir) => [`--working-directory=${dir}`] },
	{ id: "iterm2", name: "iTerm2", command: "", macApp: "iTerm", platforms: ["darwin"], args: (dir) => [dir] },
	{ id: "wezterm", name: "WezTerm", command: "wezterm", macApp: "WezTerm", platforms: ["darwin", "linux", "win32"], args: (dir) => ["start", "--cwd", dir] },
	{ id: "kitty", name: "kitty", command: "kitty", macApp: "kitty", platforms: ["darwin", "linux"], args: (dir) => ["--directory", dir] },
	{ id: "alacritty", name: "Alacritty", command: "alacritty", macApp: "Alacritty", platforms: ["darwin", "linux", "win32"], args: (dir) => ["--working-directory", dir] },
	{ id: "gnome-terminal", name: "GNOME Terminal", command: "gnome-terminal", platforms: ["linux"], args: (dir) => [`--working-directory=${dir}`] },
	{ id: "konsole", name: "Konsole", command: "konsole", platforms: ["linux"], args: (dir) => ["--workdir", dir] },
	{ id: "xfce4-terminal", name: "Xfce Terminal", command: "xfce4-terminal", platforms: ["linux"], args: (dir) => [`--working-directory=${dir}`] },
	{ id: "windows-terminal", name: "Windows Terminal", command: "wt", platforms: ["win32"], args: (dir) => ["-d", dir] },
];

function resolveInstalledTerminals(
	deps: EditorHandoffDeps,
	isExecutable: (candidatePath: string) => boolean,
	isDirectory: (candidatePath: string) => boolean,
	macApps: MacTerminalApp[],
): { target: OpenTarget; command: ResolvedCommand }[] {
	const claimedApps = new Set<string>();
	const known = TERMINAL_CANDIDATES.flatMap((candidate) => {
		if (!candidate.platforms.includes(deps.platform)) return [];
		const target: OpenTarget = { id: candidate.id, name: candidate.name, kind: "terminal" };
		if (deps.platform === "darwin" && candidate.macApp) {
			const registered = macApps.filter((app) => path.posix.basename(app.appPath) === `${candidate.macApp}.app`);
			const appPaths = [
				...registered.map((app) => app.appPath),
				...["/Applications", path.posix.join(deps.homeDir, "Applications")].map((root) => path.posix.join(root, `${candidate.macApp}.app`)),
			];
			for (const appPath of appPaths) {
				if (!isDirectory(appPath)) continue;
				for (const app of registered) claimedApps.add(app.appPath);
				claimedApps.add(appPath);
				const iconDataUrl = registered.find((app) => app.appPath === appPath)?.iconDataUrl;
				if (iconDataUrl) target.iconDataUrl = iconDataUrl;
				return [{ target, command: {
					command: "/usr/bin/open",
					iconPath: appPath,
					// Ghostty and iTerm set the surface directory from folder-open
					// events, including when already running. Other apps use CLI flags.
					argsForWorkspace: (dir: string) => candidate.id === "iterm2" || candidate.id === "ghostty"
						? ["-a", appPath, dir]
						: ["-n", "-a", appPath, "--args", ...candidate.args(dir)],
				} }];
			}
			// Ghostty's native macOS app must launch through Launch Services.
			if (candidate.id === "ghostty" || candidate.id === "iterm2") return [];
		}
		const command = candidate.command && resolveOnPath(candidate.command, deps.platform, deps.env, isExecutable);
		return command ? [{ target, command: { command, argsForWorkspace: candidate.args } }] : [];
	});
	const claimedBundleIds = new Set(["com.apple.terminal"]);
	const discovered = macApps.flatMap((app) => {
		if (claimedBundleIds.has(app.bundleId.toLowerCase()) || claimedApps.has(app.appPath) || !isDirectory(app.appPath)) return [];
		claimedApps.add(app.appPath);
		claimedBundleIds.add(app.bundleId.toLowerCase());
		// An opaque stable ID crosses IPC; installation paths remain in main.
		const id: TerminalId = `terminal-app:${createHash("sha256").update(app.bundleId.toLowerCase()).digest("hex")}`;
		return [{ target: { id, name: app.name, kind: "terminal" as const, ...(app.iconDataUrl ? { iconDataUrl: app.iconDataUrl } : {}) }, command: {
			command: "/usr/bin/open",
			iconPath: app.appPath,
			argsForWorkspace: (dir: string) => ["-a", app.appPath, dir],
		} }];
	});
	return [...known, ...discovered.sort((a, b) => a.target.name.localeCompare(b.target.name))];
}

export function createEditorHandoff(deps: EditorHandoffDeps): EditorHandoff {
	const isExecutable = deps.isExecutable ?? ((candidatePath) => defaultIsExecutable(candidatePath, deps.platform));
	const isDirectory = deps.isDirectory ?? defaultIsDirectory;
	const fileManager: OpenTarget = {
		id: "file-manager",
		name: deps.platform === "darwin" ? "Finder" : deps.platform === "win32" ? "File Explorer" : "File Manager",
		kind: "file_manager",
	};

	const icons = new Map<string, Promise<string | undefined>>();
	const readIcon = (iconPath: string) => {
		if (!deps.readIcon) return Promise.resolve(undefined);
		let icon = icons.get(iconPath);
		if (!icon) {
			icon = deps.readIcon(iconPath).then((data) => /^data:image\/png;base64,[A-Za-z0-9+/]+={0,2}$/.test(data) ? data : undefined).catch(() => {
				icons.delete(iconPath);
				return undefined;
			});
			icons.set(iconPath, icon);
		}
		return icon;
	};

	// Only an explicit menu refresh queries Launch Services and renders bundle
	// icons. Readiness polling, CDC invalidations, and launches use this shared
	// main-process snapshot; file existence is still checked at resolution time.
	let macApps: MacTerminalApp[] = [];
	let terminalRefresh: Promise<void> | undefined;
	const refreshTerminals = () => {
		if (deps.platform !== "darwin" || !deps.discoverTerminalApps) return Promise.resolve();
		if (!terminalRefresh) {
			terminalRefresh = deps.discoverTerminalApps().then((apps) => { macApps = apps; }).catch((error) => {
				deps.logError?.("failed to discover terminal apps", error);
			}).finally(() => { terminalRefresh = undefined; });
		}
		return terminalRefresh;
	};
	const resolveEditors = () => EDITOR_CANDIDATES.flatMap((candidate) => {
		const command = resolveEditor(candidate, deps, isExecutable, isDirectory);
		return command ? [{ target: { id: candidate.id, name: candidate.name, kind: "editor" } as OpenTarget, command }] : [];
	});
	const resolveTerminals = () => {
		const terminal = resolveTerminal(deps, isExecutable);
		return [...(terminal ? [terminal] : []), ...resolveInstalledTerminals(deps, isExecutable, isDirectory, macApps)];
	};

	const openTarget = async (target: OpenTarget, resolved: ResolvedCommand | undefined, directory: string, revealPath?: string) => {
		try {
			if (target.kind === "file_manager") {
				await (revealPath ? deps.revealPath(revealPath) : deps.openDirectory(directory));
			} else {
				if (!resolved) throw new Error("target command was not resolved");
				await deps.launch(resolved.command, resolved.argsForWorkspace(directory), directory);
			}
		} catch (error) {
			deps.logError?.(`failed to open session target ${target.id}`, error);
			throw new Error(`Could not open ${target.name}. Check that it is installed and try again.`);
		}
	};

	const workspaceUnavailable = (error: unknown) =>
		error instanceof Error && error.message.trim() ? error.message : "Session workspace is not available.";
	const workspaceUnavailableCode = (error: unknown) => {
		const code = error instanceof Error ? (error as { code?: unknown }).code : undefined;
		return typeof code === "string" && code ? code : undefined;
	};

	return {
		async getState(sessionId, options) {
			if (options?.refreshTerminals === true) await refreshTerminals();
			const editors = resolveEditors();
			const terminals = resolveTerminals();
			// Linux's generic launcher may be the same binary (or an alternatives
			// symlink) as a named terminal. Show it only as a fallback in the menu;
			// the generic target remains available to existing shortcut/artifact callers.
			const menuTerminals = deps.platform === "linux" && terminals.some(({ target }) => target.id !== "terminal")
				? terminals.filter(({ target }) => target.id !== "terminal") : terminals;
			const targets = [...editors.map(({ target }) => target), fileManager, ...menuTerminals.map(({ target }) => target)];
			const targetsWithIcons = await Promise.all(targets.map(async (target) => {
				if (target.iconDataUrl) return target;
				const terminal = terminals.find((entry) => entry.target.id === target.id);
				const iconPath = terminal
					? terminal.command.iconPath ?? terminal.command.command
					: target.kind === "file_manager" && deps.platform === "darwin" ? "/System/Library/CoreServices/Finder.app" : undefined;
				const iconDataUrl = iconPath ? await readIcon(iconPath) : undefined;
				return iconDataUrl ? { ...target, iconDataUrl } : target;
			}));
			const preferredEditorId = await deps.readPreference();
			try {
				await deps.resolveWorkspace(sessionId);
				return { targets: targetsWithIcons, preferredEditorId, workspaceAvailable: true };
			} catch (error) {
				return {
					targets: targetsWithIcons,
					preferredEditorId,
					workspaceAvailable: false,
					unavailableReason: workspaceUnavailable(error),
					unavailableCode: workspaceUnavailableCode(error),
				};
			}
		},

		async open(input) {
			const sessionId = input.sessionId.trim();
			if (!sessionId) throw new Error("Session is required.");
			const preferredEditorId = await deps.readPreference();
			const targetId = input.targetId ?? preferredEditorId;
			if (input.targetId && input.targetId !== "file-manager" && !isTerminalId(input.targetId) && !isEditorId(input.targetId)) {
				throw new Error("That open target is not supported.");
			}
			const entry = targetId === "file-manager" ? undefined
				: (isTerminalId(targetId) ? resolveTerminals() : resolveEditors()).find(({ target }) => target.id === targetId);
			const target = targetId === "file-manager" ? fileManager : entry?.target;
			if (!target) {
				if (isEditorId(targetId)) throw new Error("That editor is not installed. Choose another option.");
				throw new Error("That open target is not available.");
			}
			const workspacePath = await deps.resolveWorkspace(sessionId);
			const resolved = entry?.command;
			await openTarget(target, resolved, workspacePath);
			if (target.kind === "editor") await deps.writePreference(target.id as EditorId);
			return target;
		},

		async openArtifacts(input) {
			const sessionId = input.sessionId.trim();
			if (!sessionId) throw new Error("Session is required.");
			if (input.targetId !== "file-manager" && input.targetId !== "terminal") {
				throw new Error("That open target is not supported.");
			}
			const terminal = input.targetId === "terminal" ? resolveTerminal(deps, isExecutable) : undefined;
			const target = input.targetId === "file-manager" ? fileManager : terminal?.target;
			if (!target) throw new Error("That open target is not available.");
			const artifactDir = await deps.resolveArtifactDir(sessionId);
			const filePath = input.path === undefined ? undefined : await artifactFilePath(pathAPI(deps.platform), deps.realPath, artifactDir, input.path);
			await openTarget(target, terminal?.command, filePath ? pathAPI(deps.platform).dirname(filePath) : artifactDir, filePath);
			return target;
		},

		async copyWorkspacePath(sessionId) {
			const id = sessionId.trim();
			if (!id) throw new Error("Session is required.");
			if (!deps.copyText) throw new Error("Copying is not available.");
			await deps.copyText(await deps.resolveWorkspace(id));
		},
	};
}
