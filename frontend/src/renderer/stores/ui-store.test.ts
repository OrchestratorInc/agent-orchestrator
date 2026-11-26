import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FINISHED_TASK_TABS_KEPT, failedTaskKey, rememberedFileDisplayMode, sidebarIsVisible, sidebarOccupiesLayout, useUiStore } from "./ui-store";
import { sessionUiKey } from "../lib/hosts";
import { previewDocumentThemeStyle } from "../lib/theme";
import { aoBridge } from "../lib/bridge";

describe("sidebar visibility", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ isSidebarOpen: true });
	});

	it("changes only through the explicit toggle and persists the preference", () => {
		useUiStore.getState().toggleSidebar();

		let state = useUiStore.getState();
		expect(state.isSidebarOpen).toBe(false);
		expect(sidebarIsVisible(state)).toBe(false);
		expect(sidebarOccupiesLayout(state)).toBe(false);
		expect(window.localStorage.getItem("ao.sidebar.open")).toBe("false");

		useUiStore.getState().toggleSidebar();
		state = useUiStore.getState();
		expect(state.isSidebarOpen).toBe(true);
		expect(sidebarIsVisible(state)).toBe(true);
		expect(sidebarOccupiesLayout(state)).toBe(true);
		expect(window.localStorage.getItem("ao.sidebar.open")).toBe("true");
	});
});

describe("subagents tab availability", () => {
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("records per session whether its chat has subagents to list", () => {
		const session = sessionUiKey("session-1");
		useUiStore.getState().setSubagentsAvailable(session, true);
		expect(useUiStore.getState().inspectorSessions[session]?.subagentsAvailable).toBe(true);
		expect(useUiStore.getState().inspectorSessions[sessionUiKey("session-2")]).toBeUndefined();

		useUiStore.getState().setSubagentsAvailable(session, false);
		expect(useUiStore.getState().inspectorSessions[session]?.subagentsAvailable).toBe(false);
	});

	it("leaves the store alone when nothing changes, so a chat without subagents adds no session entry", () => {
		const before = useUiStore.getState().inspectorSessions;
		useUiStore.getState().setSubagentsAvailable(sessionUiKey("session-3"), false);
		expect(useUiStore.getState().inspectorSessions).toBe(before);
	});
});

describe("theme preference", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ themePreference: "dark", resolvedTheme: "dark" });
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("resolves System from the OS appearance main reports, not the forced media query", async () => {
		// The test media query reports dark, as a forced Dark themeSource would.
		vi.spyOn(aoBridge.theme, "systemAppearance").mockResolvedValue("light");
		useUiStore.getState().setThemePreference("system");

		await vi.waitFor(() => expect(useUiStore.getState().resolvedTheme).toBe("light"));
		expect(useUiStore.getState().themePreference).toBe("system");
		expect(document.documentElement.dataset.theme).toBe("light");
		expect(window.localStorage.getItem("ao.theme")).toBe("system");
	});

	it("re-reads the OS appearance once main stops forcing a mode", async () => {
		// The OS turns dark between the first lookup and the reveal.
		vi.spyOn(aoBridge.theme, "systemAppearance").mockResolvedValueOnce("light").mockResolvedValue("dark");
		useUiStore.getState().setThemePreference("system");

		await vi.waitFor(() => expect(aoBridge.theme.systemAppearance).toHaveBeenCalledTimes(2));
		await vi.waitFor(() => expect(useUiStore.getState().resolvedTheme).toBe("dark"));
		expect(useUiStore.getState().themePreference).toBe("system");
	});

	it("keeps the OS correction until a deferred update commits System", async () => {
		const updates: (() => void)[] = [];
		Object.defineProperty(document, "startViewTransition", {
			configurable: true,
			writable: true,
			value: (update: () => void) => {
				updates.push(update);
				return { ready: Promise.resolve(), finished: Promise.resolve() };
			},
		});
		try {
			// The OS turns dark between the first lookup and the reveal.
			const lookup = vi.spyOn(aoBridge.theme, "systemAppearance").mockResolvedValueOnce("light").mockResolvedValue("dark");
			useUiStore.getState().setThemePreference("system");
			await vi.waitFor(() => expect(updates).toHaveLength(1));
			// Main has answered theme:set, but the update has not run yet.
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(lookup).toHaveBeenCalledTimes(1);

			updates[0]?.();
			expect(useUiStore.getState().resolvedTheme).toBe("light");
			await vi.waitFor(() => expect(updates).toHaveLength(2));
			updates[1]?.();

			expect(useUiStore.getState().themePreference).toBe("system");
			expect(useUiStore.getState().resolvedTheme).toBe("dark");
		} finally {
			Reflect.deleteProperty(document, "startViewTransition");
		}
	});

	it("skips the second OS lookup when a newer choice lands while System reaches main", async () => {
		const lookup = vi.spyOn(aoBridge.theme, "systemAppearance").mockResolvedValue("light");
		let releaseSet: () => void = () => undefined;
		vi.spyOn(aoBridge.theme, "set").mockImplementationOnce(
			() =>
				new Promise<void>((resolve) => {
					releaseSet = resolve;
				}),
		);
		useUiStore.getState().setThemePreference("system");
		await vi.waitFor(() => expect(useUiStore.getState().themePreference).toBe("system"));
		useUiStore.getState().setThemePreference("dark");
		releaseSet();
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(lookup).toHaveBeenCalledTimes(1);
		expect(useUiStore.getState().themePreference).toBe("dark");
	});

	it("drops a System lookup that a newer choice overtook", async () => {
		let reportAppearance: (theme: "light" | "dark") => void = () => undefined;
		vi.spyOn(aoBridge.theme, "systemAppearance").mockReturnValue(
			new Promise((resolve) => {
				reportAppearance = resolve;
			}),
		);
		useUiStore.getState().setThemePreference("system");
		useUiStore.getState().setThemePreference("light");
		reportAppearance("dark");
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(useUiStore.getState().themePreference).toBe("light");
		expect(useUiStore.getState().resolvedTheme).toBe("light");
	});

	it("drops a System transition whose choice was overtaken before it ran", async () => {
		let runUpdate: (() => void) | undefined;
		Object.defineProperty(document, "startViewTransition", {
			configurable: true,
			writable: true,
			value: (update: () => void) => {
				runUpdate = update;
				return { ready: Promise.resolve(), finished: Promise.resolve() };
			},
		});
		try {
			vi.spyOn(aoBridge.theme, "systemAppearance").mockResolvedValue("light");
			useUiStore.getState().setThemePreference("system");
			await vi.waitFor(() => expect(runUpdate).toBeDefined());
			useUiStore.getState().setThemePreference("dark");
			runUpdate?.();

			expect(useUiStore.getState().themePreference).toBe("dark");
			expect(useUiStore.getState().resolvedTheme).toBe("dark");
		} finally {
			Reflect.deleteProperty(document, "startViewTransition");
		}
	});

	it("lets a newer choice cancel a pending toggle", async () => {
		let reportAppearance: (theme: "light" | "dark") => void = () => undefined;
		vi.spyOn(aoBridge.theme, "systemAppearance").mockReturnValue(
			new Promise((resolve) => {
				reportAppearance = resolve;
			}),
		);
		useUiStore.getState().toggleThemePreference();
		useUiStore.getState().setThemePreference("dark");
		reportAppearance("light");
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(useUiStore.getState().themePreference).toBe("dark");
		expect(useUiStore.getState().resolvedTheme).toBe("dark");
	});

	it("re-sends the current mode to main when it overtakes a System lookup", () => {
		vi.spyOn(aoBridge.theme, "systemAppearance").mockReturnValue(new Promise(() => undefined));
		const set = vi.spyOn(aoBridge.theme, "set");
		useUiStore.getState().setThemePreference("system");
		useUiStore.getState().setThemePreference("dark");

		expect(set).toHaveBeenCalledWith("dark");
		expect(useUiStore.getState().themePreference).toBe("dark");
	});
});

describe("color theme selection", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ themeStyle: "orchestrate" });
		delete document.documentElement.dataset.styleTheme;
	});

	it("persists a previewed theme without a View Transition", () => {
		const startViewTransition = vi.fn();
		Object.defineProperty(document, "startViewTransition", {
			configurable: true,
			writable: true,
			value: startViewTransition,
		});
		try {
			previewDocumentThemeStyle("nord", "orchestrate");
			useUiStore.getState().setThemeStyle("nord");
			previewDocumentThemeStyle(null, useUiStore.getState().themeStyle);

			expect(startViewTransition).not.toHaveBeenCalled();
			expect(useUiStore.getState().themeStyle).toBe("nord");
			expect(window.localStorage.getItem("ao.theme-style")).toBe("nord");
			expect(document.documentElement.dataset.styleTheme).toBe("nord");
		} finally {
			Reflect.deleteProperty(document, "startViewTransition");
			delete document.documentElement.dataset.styleTheme;
		}
	});
});

describe("global settings deep links", () => {
	it("stores an optional harness focus target without changing existing calls", () => {
		useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "codex" });
		expect(useUiStore.getState().settingsModal).toEqual({
			scope: "global",
			section: "harness",
			focusAgentId: "codex",
		});

		useUiStore.getState().openGlobalSettings("mobile");
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "mobile" });
	});

	it("preserves project settings only for explicit recovery navigation", () => {
		useUiStore.getState().openProjectSettings("project-1");
		useUiStore.getState().openGlobalSettings("general");
		useUiStore.getState().closeSettings();
		expect(useUiStore.getState().settingsModal).toBeNull();

		useUiStore.getState().openProjectSettings("project-1");
		useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "codex", preserveProject: true });
		expect(useUiStore.getState().settingsModal).toEqual({
			scope: "global",
			section: "harness",
			focusAgentId: "codex",
			returnTo: { scope: "project", projectId: "project-1" },
		});
		useUiStore.getState().closeSettings();
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "project-1" });
	});

	it("drops a pending account sign-in when settings close or project settings open", () => {
		useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "claude-code", loginAccountId: "acct-b" });
		expect(useUiStore.getState().accountLogin).toBe("acct-b");
		useUiStore.getState().closeSettings();
		expect(useUiStore.getState().accountLogin).toBeNull();

		useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "claude-code", loginAccountId: "acct-b" });
		useUiStore.getState().openProjectSettings("project-1");
		expect(useUiStore.getState().accountLogin).toBeNull();
	});
});

describe("cloud review cleanup", () => {
	it("keeps pane state for the same target and resets only that repository when the target changes", () => {
		useUiStore.setState({ inspectorSessions: {} });
		const store = useUiStore.getState();
		store.setCloudReviewSelection("sess-1", undefined, { scope: "staged" });
		store.setCloudReviewUi("sess-1", undefined, { filter: "src", pane: { selectionKey: "staged", files: { "src/a.ts": { fingerprint: "fp", collapsed: true, loadedDeferred: false } } } });
		store.setCloudReviewUi("sess-1", "sibling", { commitBrowserOpen: true });
		const before = useUiStore.getState();
		store.setCloudReviewSelection("sess-1", undefined, { scope: "staged" });
		expect(useUiStore.getState()).toBe(before);
		store.setCloudReviewSelection("sess-1", undefined, { scope: "combined" });
		const ui = useUiStore.getState().inspectorSessions["sess-1"].cloudReviewUi;
		expect(ui?.[""].pane).toBeUndefined();
		expect(ui?.[""].filter).toBe("src");
		expect(ui?.sibling).toBe(before.inspectorSessions["sess-1"].cloudReviewUi?.sibling);
	});

	it("clears cloud review state at the Files reset boundary without changing another session or inspector preferences", () => {
		useUiStore.setState({ inspectorSessions: {} });
		useUiStore.getState().setCloudReviewSelection("sess-1", "sibling", { scope: "committed", commitSha: "abc123" });
		useUiStore.getState().setCloudReviewRepository("sess-1", "sibling");
		useUiStore.getState().setCloudReviewUi("sess-1", "sibling", { filter: "src", commitBrowserOpen: true });
		useUiStore.getState().setInspectorView("sess-1", "files");
		useUiStore.getState().setCloudReviewSelection("sess-2", undefined, { scope: "staged" });
		const other = useUiStore.getState().inspectorSessions["sess-2"];
		useUiStore.getState().resetFilesSelection("sess-1");
		const state = useUiStore.getState();
		expect(state.inspectorSessions["sess-1"].cloudReviewSelection).toBeUndefined();
		expect(state.inspectorSessions["sess-1"].cloudReviewRepository).toBeUndefined();
		expect(state.inspectorSessions["sess-1"].cloudReviewUi).toBeUndefined();
		expect(state.inspectorSessions["sess-1"].view).toBe("files");
		expect(state.inspectorSessions["sess-2"]).toBe(other);
		useUiStore.getState().resetFilesSelection("sess-1");
		useUiStore.getState().resetFilesSelection("absent");
		expect(useUiStore.getState()).toBe(state);
	});
});

// A fresh module sees exactly what a booting renderer sees: only storage.
async function bootStore() {
	return (await import("./ui-store")).useUiStore;
}

describe("chat send key preference", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
		useUiStore.setState({ chatSendKeyMode: "enter" });
	});

	it("defaults to Enter sends, including for an invalid saved value", async () => {
		expect((await bootStore()).getState().chatSendKeyMode).toBe("enter");
		vi.resetModules();
		window.localStorage.setItem("ao.chatSendKeyMode", "invalid");
		expect((await bootStore()).getState().chatSendKeyMode).toBe("enter");
	});

	it.each(["enter", "mod-enter"] as const)("persists %s across a renderer restart", async (mode) => {
		useUiStore.getState().setChatSendKeyMode(mode);
		expect(useUiStore.getState().chatSendKeyMode).toBe(mode);
		expect(window.localStorage.getItem("ao.chatSendKeyMode")).toBe(mode);
		expect((await bootStore()).getState().chatSendKeyMode).toBe(mode);
	});
});

describe("Cloud review persistence", () => {
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("normalizes the main repository and preserves sibling and session selections", () => {
		const store = useUiStore.getState();
		store.setCloudReviewSelection("sess-1", undefined, { scope: "staged" });
		const before = useUiStore.getState();
		store.setCloudReviewSelection("sess-1", "", { scope: "staged" });
		expect(useUiStore.getState()).toBe(before);
		store.setCloudReviewSelection("sess-1", "11x", { scope: "committed", commitSha: "abc123" });
		store.setCloudReviewSelection("sess-2", undefined, { scope: "combined" });
		expect(useUiStore.getState().inspectorSessions["sess-1"].cloudReviewSelection).toEqual({
			"": { scope: "staged" }, "11x": { scope: "committed", commitSha: "abc123" },
		});
		expect(useUiStore.getState().inspectorSessions["sess-2"].cloudReviewSelection).toEqual({ "": { scope: "combined" } });
	});

	it("remembers the active repository per session without redundant writes", () => {
		const store = useUiStore.getState();
		store.setCloudReviewRepository("sess-1", "11x");
		const before = useUiStore.getState();
		store.setCloudReviewRepository("sess-1", "11x");
		store.setCloudReviewRepository("sess-2", "");
		expect(useUiStore.getState()).toBe(before);
		expect(useUiStore.getState().inspectorSessions["sess-2"]).toBeUndefined();
		store.setCloudReviewRepository("sess-1", "");
		expect(useUiStore.getState().inspectorSessions["sess-1"].cloudReviewRepository).toBe("");
	});
});

describe("remoteHosts flag", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
		useUiStore.setState({ remoteHosts: false });
	});

	it("is on by default behind developer mode", async () => {
		expect((await bootStore()).getState().remoteHosts).toBe(true);
	});

	it("persists the switch so the choice survives a restart", () => {
		useUiStore.getState().setRemoteHosts(true);
		expect(useUiStore.getState().remoteHosts).toBe(true);
		expect(window.localStorage.getItem("ao.remoteHosts")).toBe("true");
		useUiStore.getState().setRemoteHosts(false);
	});

	it("reads a stored choice back at startup", async () => {
		window.localStorage.setItem("ao.remoteHosts", "true");
		expect((await bootStore()).getState().remoteHosts).toBe(true);
	});

	it("honors an explicit opt-out", async () => {
		window.localStorage.setItem("ao.remoteHosts", "false");
		expect((await bootStore()).getState().remoteHosts).toBe(false);
	});
});

describe("retired side chats switch", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
	});

	it("leaves no switch behind and clears a stored opt-out at startup", async () => {
		window.localStorage.setItem("ao.sideChats", "false");

		const state = (await bootStore()).getState();

		expect(state).not.toHaveProperty("sideChats");
		expect(state).not.toHaveProperty("setSideChats");
		expect(window.localStorage.getItem("ao.sideChats")).toBeNull();
	});
});

describe("navigation gestures", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
		useUiStore.setState({ navigationGestures: false });
	});

	it("is on by default", async () => {
		expect((await bootStore()).getState().navigationGestures).toBe(true);
	});

	it("restores the saved choice after restarting", async () => {
		useUiStore.getState().setNavigationGestures(false);
		expect(window.localStorage.getItem("ao.navigationGestures")).toBe("false");
		let store = await bootStore();
		expect(store.getState().navigationGestures).toBe(false);
		store.getState().setNavigationGestures(true);
		vi.resetModules();
		store = await bootStore();
		expect(store.getState().navigationGestures).toBe(true);
	});
});

describe("terminalCopyOnSelect flag", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
		useUiStore.setState({ terminalCopyOnSelect: true });
	});

	it("is on until the user turns it off", async () => {
		expect((await bootStore()).getState().terminalCopyOnSelect).toBe(true);
	});

	it("persists the switch so the choice survives a restart", () => {
		useUiStore.getState().setTerminalCopyOnSelect(false);
		expect(useUiStore.getState().terminalCopyOnSelect).toBe(false);
		expect(window.localStorage.getItem("ao.terminalCopyOnSelect")).toBe("false");
		useUiStore.getState().setTerminalCopyOnSelect(true);
	});

	it("reads a stored opt-out back at startup", async () => {
		window.localStorage.setItem("ao.terminalCopyOnSelect", "false");
		expect((await bootStore()).getState().terminalCopyOnSelect).toBe(false);
	});
});

describe("project UI state across hosts", () => {
	beforeEach(() => {
		useUiStore.setState({
			restartingProjectIds: new Set(),
			provisioningProjectIds: new Set(),
			orchestratorStartupErrors: {},
			newTaskRequest: null,
		});
	});

	it("keeps equal project IDs on two hosts and local separate", () => {
		const state = useUiStore.getState();
		state.setProjectRestarting("project", true, "host-a");
		state.setProjectProvisioning("project", true, "host-a");
		state.setOrchestratorStartupError("project", "Host A failed", "host-a");
		state.setOrchestratorStartupError("project", "Host B failed", "host-b");

		expect(useUiStore.getState().restartingProjectIds.has(sessionUiKey("project", "host-a"))).toBe(true);
		expect(useUiStore.getState().restartingProjectIds.has(sessionUiKey("project", "host-b"))).toBe(false);
		expect(useUiStore.getState().restartingProjectIds.has("project")).toBe(false);
		expect(useUiStore.getState().orchestratorStartupErrors[sessionUiKey("project", "host-b")]).toBe("Host B failed");
		state.setOrchestratorStartupError("project", null, "host-a");
		expect(useUiStore.getState().orchestratorStartupErrors[sessionUiKey("project", "host-b")]).toBe("Host B failed");
	});

	it("blocks a new task only for the project still provisioning on its host", () => {
		const state = useUiStore.getState();
		state.setProjectProvisioning("project", true, "host-a");
		state.requestNewTask("project", "host-a");
		expect(useUiStore.getState().newTaskRequest).toBeNull();
		state.requestNewTask("project", "host-b");
		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: "project", hostId: "host-b" });
	});
});

describe("file display modes", () => {
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("restores a pick only for the same session, path, and open request", () => {
		useUiStore.getState().setFileDisplayMode("sess-1", "README.md", "rendered", 3);
		const state = useUiStore.getState();

		expect(rememberedFileDisplayMode(state, "sess-1", "README.md", 3)).toBe("rendered");
		expect(rememberedFileDisplayMode(state, "sess-1", "README.md", 4)).toBeUndefined();
		expect(rememberedFileDisplayMode(state, "sess-2", "README.md", 3)).toBeUndefined();
		expect(rememberedFileDisplayMode(state, "sess-1", "docs/guide.md", 3)).toBeUndefined();
		expect(rememberedFileDisplayMode(state, "sess-1", null, 3)).toBeUndefined();
	});

	it("keeps the rest of the session's Files state", () => {
		useUiStore.getState().setFilesChangedOnly("sess-1", false);
		useUiStore.getState().setFileDisplayMode("sess-1", "README.md", "rendered", 1);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesChangedOnly).toBe(false);
	});
});

describe("files review selection", () => {
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("shares one pick per session and resets it to the default", () => {
		const selection = { branch: { kind: "branch", name: "main" }, changes: { kind: "commits", shas: ["a".repeat(40)] } } as const;
		useUiStore.getState().setFilesChangedOnly("sess-1", false);
		useUiStore.getState().setFilesSelection("sess-1", selection);
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesSelection).toBe(selection);
		expect(useUiStore.getState().inspectorSessions["sess-2"]?.filesSelection).toBeUndefined();

		useUiStore.getState().resetFilesSelection("sess-1");
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesSelection).toBeUndefined();
		expect(useUiStore.getState().inspectorSessions["sess-1"]?.filesChangedOnly).toBe(false);
	});

	it("leaves the store untouched when nothing changes", () => {
		const selection = { branch: { kind: "checked_out" }, changes: { kind: "staged" } } as const;
		useUiStore.getState().setFilesSelection("sess-1", selection);
		const before = useUiStore.getState().inspectorSessions;
		useUiStore.getState().setFilesSelection("sess-1", selection);
		useUiStore.getState().resetFilesSelection("sess-2");
		expect(useUiStore.getState().inspectorSessions).toBe(before);
	});
});

describe("workspace file open requests", () => {
	beforeEach(() => {
		useUiStore.setState({ workspaceFileOpenRequest: null });
	});

	it("increments its nonce even when the same file is requested twice", () => {
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "src/App.tsx", "host-a");
		const first = useUiStore.getState().workspaceFileOpenRequest;
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "src/App.tsx", "host-a");
		const second = useUiStore.getState().workspaceFileOpenRequest;

		expect(first).toMatchObject({ sessionId: "session-1", hostId: "host-a", path: "src/App.tsx" });
		expect(second?.nonce).toBe((first?.nonce ?? 0) + 1);
	});

	it("stores the artifact source only when one is given", () => {
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "out/report.md", "host-a", "artifact");
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "session-1",
			hostId: "host-a",
			path: "out/report.md",
			source: "artifact",
		});

		useUiStore.getState().requestWorkspaceFileOpen("session-1", "src/App.tsx");
		expect(useUiStore.getState().workspaceFileOpenRequest).not.toHaveProperty("source");
	});

	it("clears only the matching request generation", () => {
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "old.ts");
		const oldNonce = useUiStore.getState().workspaceFileOpenRequest!.nonce;
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "new.ts");
		useUiStore.getState().clearWorkspaceFileOpenRequest(oldNonce);
		expect(useUiStore.getState().workspaceFileOpenRequest?.path).toBe("new.ts");

		useUiStore.getState().clearWorkspaceFileOpenRequest(oldNonce + 1);
		expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();
	});
});

describe("palette search requests", () => {
	it("carries a line with a file open request", () => {
		useUiStore.getState().requestWorkspaceFileOpen("session-1", "src/App.tsx", undefined, undefined, 42);
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "session-1", path: "src/App.tsx", line: 42 });
	});

	it("records and clears a chat message focus request by nonce", () => {
		useUiStore.getState().requestChatMessageFocus("session-1", "m1", "the passage");
		const request = useUiStore.getState().chatMessageFocusRequest;
		expect(request).toMatchObject({ sessionId: "session-1", messageId: "m1", text: "the passage" });
		useUiStore.getState().clearChatMessageFocusRequest((request?.nonce ?? 0) + 1);
		expect(useUiStore.getState().chatMessageFocusRequest).not.toBeNull();
		useUiStore.getState().clearChatMessageFocusRequest(request!.nonce);
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});
});

describe("retired new sidebar design switch", () => {
	beforeEach(() => {
		window.localStorage.clear();
		vi.resetModules();
	});

	it("boots into the one sidebar design for someone who had switched it off", async () => {
		window.localStorage.setItem("ao.developerMode", "true");
		window.localStorage.setItem("ao.newSidebarDesign", "false");

		const state = (await bootStore()).getState();

		expect(state).not.toHaveProperty("newSidebarDesign");
		expect(state).not.toHaveProperty("setNewSidebarDesign");
		expect(window.localStorage.getItem("ao.newSidebarDesign")).toBeNull();
		expect(document.documentElement.hasAttribute("data-legacy-status-colors")).toBe(false);
	});
});

describe("background tasks side panel", () => {
	const id = "task-tabs-session";
	const task = (taskId: string, extra: Record<string, unknown> = {}) => ({ id: taskId, kind: "shell" as const, state: "running" as const, description: `Task ${taskId}`, ...extra });
	const panel = () => useUiStore.getState().inspectorSessions[id];
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: { [id]: { isOpen: false, view: "browser" } } });
	});

	it("opens a task in the Background tasks tab, back to the list, and into another listed task", () => {
		const store = useUiStore.getState();
		store.openTaskTab(id, task("a"));
		expect(panel()).toMatchObject({ isOpen: true, view: "task", activeTaskTab: "a" });
		store.openTaskTab(id, task("b"));
		store.openTaskTab(id, task("a", { description: "renamed" }));
		expect(panel().taskTabs?.map((tab) => tab.task.id)).toEqual(["a", "b"]);
		expect(panel().taskTabs?.[0].task.description).toBe("renamed");
		store.showTaskList(id);
		expect(panel()).toMatchObject({ view: "task", activeTaskTab: undefined });
		store.selectTaskTab(id, "b");
		expect(panel().activeTaskTab).toBe("b");
		store.selectTaskTab(id, "missing");
		expect(panel().activeTaskTab).toBe("b");
	});

	it("lists every task a reading names but agents, and marks a live task that left it as ended", () => {
		const store = useUiStore.getState();
		store.syncTaskTabs(id, [task("dev"), task("helper", { kind: "agent" })]);
		expect(panel().taskTabs?.map((tab) => tab.task.id)).toEqual(["dev"]);
		store.syncTaskTabs(id, [task("dev"), task("build", { state: "failed", endedAt: "2026-11-14T10:00:00Z" })]);
		expect(panel().taskTabs?.map((tab) => [tab.task.id, tab.task.state, Boolean(tab.ended)])).toEqual([["dev", "running", false], ["build", "failed", false]]);
		const before = panel();
		store.syncTaskTabs(id, [task("dev"), task("build", { state: "failed", endedAt: "2026-11-14T10:00:00Z" })]);
		expect(panel()).toBe(before);
		store.syncTaskTabs(id, []);
		expect(panel().taskTabs?.map((tab) => [tab.task.id, Boolean(tab.ended)])).toEqual([["dev", true], ["build", false]]);
		// Listed again, it is live again.
		store.syncTaskTabs(id, [task("dev")]);
		expect(panel().taskTabs?.[0].ended).toBeUndefined();
	});

	it("keeps running tasks, the open one and the newest finished ones, and forgets failures seen on the rest", () => {
		const store = useUiStore.getState();
		const done = (n: number) => task(`done-${n}`, { state: "failed", runId: "r", endedAt: "2026-11-14T10:00:00Z" });
		store.syncTaskTabs(id, [task("dev"), done(0), done(1)]);
		store.acknowledgeFailedTask(id, done(0));
		store.acknowledgeFailedTask(id, done(1));
		store.selectTaskTab(id, "done-1");
		store.syncTaskTabs(id, [task("dev"), ...Array.from({ length: FINISHED_TASK_TABS_KEPT + 1 }, (_, n) => done(n + 2))]);
		const ids = panel().taskTabs?.map((tab) => tab.task.id) ?? [];
		expect(ids).toContain("dev");
		// The oldest finished row goes; the open one, and the rows the reading still lists, stay past the cap.
		expect(ids).not.toContain("done-0");
		expect(ids).toContain("done-1");
		expect(ids.filter((taskId) => taskId !== "dev")).toHaveLength(FINISHED_TASK_TABS_KEPT + 2);
		expect(panel().seenFailedTasks).toEqual([failedTaskKey(done(1))]);
	});

	it("prunes finished rows once a reading stops listing them, even when none of them changed", () => {
		const store = useUiStore.getState();
		const finished = Array.from({ length: FINISHED_TASK_TABS_KEPT + 1 }, (_, n) => task(`done-${n}`, { state: "succeeded", endedAt: `2026-11-14T10:${String(n).padStart(2, "0")}:00Z` }));
		store.syncTaskTabs(id, finished);
		expect(panel().taskTabs).toHaveLength(FINISHED_TASK_TABS_KEPT + 1);
		store.syncTaskTabs(id, []);
		const ids = panel().taskTabs?.map((tab) => tab.task.id) ?? [];
		expect(ids).toHaveLength(FINISHED_TASK_TABS_KEPT);
		expect(ids).not.toContain("done-0");
		const before = panel();
		store.syncTaskTabs(id, []);
		expect(panel()).toBe(before);
	});

	it("drops the finished rows that ended longest ago, not the first seen", () => {
		const store = useUiStore.getState();
		const at = (minute: number) => `2026-11-14T10:${String(minute).padStart(2, "0")}:00Z`;
		// A dev server seen first, which only just failed.
		store.syncTaskTabs(id, [task("dev")]);
		const finished = Array.from({ length: FINISHED_TASK_TABS_KEPT }, (_, n) => task(`done-${n}`, { state: "succeeded", endedAt: at(n) }));
		store.syncTaskTabs(id, [task("dev"), ...finished]);
		const failed = task("dev", { state: "failed", runId: "r", endedAt: at(59) });
		store.syncTaskTabs(id, [failed]);
		store.acknowledgeFailedTask(id, failed);
		// The reading no longer lists the others, and one more task finishes.
		store.syncTaskTabs(id, [failed, task("late", { state: "succeeded", endedAt: at(58) })]);
		const ids = panel().taskTabs?.map((tab) => tab.task.id) ?? [];
		expect(ids).toContain("dev");
		expect(ids).not.toContain("done-0");
		expect(ids).toHaveLength(FINISHED_TASK_TABS_KEPT);
		expect(panel().seenFailedTasks).toEqual([failedTaskKey(failed)]);
	});

	it("leaves a session with no tasks untouched", () => {
		const before = useUiStore.getState().inspectorSessions;
		useUiStore.getState().syncTaskTabs("never-ran-a-task", []);
		expect(useUiStore.getState().inspectorSessions).toBe(before);
	});

	it("remembers each failed run that its tab has shown, once", () => {
		const store = useUiStore.getState();
		const failed = task("build", { state: "failed", runId: "r1" });
		store.acknowledgeFailedTask(id, failed);
		const before = panel();
		store.acknowledgeFailedTask(id, failed);
		expect(panel()).toBe(before);
		store.acknowledgeFailedTask(id, task("build", { state: "failed", runId: "r2" }));
		expect(panel().seenFailedTasks).toEqual([failedTaskKey(failed), failedTaskKey(task("build", { runId: "r2" }))]);
	});
});
