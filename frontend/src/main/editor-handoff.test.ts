// @vitest-environment node
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { createEditorHandoff, type EditorHandoffDeps } from "./editor-handoff";

function deps(overrides: Partial<EditorHandoffDeps> = {}): EditorHandoffDeps {
	return {
		platform: "darwin",
		env: { PATH: "/bin" },
		homeDir: "/Users/tester",
		resolveWorkspace: vi.fn().mockResolvedValue("/worktrees/ao-1"),
		resolveArtifactDir: vi.fn().mockResolvedValue("/ao/data/artifacts/ao-1"),
		readPreference: vi.fn().mockResolvedValue("cursor"),
		writePreference: vi.fn().mockResolvedValue(undefined),
		launch: vi.fn().mockResolvedValue(undefined),
		openDirectory: vi.fn().mockResolvedValue(undefined),
		revealPath: vi.fn().mockResolvedValue(undefined),
		realPath: vi.fn(async (candidatePath: string) => candidatePath),
		isExecutable: (candidatePath) => candidatePath === "/bin/code",
		isDirectory: (candidatePath) => candidatePath === "/Applications/Cursor.app",
		...overrides,
	};
}

// Simulate Windows paths consistently on every test host.
function winDeps(overrides: Partial<EditorHandoffDeps> = {}): EditorHandoffDeps {
	return deps({
		platform: "win32",
		env: {
			PATH: path.win32.join("C", "bin"),
			LOCALAPPDATA: path.win32.join("C:", "Users", "tester", "AppData", "Local"),
			ProgramFiles: path.win32.join("C:", "Program Files"),
			PATHEXT: ".COM;.EXE;.BAT;.CMD",
			ComSpec: "C:\\Windows\\System32\\cmd.exe",
		},
		homeDir: path.win32.join("C:", "Users", "tester"),
		isDirectory: () => false,
		...overrides,
	});
}

function installedExecutables(...expectedPaths: string[]) {
	const expected = new Set(expectedPaths);
	return (candidatePath: string) => expected.has(candidatePath);
}

describe("editor handoff", () => {
	it("detects Dock-installed apps and keeps Finder and Terminal as safe fallbacks", async () => {
		const handoff = createEditorHandoff(deps());
		const state = await handoff.getState("ao-1");
		expect(state).toMatchObject({ preferredEditorId: "cursor", workspaceAvailable: true });
		expect(state.targets.map(({ id }) => id)).toEqual(["cursor", "vscode", "file-manager", "terminal"]);
	});

	it.each(["/Applications", "/Users/tester/Applications"])("discovers Ghostty in %s without a CLI on PATH and sends the exact workspace as a folder-open event", async (root) => {
		const workspace = "/work trees/it's $safe; $(touch nope)";
		const input = deps({
			isExecutable: () => false,
			isDirectory: (value) => value === `${root}/Ghostty.app`,
			resolveWorkspace: vi.fn().mockResolvedValue(workspace),
		});
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets).toContainEqual({ id: "ghostty", name: "Ghostty", kind: "terminal" });
		await handoff.open({ sessionId: "ao-1", targetId: "ghostty" });
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", ["-a", `${root}/Ghostty.app`, workspace], workspace);
		expect(input.writePreference).not.toHaveBeenCalled();
	});

	it.each([
		["iterm2", "iTerm", (dir: string) => ["-a", "/Applications/iTerm.app", dir]],
		["wezterm", "WezTerm", (dir: string) => ["-n", "-a", "/Applications/WezTerm.app", "--args", "start", "--cwd", dir]],
		["kitty", "kitty", (dir: string) => ["-n", "-a", "/Applications/kitty.app", "--args", "--directory", dir]],
		["alacritty", "Alacritty", (dir: string) => ["-n", "-a", "/Applications/Alacritty.app", "--args", "--working-directory", dir]],
	] as const)("opens a detected macOS %s app in the workspace", async (id, app, args) => {
		const input = deps({ isExecutable: () => false, isDirectory: (value) => value === `/Applications/${app}.app` });
		await createEditorHandoff(input).open({ sessionId: "ao-1", targetId: id });
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", args("/worktrees/ao-1"), "/worktrees/ao-1");
	});

	it.each([
		["ghostty", ["--working-directory=/worktrees/ao-1"]],
		["wezterm", ["start", "--cwd", "/worktrees/ao-1"]],
		["kitty", ["--directory", "/worktrees/ao-1"]],
		["alacritty", ["--working-directory", "/worktrees/ao-1"]],
		["gnome-terminal", ["--working-directory=/worktrees/ao-1"]],
		["konsole", ["--workdir", "/worktrees/ao-1"]],
		["xfce4-terminal", ["--working-directory=/worktrees/ao-1"]],
	] as const)("discovers and launches Linux %s with its directory argument", async (id, args) => {
		const input = deps({ platform: "linux", isExecutable: installedExecutables(`/bin/${id}`), isDirectory: () => false });
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets.some((target) => target.id === id)).toBe(true);
		await handoff.open({ sessionId: "ao-1", targetId: id });
		expect(input.launch).toHaveBeenCalledWith(`/bin/${id}`, args, "/worktrees/ao-1");
	});

	it.each([false, true])("lists GNOME once when x-terminal-emulator is installed: %s", async (alternatives) => {
		const input = deps({ platform: "linux", env: { PATH: "/usr/bin" }, isDirectory: () => false,
			isExecutable: installedExecutables("/usr/bin/gnome-terminal", ...(alternatives ? ["/usr/bin/x-terminal-emulator"] : [])) });
		const handoff = createEditorHandoff(input);
		const terminals = (await handoff.getState("ao-1")).targets.filter((target) => target.kind === "terminal");
		expect(terminals).toEqual([{ id: "gnome-terminal", name: "GNOME Terminal", kind: "terminal" }]);
		await handoff.open({ sessionId: "ao-1", targetId: "gnome-terminal" });
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/gnome-terminal", ["--working-directory=/worktrees/ao-1"], "/worktrees/ao-1");
	});

	it("keeps the generic Linux menu fallback when no named terminal resolves", async () => {
		const input = deps({ platform: "linux", isDirectory: () => false, isExecutable: installedExecutables("/bin/x-terminal-emulator") });
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets.filter((target) => target.kind === "terminal"))
			.toEqual([{ id: "terminal", name: "Terminal", kind: "terminal" }]);
		await handoff.open({ sessionId: "ao-1", targetId: "terminal" });
		expect(input.launch).toHaveBeenCalledWith("/bin/x-terminal-emulator", [], "/worktrees/ao-1");
	});

	it("retains distinct named Linux terminals and the generic target for existing callers", async () => {
		const input = deps({ platform: "linux", isDirectory: () => false,
			isExecutable: installedExecutables("/bin/gnome-terminal", "/bin/kitty", "/bin/x-terminal-emulator") });
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets.filter((target) => target.kind === "terminal").map((target) => target.id))
			.toEqual(["kitty", "gnome-terminal"]);
		await handoff.open({ sessionId: "ao-1", targetId: "terminal" });
		expect(input.launch).toHaveBeenCalledWith("/bin/x-terminal-emulator", [], "/worktrees/ao-1");
		await handoff.openArtifacts({ sessionId: "ao-1", targetId: "terminal" });
		expect(input.launch).toHaveBeenLastCalledWith("/bin/x-terminal-emulator", [], "/ao/data/artifacts/ao-1");
	});

	it("restores the generic Linux menu fallback when the named terminal is removed", async () => {
		let installed = true;
		const handoff = createEditorHandoff(deps({ platform: "linux", isDirectory: () => false,
			isExecutable: (value) => value === "/bin/x-terminal-emulator" || (installed && value === "/bin/gnome-terminal") }));
		expect((await handoff.getState("ao-1")).targets.filter((target) => target.kind === "terminal").map((target) => target.id)).toEqual(["gnome-terminal"]);
		installed = false;
		expect((await handoff.getState("ao-1")).targets.filter((target) => target.kind === "terminal").map((target) => target.id)).toEqual(["terminal"]);
	});

	it("discovers Windows Terminal from PATH", async () => {
		const input = winDeps({ isExecutable: installedExecutables("C\\bin\\wt.exe") });
		await createEditorHandoff(input).open({ sessionId: "ao-1", targetId: "windows-terminal" });
		expect(input.launch).toHaveBeenCalledWith("C\\bin\\wt.exe", ["-d", "/worktrees/ao-1"], "/worktrees/ao-1");
	});

	it("rechecks terminal installation before launch and never launches a removed target", async () => {
		let installed = true;
		const input = deps({ isDirectory: (value) => installed && value === "/Applications/Ghostty.app" });
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets.some((target) => target.id === "ghostty")).toBe(true);
		installed = false;
		await expect(handoff.open({ sessionId: "ao-1", targetId: "ghostty" })).rejects.toThrow("not available");
		expect(input.launch).not.toHaveBeenCalled();
	});

	it.each(["Warp", "Future Terminal"])("discovers %s from OS capabilities without adding its name to the registry", async (name) => {
		const appPath = `/Volumes/Tools/${name}.app`;
		const app = { bundleId: `org.example.${name.replaceAll(" ", "-")}`, name, appPath };
		const discoverTerminalApps = vi.fn().mockResolvedValue([app]);
		const workspace = "/work trees/it's $safe; $(touch nope)";
		const input = deps({ discoverTerminalApps, isDirectory: (value) => value === appPath,
			resolveWorkspace: vi.fn().mockResolvedValue(workspace), readIcon: vi.fn().mockResolvedValue("data:image/png;base64,dGVzdA==") });
		const handoff = createEditorHandoff(input);
		const state = await handoff.getState("ao-1", { refreshTerminals: true });
		const target = state.targets.find((target) => target.name === name)!;
		expect(target).toMatchObject({ kind: "terminal", iconDataUrl: "data:image/png;base64,dGVzdA==" });
		expect(target.id).toMatch(/^terminal-app:[a-f0-9]{64}$/);
		expect(JSON.stringify(state)).not.toContain(appPath);
		await handoff.open({ sessionId: "ao-1", targetId: target.id });
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", ["-a", appPath, workspace], workspace);
		expect(input.writePreference).not.toHaveBeenCalled();
		discoverTerminalApps.mockResolvedValue([]);
		await handoff.getState("ao-1", { refreshTerminals: true });
		await expect(handoff.open({ sessionId: "ao-1", targetId: target.id })).rejects.toThrow("not available");
		expect(input.launch).toHaveBeenCalledTimes(1);
	});

	it("prefers extracted bundle icons over Electron's generic file icon", async () => {
		const iconDataUrl = "data:image/png;base64,YWN0dWFs";
		const readIcon = vi.fn().mockResolvedValue("data:image/png;base64,cGxhY2Vob2xkZXI=");
		const input = deps({ readIcon, isDirectory: () => true, discoverTerminalApps: vi.fn().mockResolvedValue([
			{ bundleId: "dev.warp.Warp-Stable", name: "Warp", appPath: "/Applications/Warp.app", iconDataUrl },
		]) });
		const { targets } = await createEditorHandoff(input).getState("ao-1", { refreshTerminals: true });
		expect(targets.find((target) => target.name === "Warp")).toMatchObject({ iconDataUrl });
		expect(readIcon).not.toHaveBeenCalledWith("/Applications/Warp.app");
	});

	it("discovers new installations on menu refresh and deduplicates existing built-in targets", async () => {
		const discoverTerminalApps = vi.fn().mockResolvedValue([]);
		const handoff = createEditorHandoff(deps({ discoverTerminalApps, isDirectory: () => true }));
		expect((await handoff.getState("ao-1", { refreshTerminals: true })).targets.some((target) => target.name === "Warp")).toBe(false);
		discoverTerminalApps.mockResolvedValue([
			{ bundleId: "com.apple.Terminal", name: "Terminal", appPath: "/System/Applications/Utilities/Terminal.app" },
			{ bundleId: "com.mitchellh.ghostty", name: "Ghostty", appPath: "/Applications/Ghostty.app" },
			{ bundleId: "dev.warp.Warp-Stable", name: "Warp", appPath: "/Applications/Warp.app" },
		]);
		const { targets } = await handoff.getState("ao-1", { refreshTerminals: true });
		for (const name of ["Terminal", "Ghostty", "Warp"]) expect(targets.filter((target) => target.name === name)).toHaveLength(1);
	});

	it("resolves known terminal adapters in nonstandard install locations", async () => {
		const appPath = "/Volumes/External Apps/WezTerm.app";
		const input = deps({ isDirectory: (value) => value === appPath, discoverTerminalApps: vi.fn().mockResolvedValue([
			{ bundleId: "com.github.wez.wezterm", name: "WezTerm", appPath },
		]) });
		const handoff = createEditorHandoff(input);
		await handoff.getState("ao-1", { refreshTerminals: true });
		await handoff.open({ sessionId: "ao-1", targetId: "wezterm" });
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", ["-n", "-a", appPath, "--args", "start", "--cwd", "/worktrees/ao-1"], "/worktrees/ao-1");
	});

	it("preserves safe fallbacks when OS discovery fails and rejects unregistered dynamic IDs", async () => {
		const handoff = createEditorHandoff(deps({ discoverTerminalApps: vi.fn().mockRejectedValue(new Error("Timed out")) }));
		expect((await handoff.getState("ao-1", { refreshTerminals: true })).targets.some((target) => target.id === "terminal")).toBe(true);
		await expect(handoff.open({ sessionId: "ao-1", targetId: `terminal-app:${"a".repeat(64)}` })).rejects.toThrow("not available");
		await expect(handoff.open({ sessionId: "ao-1", targetId: "terminal-app:/tmp/untrusted" as never })).rejects.toThrow("not supported");
	});

	it("never runs macOS discovery on Linux or Windows", async () => {
		const discoverTerminalApps = vi.fn();
		for (const platform of ["linux", "win32"] as const) await createEditorHandoff(deps({ platform, discoverTerminalApps })).getState("ao-1", { refreshTerminals: true });
		expect(discoverTerminalApps).not.toHaveBeenCalled();
	});

	it("caches discovery across sessions and polling, refreshing only for an explicit menu request", async () => {
		const app = { bundleId: "org.example.shell", name: "Shell", appPath: "/Applications/Shell.app" };
		const discoverTerminalApps = vi.fn().mockResolvedValue([app]);
		const handoff = createEditorHandoff(deps({ discoverTerminalApps, isDirectory: () => true }));
		await handoff.getState("ao-1");
		expect(discoverTerminalApps).not.toHaveBeenCalled();
		await handoff.getState("ao-1", { refreshTerminals: true });
		for (let i = 0; i < 10; i++) {
			expect((await handoff.getState(`ao-${i}`)).targets.some((target) => target.name === "Shell")).toBe(true);
		}
		expect(discoverTerminalApps).toHaveBeenCalledTimes(1);
		await handoff.getState("ao-2", { refreshTerminals: true });
		expect(discoverTerminalApps).toHaveBeenCalledTimes(2);
	});

	it("never blocks editor/Finder launches or readiness checks on an in-flight terminal refresh", async () => {
		let finish!: (apps: []) => void;
		const discoverTerminalApps = vi.fn(() => new Promise<[]>((resolve) => { finish = resolve; }));
		const input = deps({ discoverTerminalApps });
		const handoff = createEditorHandoff(input);
		await handoff.open({ sessionId: "ao-1" });
		await handoff.open({ sessionId: "ao-1", targetId: "file-manager" });
		expect(discoverTerminalApps).not.toHaveBeenCalled();
		const refresh = handoff.getState("ao-1", { refreshTerminals: true });
		const secondRefresh = handoff.getState("ao-2", { refreshTerminals: true });
		await handoff.open({ sessionId: "ao-1", targetId: "cursor" });
		await handoff.open({ sessionId: "ao-1", targetId: "file-manager" });
		await handoff.open({ sessionId: "ao-1", targetId: "terminal" });
		expect((await handoff.getState("ao-3")).workspaceAvailable).toBe(true);
		expect(input.launch).toHaveBeenCalledTimes(3);
		expect(input.openDirectory).toHaveBeenCalledTimes(2);
		expect(discoverTerminalApps).toHaveBeenCalledTimes(1);
		finish([]);
		await Promise.all([refresh, secondRefresh]);
	});

	it("keeps the previous discovery snapshot after a failed refresh but never launches a removed cached app", async () => {
		let installed = true;
		const app = { bundleId: "org.example.shell", name: "Shell", appPath: "/Applications/Shell.app" };
		const discoverTerminalApps = vi.fn().mockResolvedValue([app]);
		const input = deps({ discoverTerminalApps, isDirectory: (value) => installed && value === app.appPath });
		const handoff = createEditorHandoff(input);
		const state = await handoff.getState("ao-1", { refreshTerminals: true });
		const target = state.targets.find((target) => target.name === "Shell")!;
		discoverTerminalApps.mockRejectedValue(new Error("Timed out"));
		expect((await handoff.getState("ao-1", { refreshTerminals: true })).targets).toContainEqual(target);
		await handoff.open({ sessionId: "ao-1", targetId: target.id });
		expect(discoverTerminalApps).toHaveBeenCalledTimes(2);
		installed = false;
		await expect(handoff.open({ sessionId: "ao-1", targetId: target.id })).rejects.toThrow("not available");
		expect(input.launch).toHaveBeenCalledTimes(1);
	});

	it("copies the resolved workspace verbatim without launching an app or changing preferences", async () => {
		const copyText = vi.fn();
		const input = deps({ copyText });
		await createEditorHandoff(input).copyWorkspacePath("ao-1");
		expect(copyText).toHaveBeenCalledWith("/worktrees/ao-1");
		expect(input.launch).not.toHaveBeenCalled();
		expect(input.writePreference).not.toHaveBeenCalled();
	});

	it("does not copy a stale workspace when resolution fails", async () => {
		const copyText = vi.fn();
		const input = deps({ copyText, resolveWorkspace: vi.fn().mockRejectedValue(new Error("Workspace removed")) });
		await expect(createEditorHandoff(input).copyWorkspacePath("ao-1")).rejects.toThrow("Workspace removed");
		expect(copyText).not.toHaveBeenCalled();
	});

	it("returns native Finder and terminal icons without exposing installation paths and caches icon reads", async () => {
		const iconDataUrl = "data:image/png;base64,dGVzdA==";
		const readIcon = vi.fn().mockResolvedValue(iconDataUrl);
		const handoff = createEditorHandoff(deps({ readIcon, isDirectory: (value) => value === "/Applications/Ghostty.app" }));
		const state = await handoff.getState("ao-1");
		expect(readIcon).toHaveBeenCalledWith("/System/Library/CoreServices/Finder.app");
		expect(readIcon).toHaveBeenCalledWith("/System/Applications/Utilities/Terminal.app");
		expect(readIcon).toHaveBeenCalledWith("/Applications/Ghostty.app");
		for (const id of ["file-manager", "terminal", "ghostty"]) {
			expect(state.targets.find((target) => target.id === id)).toMatchObject({ iconDataUrl });
		}
		expect(JSON.stringify(state)).not.toContain(".app");
		await handoff.getState("ao-2");
		expect(readIcon).toHaveBeenCalledTimes(3);
	});

	it("keeps targets usable when native icon lookup fails and retries the lookup", async () => {
		const readIcon = vi.fn().mockRejectedValue(new Error("No icon"));
		const handoff = createEditorHandoff(deps({ readIcon }));
		const state = await handoff.getState("ao-1");
		expect(state.workspaceAvailable).toBe(true);
		expect(state.targets.every((target) => !target.iconDataUrl)).toBe(true);
		readIcon.mockResolvedValue("data:image/png;base64,dGVzdA==");
		expect((await handoff.getState("ao-1")).targets.find((target) => target.id === "terminal")).toHaveProperty("iconDataUrl");
	});

	it.each(["data:image/png;base64,", "https://example.test/icon.png"])("omits an empty or non-native icon: %s", async (icon) => {
		const handoff = createEditorHandoff(deps({ readIcon: vi.fn().mockResolvedValue(icon) }));
		expect((await handoff.getState("ao-1")).targets.every((target) => !target.iconDataUrl)).toBe(true);
	});

	it("opens Neovim in macOS Terminal with shell-safe workspace quoting", async () => {
		const workspacePath = "/work trees/it's & safe";
		const input = deps({
			resolveWorkspace: vi.fn().mockResolvedValue(workspacePath),
			isExecutable: (candidatePath) => candidatePath === "/bin/nvim",
			isDirectory: () => false,
		});
		const handoff = createEditorHandoff(input);

		const state = await handoff.getState("ao-1");
		expect(state.targets.map(({ id }) => id)).toContain("neovim");
		await handoff.open({ sessionId: "ao-1", targetId: "neovim" });

		expect(input.launch).toHaveBeenCalledWith(
			"/usr/bin/osascript",
			["-e", `tell application "Terminal"\nactivate\ndo script "exec '/bin/nvim' '/work trees/it'\\\\''s & safe'"\nend tell`],
			workspacePath,
		);
	});

	it.each([
		["x-terminal-emulator", ["-e"]],
		["gnome-terminal", ["--"]],
		["konsole", ["-e"]],
		["xfce4-terminal", ["--execute"]],
		["kitty", []],
		["alacritty", ["-e"]],
	] as const)("opens Neovim through the Linux %s launcher", async (terminalCommand, argsBeforeCommand) => {
		const workspacePath = "/work trees/ao-1";
		const input = deps({
			platform: "linux",
			env: { PATH: "/bin" },
			resolveWorkspace: vi.fn().mockResolvedValue(workspacePath),
			isExecutable: (candidatePath) => ["/bin/nvim", `/bin/${terminalCommand}`].includes(candidatePath),
			isDirectory: () => false,
		});
		const handoff = createEditorHandoff(input);

		await handoff.open({ sessionId: "ao-1", targetId: "neovim" });

		expect(input.launch).toHaveBeenCalledWith(
			`/bin/${terminalCommand}`,
			[...argsBeforeCommand, "/bin/nvim", workspacePath],
			workspacePath,
		);
	});

	it("opens Neovim through Command Prompt on Windows without expanding workspace percent sequences", async () => {
		const workspacePath = "C:\\work trees\\%TEMP% & fix";
		const input = deps({
			platform: "win32",
			env: {
				PATH: "C:\\bin",
				PATHEXT: ".EXE",
				ComSpec: "C:\\Windows\\System32\\cmd.exe",
			},
			homeDir: "C:\\Users\\tester",
			resolveWorkspace: vi.fn().mockResolvedValue(workspacePath),
			isExecutable: (candidatePath) => candidatePath === "C:\\bin\\nvim.exe",
			isDirectory: () => false,
		});
		const handoff = createEditorHandoff(input);

		await handoff.open({ sessionId: "ao-1", targetId: "neovim" });

		expect(input.launch).toHaveBeenCalledWith(
			"C:\\Windows\\System32\\cmd.exe",
			["/d", "/s", "/v:off", "/k", `""C:\\bin\\nvim.exe" ".""`],
			workspacePath,
		);
	});

	it("reports a missing workspace without hiding the available targets", async () => {
		const missing = Object.assign(new Error("Session workspace is not available."), {
			code: "SESSION_WORKSPACE_NOT_FOUND",
		});
		const handoff = createEditorHandoff(deps({
			resolveWorkspace: vi.fn().mockRejectedValue(missing),
		}));
		const state = await handoff.getState("ao-1");
		expect(state.workspaceAvailable).toBe(false);
		expect(state.unavailableReason).toBe("Session workspace is not available.");
		expect(state.unavailableCode).toBe("SESSION_WORKSPACE_NOT_FOUND");
		expect(state.targets).toHaveLength(4);
	});

	it("opens only the workspace root and persists a chosen editor", async () => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await expect(handoff.open({ sessionId: "ao-1", targetId: "vscode" })).resolves.toMatchObject({
			id: "vscode",
			kind: "editor",
		});
		expect(input.launch).toHaveBeenCalledWith("/bin/code", ["/worktrees/ao-1"], "/worktrees/ao-1");
		expect(input.writePreference).toHaveBeenCalledWith("vscode");
	});

	it("opens Finder without changing the editor preference", async () => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await handoff.open({ sessionId: "ao-1", targetId: "file-manager" });
		expect(input.openDirectory).toHaveBeenCalledWith("/worktrees/ao-1");
		expect(input.writePreference).not.toHaveBeenCalled();
	});

	it("opens the artifact directory in Finder and Terminal without the workspace", async () => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "file-manager" })).resolves.toMatchObject({ kind: "file_manager" });
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "terminal" })).resolves.toMatchObject({ kind: "terminal" });
		expect(input.openDirectory).toHaveBeenCalledWith("/ao/data/artifacts/ao-1");
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", ["-a", "Terminal", "/ao/data/artifacts/ao-1"], "/ao/data/artifacts/ao-1");
		expect(input.resolveWorkspace).not.toHaveBeenCalled();
		expect(input.writePreference).not.toHaveBeenCalled();
	});

	it("reveals one artifact in Finder and opens Terminal in its folder", async () => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await handoff.openArtifacts({ sessionId: "ao-1", targetId: "file-manager", path: "plans/notes.md" });
		await handoff.openArtifacts({ sessionId: "ao-1", targetId: "terminal", path: "plans/notes.md" });
		expect(input.revealPath).toHaveBeenCalledWith("/ao/data/artifacts/ao-1/plans/notes.md");
		expect(input.openDirectory).not.toHaveBeenCalled();
		expect(input.launch).toHaveBeenCalledWith("/usr/bin/open", ["-a", "Terminal", "/ao/data/artifacts/ao-1/plans"], "/ao/data/artifacts/ao-1/plans");
	});

	it.each(["../ao-2/secret.md", "/etc/passwd", "plans/../../x.md", ""])("refuses an artifact path outside the artifact directory: %j", async (artifactPath) => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "file-manager", path: artifactPath })).rejects.toThrow(
			"That artifact is not valid.",
		);
		expect(input.revealPath).not.toHaveBeenCalled();
	});

	it("refuses an artifact whose symlinks resolve outside the artifact directory", async () => {
		const input = deps({
			realPath: vi.fn(async (candidatePath: string) => candidatePath.replace("/ao/data/artifacts/ao-1/plans", "/elsewhere")),
		});
		const handoff = createEditorHandoff(input);
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "terminal", path: "plans/notes.md" })).rejects.toThrow(
			"That artifact is not valid.",
		);
		expect(input.launch).not.toHaveBeenCalled();
	});

	it("says when the artifact is gone", async () => {
		const input = deps({
			realPath: vi.fn(async (candidatePath: string) => {
				if (candidatePath.endsWith("notes.md")) throw new Error("ENOENT");
				return candidatePath;
			}),
		});
		const handoff = createEditorHandoff(input);
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "file-manager", path: "notes.md" })).rejects.toThrow(
			"That artifact no longer exists.",
		);
	});

	it("refuses to open artifacts in an editor", async () => {
		const input = deps();
		const handoff = createEditorHandoff(input);
		await expect(handoff.openArtifacts({ sessionId: "ao-1", targetId: "vscode" as never })).rejects.toThrow(
			"That open target is not supported.",
		);
		expect(input.resolveArtifactDir).not.toHaveBeenCalled();
	});

	it("does not silently replace a missing preferred editor", async () => {
		const handoff = createEditorHandoff(deps({
			isExecutable: () => false,
			isDirectory: () => false,
		}));
		await expect(handoff.open({ sessionId: "ao-1" })).rejects.toThrow(
			"That editor is not installed. Choose another option.",
		);
	});

	it("turns a launcher failure into a visible path-free error", async () => {
		const input = deps({ launch: vi.fn().mockRejectedValue(new Error("/private/path failed")) });
		const handoff = createEditorHandoff(input);
		await expect(handoff.open({ sessionId: "ao-1", targetId: "vscode" })).rejects.toThrow(
			"Could not open VS Code. Check that it is installed and try again.",
		);
	});

	it.each(["Antigravity IDE", "Antigravity"])(
		"finds Antigravity IDE on macOS via %s.app bundle and launches with open -a",
		async (bundleName) => {
			const input = deps({
				platform: "darwin",
				isExecutable: () => false,
				isDirectory: (candidatePath) => candidatePath === `/Applications/${bundleName}.app`,
			});
			const handoff = createEditorHandoff(input);
			const state = await handoff.getState("ao-1");
			const antigravity = state.targets.find(({ id }) => id === "antigravity");
			expect(antigravity).toEqual({ id: "antigravity", name: "Antigravity IDE", kind: "editor" });

			await handoff.open({ sessionId: "ao-1", targetId: "antigravity" });
			expect(input.launch).toHaveBeenCalledWith(
				"/usr/bin/open",
				["-a", bundleName, "/worktrees/ao-1"],
				"/worktrees/ao-1",
			);
		},
	);

	it.each(["antigravity-ide", "antigravity"])(
		"finds Antigravity IDE on Linux via %s on PATH",
		async (command) => {
			const input = deps({
				platform: "linux",
				env: { PATH: "/usr/bin" },
				isExecutable: (candidatePath) => candidatePath === `/usr/bin/${command}`,
				isDirectory: () => false,
			});
			const handoff = createEditorHandoff(input);
			const state = await handoff.getState("ao-1");
			const antigravity = state.targets.find(({ id }) => id === "antigravity");
			expect(antigravity).toEqual({ id: "antigravity", name: "Antigravity IDE", kind: "editor" });

			await handoff.open({ sessionId: "ao-1", targetId: "antigravity" });
			expect(input.launch).toHaveBeenCalledWith(
				`/usr/bin/${command}`,
				["/worktrees/ao-1"],
				"/worktrees/ao-1",
			);
		},
	);
});

describe("editor handoff (win32 fallback discovery)", () => {
	// Cursor's Windows per-user install keeps its .cmd shim under
	// resources\app\bin (the VS Code fork layout), not a top-level bin dir.
	const cursorBin = path.win32.join("C:", "Users", "tester", "AppData", "Local", "Programs", "Cursor", "resources", "app", "bin", "cursor.cmd");
	const vscodeSystemBin = path.win32.join("C:", "Program Files", "Microsoft VS Code", "bin", "code.cmd");
	const vscodeAgentExec = path.win32.join("C:", "Program Files", "Microsoft VS Code", "bin", "code.exe");

	it("finds an editor that is present only in the per-user LOCALAPPDATA install dir", async () => {
		const handoff = createEditorHandoff(winDeps({ isExecutable: installedExecutables(cursorBin) }));
		const state = await handoff.getState("ao-1");
		const cursor = state.targets.find(({ id }) => id === "cursor");
		expect(cursor).toBeDefined();
	});

	it("finds Antigravity IDE when present in LOCALAPPDATA install dir", async () => {
		const antigravityBin = path.win32.join("C:", "Users", "tester", "AppData", "Local", "Programs", "Antigravity IDE", "bin", "antigravity-ide.cmd");
		const handoff = createEditorHandoff(winDeps({ isExecutable: installedExecutables(antigravityBin) }));
		const state = await handoff.getState("ao-1");
		const antigravity = state.targets.find(({ id }) => id === "antigravity");
		expect(antigravity).toEqual({ id: "antigravity", name: "Antigravity IDE", kind: "editor" });
	});

	it("prefers a Windows-native .cmd shim over a bare extension-less sh script on PATH", async () => {
		// VS Code/Cursor ship a bare `code` sh script and a `code.cmd` batch
		// side by side; spawn must use the .cmd so cmd.exe can run it.
		const dir = path.win32.join("C", "bin");
		const shScript = path.win32.join(dir, "code");
		const cmdShim = path.win32.join(dir, "code.cmd");
		const input = winDeps({ isExecutable: installedExecutables(shScript, cmdShim) });
		const handoff = createEditorHandoff(input);
		await handoff.open({ sessionId: "ao-1", targetId: "vscode" });
		expect(input.launch).toHaveBeenCalledWith(cmdShim, ["/worktrees/ao-1"], "/worktrees/ao-1");
	});

	it("finds an editor installed only in a system Program Files dir", async () => {
		const handoff = createEditorHandoff(winDeps({ isExecutable: installedExecutables(vscodeSystemBin) }));
		const state = await handoff.getState("ao-1");
		const vscode = state.targets.find(({ id }) => id === "vscode");
		expect(vscode).toBeDefined();
	});

	it("prefers a PATH install over the fallback install dirs", async () => {
		const pathCode = path.win32.join("C", "bin", "code.cmd");
		const input = winDeps({ isExecutable: installedExecutables(pathCode, vscodeAgentExec) });
		const handoff = createEditorHandoff(input);
		await handoff.open({ sessionId: "ao-1", targetId: "vscode" });
		expect(input.launch).toHaveBeenCalledWith(pathCode, ["/worktrees/ao-1"], "/worktrees/ao-1");
	});

	it("resolves a .cmd shim from an install dir via PATHEXT", async () => {
		const handoff = createEditorHandoff(winDeps({ isExecutable: installedExecutables(vscodeSystemBin) }));
		const state = await handoff.getState("ao-1");
		expect(state.targets.some(({ id }) => id === "vscode")).toBe(true);
	});

	it("safely ignores nonexistent fallback roots (unset env vars) instead of throwing", async () => {
		const input = winDeps({
			env: {
				PATH: path.win32.join("C", "bin"),
				PATHEXT: ".COM;.EXE;.BAT;.CMD",
			},
			isExecutable: () => false,
		});
		const handoff = createEditorHandoff(input);
		const state = await handoff.getState("ao-1");
		expect(state.targets).toEqual([
			{ id: "file-manager", name: "File Explorer", kind: "file_manager" },
			{ id: "terminal", name: "Command Prompt", kind: "terminal" },
		]);
	});

	it("leaves editors unavailable on win32 when neither PATH nor install dirs match", async () => {
		const handoff = createEditorHandoff(winDeps({ isExecutable: () => false }));
		const state = await handoff.getState("ao-1");
		expect(state.targets.some(({ id }) => id === "vscode")).toBe(false);
		expect(state.targets.some(({ id }) => id === "cursor")).toBe(false);
	});

	it("does not scan install dirs on non-win32 platforms", async () => {
		const macHandoff = createEditorHandoff(deps({ isExecutable: () => false, isDirectory: () => false }));
		const state = await macHandoff.getState("ao-1");
		expect(state.targets.some(({ id }) => id === "vscode")).toBe(false);
	});

	it("still resolves an editor on linux via its extra search dirs (not the win32 fallback)", async () => {
		const code = path.posix.join("/usr/local/bin", "code");
		const input = deps({
			platform: "linux",
			env: { PATH: "/bin" },
			isExecutable: (candidatePath) => candidatePath === code,
			isDirectory: () => false,
		});
		const state = await createEditorHandoff(input).getState("ao-1");
		expect(state.targets.some(({ id }) => id === "vscode")).toBe(true);
	});

	it("re-resolves editors installed after handoff creation instead of freezing at startup", async () => {
		const installed = new Set<string>();
		const input = winDeps({
			isExecutable: (candidatePath) => installed.has(candidatePath),
			isDirectory: () => false,
		});
		const handoff = createEditorHandoff(input);
		expect((await handoff.getState("ao-1")).targets.some(({ id }) => id === "cursor")).toBe(false);

		installed.add(cursorBin);
		expect((await handoff.getState("ao-1")).targets.some(({ id }) => id === "cursor")).toBe(true);
	});
});
