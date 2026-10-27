import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";

const navigateMock = vi.hoisted(() => vi.fn());
const spawnMock = vi.hoisted(() => vi.fn());
const choosePathMock = vi.hoisted(() => vi.fn());
const getMock = vi.hoisted(() => vi.fn());
const postMock = vi.hoisted(() => vi.fn());
const openExternalMock = vi.hoisted(() => vi.fn());
const writeTextMock = vi.hoisted(() => vi.fn());
const restoreMock = vi.hoisted(() => vi.fn());
const workspaceSubscriptionMock = vi.hoisted(() => vi.fn());
const cloudSearchMock = vi.hoisted(() => vi.fn());
const createProjectFlowMock = vi.hoisted(() => ({
	props: null as null | {
		existingProjectPaths?: readonly string[];
		onCreateStandaloneAgent?: () => void;
		onOpenExistingProject?: (path: string) => void | Promise<void>;
	},
	lastOpenSignal: 0,
}));

const ctx = vi.hoisted(() => {
	const workspaces: WorkspaceSummary[] = [
		{
			id: "proj-1",
			name: "app",
			path: "/repos/app",
			type: "main",
			orchestratorAgent: "codex",
			sessions: [
				{
					id: "w-merge",
					workspaceId: "proj-1",
					workspaceName: "app",
					title: "ship banner",
					provider: "codex",
					kind: "worker",
					branch: "feature/ship",
					status: "mergeable",
					updatedAt: "2026-06-10T00:00:00Z",
					prs: [],
				},
				{
					id: "w-fix",
					workspaceId: "proj-1",
					workspaceName: "app",
					title: "fix flake",
					provider: "codex",
					kind: "worker",
					branch: "feature/fix",
					status: "working",
					updatedAt: "2026-06-10T00:00:00Z",
					prs: [],
				},
				{
					id: "w-archived",
					workspaceId: "proj-1",
					workspaceName: "app",
					title: "archived cleanup",
					provider: "codex",
					kind: "worker",
					branch: "feature/archived",
					status: "terminated",
					updatedAt: "2026-06-10T00:00:00Z",
					prs: [],
				},
				{
					id: "orch",
					workspaceId: "proj-1",
					workspaceName: "app",
					title: "orchestrate",
					provider: "codex",
					kind: "orchestrator",
					branch: "main",
					status: "working",
					updatedAt: "2026-06-10T00:00:00Z",
					prs: [],
				},
			],
		},
		{
			id: "proj-2",
			name: "lib",
			path: "/repos/lib",
			type: "main",
			orchestratorAgent: "codex",
			sessions: [],
		},
	];
	return {
		params: {} as { hostId?: string; projectId?: string; sessionId?: string },
		enabled: true,
		workspaces,
	};
});

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useParams: () => ctx.params,
}));

vi.mock("../hooks/useCommandPaletteEnabled", () => ({
	useCommandPaletteEnabled: () => ctx.enabled,
	useAppVersion: () => "0.0.0-test",
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { searchWorkspaceReview: cloudSearchMock },
		ready: true,
		baseUrl: "https://cloud.example.test",
		userId: "user-1",
	}),
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: (options: { subscribed?: boolean }) => {
		workspaceSubscriptionMock(options);
		return { data: ctx.workspaces };
	},
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ cloneProject: vi.fn(), createProject: vi.fn(), initializeProjectRepository: vi.fn(), daemonStatus: {} }),
}));

vi.mock("../lib/spawn-orchestrator", () => ({ spawnOrchestrator: spawnMock }));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		POST: postMock,
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return fallback;
	},
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));


vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			openExternal: openExternalMock,
			onShortcut: (listener: Parameters<typeof appShortcutBus.subscribe>[0]) => appShortcutBus.subscribe(listener),
		},
		clipboard: { writeText: writeTextMock },
	},
}));

vi.mock("../hooks/useRestoreSession", () => ({ useRestoreSession: () => restoreMock }));

vi.mock("./CreateProjectFlow", () => ({
	CreateProjectFlow: (props: {
		openSignal?: number;
		existingProjectPaths?: readonly string[];
		onOpenExistingProject?: (path: string) => void | Promise<void>;
	}) => {
		createProjectFlowMock.props = props;
		if (props.openSignal && props.openSignal !== createProjectFlowMock.lastOpenSignal) {
			createProjectFlowMock.lastOpenSignal = props.openSignal;
			choosePathMock();
		}
		return null;
	},
}));

import { CommandPalette } from "./CommandPalette";
import { appShortcutBus } from "../test/app-shortcut-bus";

function renderPalette() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrap = () => (
		<QueryClientProvider client={queryClient}>
			<CommandPalette />
		</QueryClientProvider>
	);
	const result = render(wrap());
	return { ...result, rerenderPalette: () => result.rerender(wrap()) };
}

function pressKey(init: KeyboardEventInit): KeyboardEvent {
	const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
	act(() => {
		window.dispatchEvent(event);
	});
	return event;
}

function pressEscape(init: KeyboardEventInit = {}): KeyboardEvent {
	const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true, ...init });
	act(() => {
		document.body.dispatchEvent(event);
	});
	return event;
}

const paletteInput = () => screen.queryByPlaceholderText(/search projects/i);

beforeEach(() => {
	ctx.params = {};
	ctx.enabled = true;
	ctx.workspaces[0].orchestratorAgent = "codex";
	delete ctx.workspaces[0].hostId;
	delete ctx.workspaces[0].kind;
	ctx.workspaces[1].orchestratorAgent = "codex";
	delete ctx.workspaces[1].hostId;
	delete ctx.workspaces[1].kind;
	for (const session of ctx.workspaces.flatMap((workspace) => workspace.sessions)) {
		delete session.cloud;
		delete session.hostId;
	}
	ctx.workspaces[0].sessions[0].prs = [];
	navigateMock.mockReset();
	spawnMock.mockReset();
	choosePathMock.mockReset();
	getMock.mockReset();
	postMock.mockReset();
	openExternalMock.mockReset();
	writeTextMock.mockReset();
	restoreMock.mockReset();
	workspaceSubscriptionMock.mockReset();
	cloudSearchMock.mockReset();
	createProjectFlowMock.props = null;
	createProjectFlowMock.lastOpenSignal = 0;
	restoreMock.mockResolvedValue({ status: "success" });
	act(() => {
		useUiStore.setState({
			isCommandPaletteOpen: false,
			themePreference: "dark",
			resolvedTheme: "dark",
			restartingProjectIds: new Set(),
			settingsModal: null,
			workspaceFileOpenRequest: null,
			chatMessageFocusRequest: null,
		});
	});
});

afterEach(() => {
	document.querySelectorAll(".xterm, [data-fake-modal]").forEach((node) => node.remove());
});

describe("CommandPalette gating", () => {
	it("does not mount the project-import flow until New project is selected", async () => {
		renderPalette();
		expect(createProjectFlowMock.props).toBeNull();

		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		expect(createProjectFlowMock.props).toBeNull();

		fireEvent.click(screen.getByText("New project"));
		await waitFor(() => expect(createProjectFlowMock.props).not.toBeNull());
	});

	it("subscribes to workspace updates only while open", () => {
		renderPalette();
		expect(workspaceSubscriptionMock).toHaveBeenLastCalledWith({ subscribed: false });

		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		expect(workspaceSubscriptionMock).toHaveBeenLastCalledWith({ subscribed: true });
	});

	it("renders nothing and binds no shortcut on a disabled (stable) build", () => {
		ctx.enabled = false;
		renderPalette();
		pressKey({ key: "k", metaKey: true });
		expect(paletteInput()).toBeNull();
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
	});
});

/** Answers the palette's searches: file names, file contents and chats. */
function mockSearch(names: unknown[], content: unknown[] = [], messages: unknown[] = []) {
	getMock.mockImplementation(async (path: string) => {
		if (path.endsWith("/workspace/search/content")) return { data: { results: content } };
		if (path === "/api/v1/conversations/search") return { data: { results: messages } };
		if (path.endsWith("/workspace/search")) return { data: { query: "", results: names, truncated: false } };
		return { data: {} };
	});
}

describe("CommandPalette workspace file search", () => {
	it("searches the current session and requests the selected file without navigating", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		mockSearch([{ path: "src/components/App.tsx", status: "modified", size: 42, binary: false, fileFingerprint: "v1" }]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "App" } });

		const file = await screen.findByText("App.tsx", {}, { timeout: 2_000 });
		fireEvent.click(file);

		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "w-fix",
			path: "src/components/App.tsx",
		});
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("asks for a worktree on a project page before searching", async () => {
		ctx.params = { projectId: "proj-1" };
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);

		fireEvent.click(screen.getByText("Search files…"));
		expect(await screen.findByPlaceholderText("Choose a session")).toBeInTheDocument();
		expect(screen.getByText("ship banner")).toBeInTheDocument();
		expect(screen.getByText("fix flake")).toBeInTheDocument();
		expect(screen.queryByText("orchestrate")).not.toBeInTheDocument();
		expect(getMock).not.toHaveBeenCalledWith(expect.stringContaining("/workspace/search"), expect.anything());

		fireEvent.click(screen.getByText("fix flake"));
		expect(await screen.findByPlaceholderText("Search files in fix flake…")).toBeInTheDocument();
	});

	it("does not call workspace search until two characters settle", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		getMock.mockResolvedValue({ data: { query: "", results: [], truncated: false } });
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "A" } });
		await new Promise((resolve) => window.setTimeout(resolve, 250));
		expect(getMock).not.toHaveBeenCalledWith(expect.stringContaining("/workspace/search"), expect.anything());

		fireEvent.change(input, { target: { value: "Ap" } });
		await waitFor(() => expect(getMock).toHaveBeenCalledWith(
			expect.stringContaining("/workspace/search"),
			expect.objectContaining({ params: expect.objectContaining({ query: { query: "Ap", limit: 20 } }) }),
		));
	});

	it("uses Cloud workspace search for a Cloud session", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		ctx.workspaces[0].kind = "cloud";
		ctx.workspaces[0].sessions[1]!.cloud = { orgId: "org-1" };
		cloudSearchMock.mockResolvedValue({
			query: "cloud",
			results: [{ path: "src/cloud.ts", status: "added", size: 10, binary: false, fileFingerprint: "v1" }],
			truncated: false,
		});
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "cloud" } });

		expect(await screen.findByText("cloud.ts", {}, { timeout: 2_000 })).toBeInTheDocument();
		expect(cloudSearchMock).toHaveBeenCalledWith(
			"org-1",
			"w-fix",
			{ query: "cloud", limit: 20 },
			expect.objectContaining({ signal: expect.any(AbortSignal) }),
		);
		expect(getMock).not.toHaveBeenCalledWith(expect.stringContaining("/workspace/search"), expect.anything());
	});

	it("navigates to the selected connected-host worktree before opening its file", async () => {
		ctx.params = { hostId: "host-a", projectId: "proj-1" };
		ctx.workspaces[0].hostId = "host-a";
		for (const session of ctx.workspaces[0].sessions) session.hostId = "host-a";
		mockSearch([{ path: "src/app.ts", status: "modified", size: 5, binary: false, fileFingerprint: "v1" }]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("Search files…"));
		fireEvent.click(await screen.findByText("fix flake"));
		const input = await screen.findByPlaceholderText("Search files in fix flake…");
		fireEvent.change(input, { target: { value: "app" } });
		fireEvent.click(await screen.findByText("app.ts", {}, { timeout: 2_000 }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "host-a", projectId: "proj-1", sessionId: "w-fix" },
		});
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ hostId: "host-a", sessionId: "w-fix", path: "src/app.ts" });
	});
});

describe("CommandPalette content search", () => {
	const contentHit = { path: "src/lib/palette.ts", line: 12, text: "export function \x02closePalette\x03() {}" };
	const chatHit = { messageId: "msg-1", sessionId: "w-fix", projectId: "proj-1", title: "Palette cleanup", text: "we should \x02close\x03 the \x02palette\x03 on Escape" };

	it("opens a file at its matched line", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		mockSearch([], [contentHit]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		fireEvent.change(await screen.findByPlaceholderText(/search projects/i), { target: { value: "close palette" } });

		fireEvent.click(await screen.findByText("palette.ts:12", {}, { timeout: 2_000 }));
		expect(getMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/workspace/search/content",
			expect.objectContaining({ params: expect.objectContaining({ query: { query: "close palette" } }) }),
		);
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "w-fix", path: "src/lib/palette.ts", line: 12 });
	});

	it("opens a chat scrolled to its message, from any page", async () => {
		ctx.params = {};
		mockSearch([], [], [chatHit]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		fireEvent.change(await screen.findByPlaceholderText(/search projects/i), { target: { value: "close the palette" } });

		fireEvent.click(await screen.findByText("Palette cleanup", {}, { timeout: 2_000 }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "proj-1", sessionId: "w-fix" } });
		expect(useUiStore.getState().chatMessageFocusRequest).toMatchObject({ sessionId: "w-fix", messageId: "msg-1", text: "we should close the palette on Escape" });
	});

	it("hides chat hits without a loaded session instead of guessing a route", async () => {
		mockSearch([], [], [
			chatHit,
			{ ...chatHit, messageId: "missing", sessionId: "removed", title: "Removed session hit" },
			{ ...chatHit, messageId: "unbound", sessionId: undefined, title: "Unbound conversation hit" },
		]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		fireEvent.change(await screen.findByPlaceholderText(/search projects/i), { target: { value: "close palette" } });
		await screen.findByText("Palette cleanup", {}, { timeout: 2_000 });
		expect(screen.queryByText("Removed session hit")).not.toBeInTheDocument();
		expect(screen.queryByText("Unbound conversation hit")).not.toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it.each(["remote", "cloud"])("does not open a local chat hit in a %s session with the same ID", async (location) => {
		const localSession = ctx.workspaces[0].sessions[0];
		const originalSessions = ctx.workspaces[1].sessions;
		ctx.workspaces[1].sessions = [{ ...localSession, id: "local-result", workspaceId: "proj-2" }];
		if (location === "remote") ctx.workspaces[0].hostId = "other-host";
		else ctx.workspaces[0].kind = "cloud";
		try {
			mockSearch([], [], [chatHit, { ...chatHit, messageId: "local", sessionId: "local-result", title: "Local chat hit" }]);
			renderPalette();
			act(() => useUiStore.getState().setCommandPaletteOpen(true));
			fireEvent.change(await screen.findByPlaceholderText(/search projects/i), { target: { value: "close palette" } });
			await screen.findByText("Local chat hit", {}, { timeout: 2_000 });
			expect(screen.queryByText("Palette cleanup")).not.toBeInTheDocument();
		} finally {
			ctx.workspaces[1].sessions = originalSessions;
		}
	});

	it.each([
		["/workspace/search/content", "Unable to search file contents."],
		["/api/v1/conversations/search", "Unable to search chats."],
	])("shows errors from %s only for the current query", async (failedPath, message) => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		getMock.mockImplementation(async (path: string) => path.endsWith(failedPath)
			? { error: {} }
			: { data: { results: [], truncated: false } });
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "close palette" } });
		expect(await screen.findByRole("alert", {}, { timeout: 2_000 })).toHaveTextContent(message);
		fireEvent.change(input, { target: { value: "new query" } });
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("keeps a control that names every word above content that mentions them", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		mockSearch([], [{ path: "src/Sidebar.tsx", line: 3, text: "// \x02hide\x03 \x02sidebar\x03" }]);
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		for (const query of ["hide sidebar", "toggle sidbar"]) {
			fireEvent.change(input, { target: { value: query } });
			await screen.findByText("Sidebar.tsx:3", {}, { timeout: 2_000 });
			expect(screen.getAllByRole("option")[0]).toHaveTextContent("Toggle sidebar");
		}
	});
});

// Chords are matched in the main process (see main/app-shortcuts.ts and its
// tests, which also cover the Windows/Linux terminal yield); the palette only
// reacts to the resulting command.
describe("CommandPalette shortcuts", () => {
	it("opens on the palette command and toggles closed on a second one", async () => {
		renderPalette();
		act(() => appShortcutBus.emit({ id: "command-palette" }));
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
		act(() => appShortcutBus.emit({ id: "command-palette" }));
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("quick-opens file search for the session on screen and toggles closed on a second press", async () => {
		ctx.params = { projectId: "proj-1", sessionId: "w-fix" };
		renderPalette();
		act(() => appShortcutBus.emit({ id: "quick-open-file" }));
		expect(await screen.findByPlaceholderText("Search files in fix flake…")).toBeInTheDocument();
		act(() => appShortcutBus.emit({ id: "quick-open-file" }));
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("quick-open falls back to the plain palette when no session is on screen", async () => {
		ctx.params = {};
		renderPalette();
		act(() => appShortcutBus.emit({ id: "quick-open-file" }));
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
	});

	it("does not open over an already-open modal", () => {
		renderPalette();
		const modal = document.createElement("div");
		modal.setAttribute("role", "dialog");
		modal.setAttribute("data-state", "open");
		modal.setAttribute("data-fake-modal", "");
		document.body.appendChild(modal);
		act(() => appShortcutBus.emit({ id: "command-palette" }));
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
	});

	it.each([
		{ label: "Cmd", init: { key: "1", metaKey: true } satisfies KeyboardEventInit },
		{ label: "Ctrl", init: { key: "1", ctrlKey: true } satisfies KeyboardEventInit },
	])("swallows $label+digit project-switch while the palette is open", async ({ init }) => {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		expect(pressKey(init).defaultPrevented).toBe(true);
	});

	it("does not swallow Cmd+digit while the palette is closed (project switch still works)", () => {
		renderPalette();
		const event = pressKey({ key: "1", metaKey: true });
		expect(event.defaultPrevented).toBe(false);
	});
});

describe("CommandPalette drill-in + Enter", () => {
	it("opens a session's actions panel, then jumps with a second Enter", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);

		fireEvent.change(input, { target: { value: "ship" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("ship banner");
		});
		fireEvent.keyDown(input, { key: "Enter" });

		const actionsInput = await screen.findByPlaceholderText(/search actions/i);
		expect(navigateMock).not.toHaveBeenCalled();
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("Jump to session");
		});
		fireEvent.keyDown(actionsInput, { key: "Enter" });

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "w-merge" },
		});
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("opens the highest-scoring match, not the first category", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);

		// "app" is the exact project title but also a keyword hit on the Needs
		// attention rows, which render above Projects by default.
		fireEvent.change(input, { target: { value: "app" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.getAttribute("data-value")).toBe("project:proj-1");
		});
		fireEvent.keyDown(input, { key: "Enter" });

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
	});

	it("jumps to an archived (terminated) session via search + Enter", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);

		fireEvent.change(input, { target: { value: "archived" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("archived cleanup");
		});
		fireEvent.keyDown(input, { key: "Enter" });

		await screen.findByPlaceholderText(/search actions/i);
		expect(screen.getByText("Resume agent")).toBeInTheDocument();
		expect(screen.getByText("Jump to session")).toBeInTheDocument();

		pressEscape();
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
	});

	it("does not offer Resume for a live worker", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);

		fireEvent.change(input, { target: { value: "flake" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("fix flake");
		});
		fireEvent.keyDown(input, { key: "Enter" });

		await screen.findByPlaceholderText(/search actions/i);
		expect(screen.queryByText("Resume agent")).toBeNull();
		expect(screen.getByText("Copy branch name")).toBeInTheDocument();
	});

	it("dispatches restore when Resume is selected and closes the palette on success", async () => {
		restoreMock.mockResolvedValue({ status: "success" });
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "archived" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("archived cleanup");
		});
		fireEvent.keyDown(input, { key: "Enter" });
		await screen.findByPlaceholderText(/search actions/i);

		fireEvent.click(screen.getByText("Resume agent"));
		await waitFor(() => expect(restoreMock).toHaveBeenCalledWith("w-archived"));
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "w-archived" },
			}),
		);
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("restates a not-resumable refusal instead of echoing the daemon envelope", async () => {
		restoreMock.mockResolvedValue({ status: "not_resumable", message: "SESSION_NOT_RESUMABLE" });
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "archived" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("archived cleanup");
		});
		fireEvent.keyDown(input, { key: "Enter" });
		await screen.findByPlaceholderText(/search actions/i);

		fireEvent.click(screen.getByText("Resume agent"));
		expect(await screen.findByRole("alert")).toHaveTextContent(/no saved agent session or prompt/i);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("surfaces an inline error and stays open when resume is rejected", async () => {
		restoreMock.mockResolvedValue({ status: "error", message: "Session is not restorable" });
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "archived" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("archived cleanup");
		});
		fireEvent.keyDown(input, { key: "Enter" });
		await screen.findByPlaceholderText(/search actions/i);

		fireEvent.click(screen.getByText("Resume agent"));
		expect(await screen.findByRole("alert")).toHaveTextContent("not restorable");
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
	});
});

describe("CommandPalette actions", () => {
	it("shows disabled New task reason, skips it for selection, and ignores clicks", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		expect(screen.getByText("No current project")).toBeInTheDocument();
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).not.toContain("No current project");
			expect(selected?.getAttribute("aria-disabled")).not.toBe("true");
		});
		fireEvent.click(screen.getByText("New task"));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it("does not spawn Open orchestrator while the project is restarting", async () => {
		ctx.params = { projectId: "proj-1" };
		act(() => useUiStore.setState({ restartingProjectIds: new Set(["proj-1"]) }));
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("Open orchestrator"));
		expect(spawnMock).not.toHaveBeenCalled();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("opens project settings instead of spawning when no orchestrator agent is configured", async () => {
		ctx.params = { projectId: "proj-2" };
		ctx.workspaces[1].orchestratorAgent = undefined;
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("Open orchestrator"));

		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "proj-2" });
		expect(navigateMock).not.toHaveBeenCalled();
		expect(spawnMock).not.toHaveBeenCalled();
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("navigates and closes when selecting a project", async () => {
		ctx.params = {};
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("lib"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-2" } });
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("re-highlights the first result after the query changes", async () => {
		ctx.params = { projectId: "proj-1" };
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "ship" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("ship banner");
		});
		fireEvent.change(input, { target: { value: "fix" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("fix flake");
		});
	});

	it("spawns only once when Open orchestrator is selected twice (in-flight guard)", async () => {
		ctx.params = { projectId: "proj-2" };
		spawnMock.mockReturnValueOnce(new Promise<string>(() => {}));
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		const item = screen.getByText("Open orchestrator");
		fireEvent.click(item);
		fireEvent.click(item);
		expect(spawnMock).toHaveBeenCalledTimes(1);
	});

	it("keeps the palette open and shows an error when spawning an orchestrator fails", async () => {
		ctx.params = { projectId: "proj-2" };
		spawnMock.mockRejectedValueOnce(new Error("daemon down"));
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("Open orchestrator"));
		expect(await screen.findByRole("alert")).toHaveTextContent("daemon down");
		expect(spawnMock).toHaveBeenCalledWith("proj-2", "command_palette");
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
	});

	it("navigates home from search and closes", async () => {
		ctx.params = { projectId: "proj-1" };
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "home" } });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(navigateMock).toHaveBeenCalledWith({ to: "/" });
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("toggles the theme and closes", async () => {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "toggle theme" } });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(useUiStore.getState().resolvedTheme).toBe("light");
		expect(input).toHaveValue("toggle theme");
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("opens the new-project path picker and closes the palette", async () => {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("New project"));
		await waitFor(() => expect(choosePathMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("offers standalone agent creation from the new-project picker", async () => {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("New project"));
		await waitFor(() => expect(createProjectFlowMock.props?.onCreateStandaloneAgent).toBeTypeOf("function"));

		act(() => createProjectFlowMock.props?.onCreateStandaloneAgent?.());
		expect(useUiStore.getState().newTaskRequest?.projectId).toBe(STANDALONE_WORKSPACE_ID);
	});

	it("opens an already registered project selected by the import flow", async () => {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("New project"));
		await waitFor(() => expect(createProjectFlowMock.props).not.toBeNull());

		expect(createProjectFlowMock.props?.existingProjectPaths).toEqual(["/repos/app", "/repos/lib"]);
		await act(async () => createProjectFlowMock.props?.onOpenExistingProject?.("/repos/lib"));

		expect(useUiStore.getState().isCommandPaletteOpen).toBe(false);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-2" },
		});
	});
});

describe("CommandPalette PR and review actions", () => {
	const openPr = {
		url: "https://github.com/o/r/pull/7",
		number: 7,
		state: "open" as const,
		ci: "passing",
		review: "pending",
		mergeability: "clean",
		reviewComments: false,
		updatedAt: "2026-06-10T00:00:00Z",
	};

	const reviewState = (status: string) => ({
		prNumber: 7,
		prUrl: openPr.url,
		status,
		targetSha: "sha",
		title: "PR 7",
	});

	const mockReviews = (reviews: unknown[]) => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/reviews") {
				return { data: { reviewerHandleId: "", reviews } };
			}
			return { data: undefined };
		});
	};

	beforeEach(() => {
		ctx.workspaces[0].sessions[0].prs = [openPr];
		mockReviews([]);
		postMock.mockResolvedValue({ data: { reviewerHandleId: "", reviews: [] } });
	});

	async function openPaletteWithQuery(value: string) {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value } });
		return input;
	}

	it("opens the PR URL externally and closes", async () => {
		await openPaletteWithQuery("open pr");
		fireEvent.click(await screen.findByText("Open PR #7"));
		await waitFor(() => expect(openExternalMock).toHaveBeenCalledWith("https://github.com/o/r/pull/7"));
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("copies the PR URL to the clipboard and closes", async () => {
		await openPaletteWithQuery("copy pr");
		fireEvent.click(await screen.findByText("Copy PR URL #7"));
		await waitFor(() => expect(writeTextMock).toHaveBeenCalledWith("https://github.com/o/r/pull/7"));
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("triggers a review for the PR's session and closes", async () => {
		mockReviews([reviewState("needs_review")]);
		await openPaletteWithQuery("review");
		fireEvent.click(await screen.findByText("Review latest commit #7"));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: "w-merge" } },
			}),
		);
		await waitFor(() => expect(paletteInput()).toBeNull());
	});

	it("does not post when the review item is not eligible to run", async () => {
		await openPaletteWithQuery("review");
		expect(await screen.findByText("Not eligible for review")).toBeInTheDocument();
		fireEvent.click(screen.getByText("Run review #7"));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("shows Not eligible for review once the session review state loads", async () => {
		await openPaletteWithQuery("review");
		expect(await screen.findByText("Not eligible for review")).toBeInTheDocument();
		fireEvent.click(screen.getByText("Run review #7"));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("disables the review action with Review already running once the session review state loads", async () => {
		mockReviews([reviewState("running")]);
		await openPaletteWithQuery("review");
		expect(await screen.findByText("Review already running")).toBeInTheDocument();
		fireEvent.click(screen.getByText("Reviewing... #7"));
		expect(postMock).not.toHaveBeenCalled();
		expect(useUiStore.getState().isCommandPaletteOpen).toBe(true);
	});

	it("hides the review action until the session's review state loads, then keeps it stable for the rest of the open", async () => {
		let resolveReviews: (value: { data: { reviewerHandleId: string; reviews: unknown[] } }) => void = () => {};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/reviews") {
				return new Promise((resolve) => {
					resolveReviews = resolve;
				});
			}
			return { data: undefined };
		});
		await openPaletteWithQuery("#7");
		// Open PR / Copy PR URL don't depend on review state; the mutating
		// review action must not render until we actually know it's safe.
		expect(await screen.findByText("Open PR #7")).toBeInTheDocument();
		expect(screen.queryByText(/run review|re-run review|reviewing/i)).toBeNull();
		await waitFor(() =>
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId: "w-merge" } },
			}),
		);
		await act(async () => {
			resolveReviews({ data: { reviewerHandleId: "", reviews: [reviewState("running")] } });
		});
		expect(await screen.findByText("Review already running")).toBeInTheDocument();
		// A later poll reflecting a finished review must not retitle the row —
		// this session is frozen for the remainder of this open.
		mockReviews([reviewState("up_to_date")]);
		await act(async () => {
			await Promise.resolve();
		});
		expect(screen.getByText("Reviewing... #7")).toBeInTheDocument();
		expect(screen.queryByText("Re-run review #7")).toBeNull();
	});

	it("does not expose Run review from stale cached data while a background refetch is settling", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(
			["session-reviews", "w-merge"],
			{ reviewerHandleId: "", reviews: [reviewState("needs_review")] },
			// Force this cached value to look well past the palette's 60s
			// staleTime so opening the palette triggers a background refetch
			// while still returning this (now-outdated) data synchronously.
			{ updatedAt: Date.now() - 120_000 },
		);

		let resolveReviews: (value: { data: { reviewerHandleId: string; reviews: unknown[] } }) => void = () => {};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/reviews") {
				return new Promise((resolve) => {
					resolveReviews = resolve;
				});
			}
			return { data: undefined };
		});

		render(
			<QueryClientProvider client={queryClient}>
				<CommandPalette />
			</QueryClientProvider>,
		);
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "#7" } });

		// The stale cached "needs_review" data must not surface as an enabled
		// Run review action while the background refetch is still settling.
		expect(await screen.findByText("Open PR #7")).toBeInTheDocument();
		expect(screen.queryByText(/run review|re-run review/i)).toBeNull();
		await waitFor(() =>
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId: "w-merge" } },
			}),
		);

		await act(async () => {
			resolveReviews({ data: { reviewerHandleId: "", reviews: [reviewState("running")] } });
		});

		// Once the refetch settles with the true state, the row must reflect it.
		expect(await screen.findByText("Review already running")).toBeInTheDocument();
		fireEvent.click(screen.getByText("Reviewing... #7"));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("does not expose Run review from stale cached data when a background refetch fails", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(
			["session-reviews", "w-merge"],
			{ reviewerHandleId: "", reviews: [reviewState("needs_review")] },
			// Force this cached value to look well past the palette's 60s
			// staleTime so opening the palette triggers a background refetch
			// while still returning this (now-outdated) data synchronously.
			{ updatedAt: Date.now() - 120_000 },
		);
		let rejectReviews: (error: Error) => void = () => {};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/reviews") {
				return new Promise((_resolve, reject) => {
					rejectReviews = reject;
				});
			}
			return { data: undefined };
		});
		render(
			<QueryClientProvider client={queryClient}>
				<CommandPalette />
			</QueryClientProvider>,
		);
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "#7" } });
		expect(await screen.findByText("Open PR #7")).toBeInTheDocument();
		expect(screen.queryByText(/run review|re-run review/i)).toBeNull();
		await act(async () => {
			rejectReviews(new Error("network error"));
		});
		// TanStack Query keeps the stale cached data and flips isFetching back
		// to false on a failed refetch — the stale "needs_review" value must
		// still not be treated as resolved, so the row must stay hidden rather
		// than reappearing as an enabled Run review.
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(screen.queryByText(/run review|re-run review/i)).toBeNull();
	});
});

describe("CommandPalette back navigation", () => {
	async function drillIntoTest() {
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		const input = await screen.findByPlaceholderText(/search projects/i);
		fireEvent.change(input, { target: { value: "archived" } });
		await waitFor(() => {
			const selected = document.querySelector('[cmdk-item][data-selected="true"]');
			expect(selected?.textContent).toContain("archived cleanup");
		});
		fireEvent.keyDown(input, { key: "Enter" });
		return screen.findByPlaceholderText(/search actions/i);
	}

	it("pops to root on Backspace at an empty query", async () => {
		ctx.params = {};
		const actionsInput = await drillIntoTest();
		fireEvent.keyDown(actionsInput, { key: "Backspace" });
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
	});

	it("blocks a duplicate resume while one is in flight", async () => {
		restoreMock.mockReturnValueOnce(new Promise<never>(() => {}));
		ctx.params = {};
		await drillIntoTest();
		fireEvent.click(screen.getByText("Resume agent"));
		expect(restoreMock).toHaveBeenCalledTimes(1);
		fireEvent.click(screen.getByText("Resume agent"));
		expect(restoreMock).toHaveBeenCalledTimes(1);
	});

	it("still dismisses while a command hangs, and drops the late result", async () => {
		let reject: (err: Error) => void = () => undefined;
		restoreMock.mockReturnValueOnce(new Promise<never>((_resolve, r) => (reject = r)));
		ctx.params = {};
		await drillIntoTest();
		fireEvent.click(screen.getByText("Resume agent"));

		pressEscape();
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();

		await act(async () => {
			reject(new Error("daemon went away"));
			await Promise.resolve();
		});
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("lets an IME consume Escape instead of popping the view", async () => {
		ctx.params = {};
		await drillIntoTest();
		const composing = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
		Object.defineProperty(composing, "isComposing", { value: true });
		act(() => {
			document.body.dispatchEvent(composing);
		});
		expect(screen.getByPlaceholderText(/search actions/i)).toBeInTheDocument();
		pressEscape();
		expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
	});

	it("auto-pops to root when the scoped session disappears", async () => {
		ctx.params = {};
		const original = ctx.workspaces;
		try {
			const { rerenderPalette } = renderPalette();
			act(() => useUiStore.getState().setCommandPaletteOpen(true));
			const input = await screen.findByPlaceholderText(/search projects/i);
			fireEvent.change(input, { target: { value: "archived" } });
			await waitFor(() => {
				const selected = document.querySelector('[cmdk-item][data-selected="true"]');
				expect(selected?.textContent).toContain("archived cleanup");
			});
			fireEvent.keyDown(input, { key: "Enter" });
			await screen.findByPlaceholderText(/search actions/i);

			ctx.workspaces = original.map((w) => ({ ...w, sessions: w.sessions.filter((s) => s.id !== "w-archived") }));
			act(() => rerenderPalette());

			expect(await screen.findByPlaceholderText(/search projects/i)).toBeInTheDocument();
			expect(await screen.findByRole("alert")).toHaveTextContent(/no longer available/i);
		} finally {
			ctx.workspaces = original;
		}
	});
});

describe("CommandPalette new task", () => {
	it("closes the palette and opens the new chat page for the current project", async () => {
		ctx.params = { projectId: "proj-1" };
		useUiStore.setState({ newTaskRequest: null });
		renderPalette();
		act(() => useUiStore.getState().setCommandPaletteOpen(true));
		await screen.findByPlaceholderText(/search projects/i);
		fireEvent.click(screen.getByText("New task"));
		await waitFor(() => expect(useUiStore.getState().isCommandPaletteOpen).toBe(false));
		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: "proj-1" });
	});
});
