import { beforeEach, describe, expect, it, vi } from "vitest";
import { rememberedFileDisplayMode, sidebarIsVisible, sidebarOccupiesLayout, useUiStore } from "./ui-store";
import { sessionUiKey } from "../lib/hosts";
import { previewDocumentThemeStyle } from "../lib/theme";

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

		useUiStore.getState().openGlobalSettings("agents");
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "agents" });
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
});

// A fresh module sees exactly what a booting renderer sees: only storage.
async function bootStore() {
	return (await import("./ui-store")).useUiStore;
}

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
