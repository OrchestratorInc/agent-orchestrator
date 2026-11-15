import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { toKanbanColumn } from "@aoagents/product-ui";
import { appI18n } from "../i18n";

// Instant motion updates so height tweens do not leave tests waiting on timers.
vi.mock("motion/react", async (importOriginal) => {
	const actual = await importOriginal<typeof import("motion/react")>();
	return {
		...actual,
		AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
	};
});

const {
	appMemoryMock,
	sessionMemoryMock,
	navigateMock,
	notificationShowMock,
	postMock,
	patchMock,
	workspaceQueryMock,
	cloudProjectsMock,
	spawnCloudOrchestratorMock,
	offeringMock,
	participantsMock,
	shareDialogMock,
	routerListeners,
	fakeRouter,
} = vi.hoisted(() => {
	// Stands in for the router's onBeforeNavigate events, which the archive host follows.
	const listeners = new Set<(event: { pathChanged: boolean }) => void>();
	return {
		routerListeners: listeners,
		fakeRouter: {
			subscribe: (_type: string, listener: (event: { pathChanged: boolean }) => void) => {
				listeners.add(listener);
				return () => {
					listeners.delete(listener);
				};
			},
		},
		appMemoryMock: vi.fn(),
		sessionMemoryMock: vi.fn(),
		navigateMock: vi.fn(),
		notificationShowMock: vi.fn(),
		postMock: vi.fn(),
		patchMock: vi.fn(),
		workspaceQueryMock: vi.fn(),
		cloudProjectsMock: vi.fn((): { id: string; config?: object }[] | undefined => undefined),
		spawnCloudOrchestratorMock: vi.fn(),
		offeringMock: vi.fn((): Record<string, unknown> => ({ defaultProvider: "coder" })),
		participantsMock: vi.fn((): ReadonlyMap<string, unknown[]> => new Map()),
		shareDialogMock: vi.fn(),
	};
});

vi.mock("../lib/cloud-orchestrator", () => ({ spawnCloudOrchestrator: spawnCloudOrchestratorMock }));

vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => offeringMock() }));
vi.mock("../hooks/useSessionParticipants", () => ({ useSessionParticipants: () => participantsMock() }));
vi.mock("../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => true }));
vi.mock("./ShareSessionDialog", () => ({
	ShareSessionDialog: (props: { open: boolean; sessionId?: string; sessionName?: string; projectName?: string; orgId: string; projectId: string }) => {
		shareDialogMock(props);
		return props.open ? (
			<div role="dialog" aria-label="Share dialog" data-session={props.sessionId ?? ""} data-session-name={props.sessionName ?? ""} data-project-name={props.projectName ?? ""} data-org={props.orgId} data-project={props.projectId} />
		) : null;
	},
}));


vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useRouter: () => fakeRouter,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	useWorkspaceQuery: workspaceQueryMock,
	useRemoteProjectQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useRemoteHostWorkspacesQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useCloudProjectsQuery: () => ({ data: cloudProjectsMock() }),
	useWorkspaceScope: (projectId?: string) => {
		const query = workspaceQueryMock();
		return {
			...query,
			data: { project: query.data?.find((workspace: WorkspaceSummary) => workspace.id === projectId) },
		};
	},
}));

vi.mock("./ProjectImportOffer", () => ({ ProjectImportOffer: () => null }));

vi.mock("./SessionTopbarActions", async (importOriginal) => ({ ...(await importOriginal<typeof import("./SessionTopbarActions")>()), SessionTopbarActions: () => null }));

vi.mock("../hooks/useSessionMemory", async (importOriginal) => {
	const { pressureState } = await import("@aoagents/product-ui");
	return {
		...(await importOriginal<typeof import("../hooks/useSessionMemory")>()),
		useAppMemory: appMemoryMock,
		useSessionMemory: sessionMemoryMock,
		// The light's own poll, fed from the same mocked reading.
		usePressureState: () => {
			const system = appMemoryMock()?.data?.system;
			return system ? pressureState(system) : undefined;
		},
	};
});

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: (...args: unknown[]) => postMock(...args), PATCH: (...args: unknown[]) => patchMock(...args) },
	apiErrorCode: (error: unknown) => (error as { code?: string } | null)?.code,
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			onShortcut: () => () => {},
			onCloseShellTerminalShortcut: () => () => {},
			onPreviousTabShortcut: () => () => {},
			onNextTabShortcut: () => () => {},
			setCloseShellTerminalShortcutEnabled: () => {},
		},
		cloud: {
			getSession: vi.fn().mockResolvedValue(null),
			onSessionChanged: vi.fn(() => () => {}),
			signIn: vi.fn().mockResolvedValue(undefined),
			signOut: vi.fn().mockResolvedValue(undefined),
		},
		clipboard: {
			writeText: vi.fn(),
		},
		editorHandoff: {
			getState: vi.fn().mockResolvedValue({ targets: [], preferredEditorId: "cursor", workspaceAvailable: true }),
		},
		notifications: {
			show: (...args: unknown[]) => notificationShowMock(...args),
		},
	},
}));

vi.mock("../lib/platform", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/platform")>();
	return {
		...actual,
		isLinuxPlatform: () => false,
	};
});

import { isMacPlatform } from "../lib/platform";
import { useUiStore } from "../stores/ui-store";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { ArchiveModalHost } from "./ArchiveModalHost";
import { SessionsBoard } from "./SessionsBoard";
import { latestSessionArtifacts, toBoardSessionPresentation } from "./SessionsBoardAdapters";
import { TooltipProvider } from "./ui/tooltip";

function renderBoard(projectId?: string) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	renderBoardWithClient(queryClient, projectId);
	return queryClient;
}

/** The board with the shell's archive modal host beside it, as the app mounts them. */
function renderBoardWithClient(queryClient: QueryClient, projectId?: string) {
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SessionsBoard projectId={projectId} />
				<ArchiveModalHost />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

/** Opens the archive modal from the board's button; its cards mount the frame after it opens. */
async function expandArchive() {
	await userEvent.click(screen.getByRole("button", { name: /^Archived sessions, \d+$/ }));
	const dialog = await screen.findByRole("dialog", { name: "Archived sessions" });
	await within(dialog).findAllByTestId("session-archive-card");
	return dialog;
}

beforeEach(() => {
	// The memory light and card chips are Developer mode tools behind the Diagnostics toggle.
	useUiStore.setState({ developerMode: true, diagnostics: true, archiveModal: null });
	navigateMock.mockReset();
	notificationShowMock.mockReset().mockResolvedValue(undefined);
	postMock.mockReset().mockResolvedValue({ data: {} });
	patchMock.mockReset().mockResolvedValue({ data: {}, response: { status: 200 } });
	workspaceQueryMock.mockReset().mockReturnValue({ data: [], isError: false });
	appMemoryMock.mockReset().mockReturnValue({ data: undefined, isError: false });
	sessionMemoryMock.mockReset().mockReturnValue({ data: undefined, isError: false });
	offeringMock.mockReset().mockReturnValue({ defaultProvider: "coder" });
	participantsMock.mockReset().mockReturnValue(new Map());
	shareDialogMock.mockReset();
});

describe("SessionsBoard", () => {
	it("leaves a Cloud standalone agent's project for Home: it has no board", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "agent-1", name: "Fix the flake", kind: "cloud", standaloneAgent: true, path: "", sessions: [] }],
			isSuccess: true,
		});
		renderBoard("agent-1");
		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: "/", replace: true }));
	});

	it("shows the Cloud loader instead of Chat while a cloud orchestrator is starting", () => {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "p1", name: "Cloud project", kind: "cloud", path: "", sessions: [] }], isSuccess: true });
		useUiStore.getState().setProjectProvisioning("p1", true);
		try {
			renderBoard("p1");
			expect(screen.getByTestId("cloud-session-loader-screen")).toBeInTheDocument();
			expect(screen.queryByLabelText("Message the agent")).not.toBeInTheDocument();
		} finally {
			useUiStore.getState().setProjectProvisioning("p1", false);
		}
	});

	it("starts a Freestyle cloud orchestrator in place, on its conversation, and keeps the loader for Coder", () => {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "p1", name: "Cloud project", kind: "cloud", path: "", sessions: [] }], isSuccess: true });
		useUiStore.getState().setProjectProvisioning("p1", true);
		try {
			cloudProjectsMock.mockReturnValue([{ id: "p1", config: { sandboxProvider: "freestyle" } }]);
			renderBoard("p1");
			expect(screen.queryByTestId("cloud-session-loader-screen")).not.toBeInTheDocument();
			const composer = screen.getByRole("combobox", { name: "Message the agent" });
			expect(composer.closest("form")).toHaveAttribute("data-starting", "true");
			cleanup();
			// The org default applies to a project with no provider of its own (Coder here).
			cloudProjectsMock.mockReturnValue([{ id: "p1", config: {} }]);
			renderBoard("p1");
			expect(screen.getByTestId("cloud-session-loader-screen")).toBeInTheDocument();
			expect(screen.queryByRole("combobox", { name: "Message the agent" })).not.toBeInTheDocument();
		} finally {
			cloudProjectsMock.mockReturnValue(undefined);
			useUiStore.getState().setProjectProvisioning("p1", false);
		}
	});

	it("starts a just-created Freestyle project's orchestrator once, without showing the empty board", async () => {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "p1", name: "Cloud project", kind: "cloud", path: "", sessions: [] }], isSuccess: true });
		cloudProjectsMock.mockReturnValue([{ id: "p1", config: { sandboxProvider: "freestyle" } }]);
		spawnCloudOrchestratorMock.mockReset().mockReturnValue(new Promise(() => {}));
		useUiStore.getState().setAutoStartOrchestrator("p1", true);
		try {
			renderBoard("p1");
			expect(screen.queryByTestId("board")).not.toBeInTheDocument();
			expect(screen.getByRole("combobox", { name: "Message the agent" }).closest("form")).toHaveAttribute("data-starting", "true");
			await waitFor(() => expect(spawnCloudOrchestratorMock).toHaveBeenCalledTimes(1));
			expect(useUiStore.getState().autoStartOrchestratorProjectIds.has("p1")).toBe(false);
		} finally {
			cloudProjectsMock.mockReturnValue(undefined);
			useUiStore.getState().setAutoStartOrchestrator("p1", false);
		}
	});

	it("offers a guest of a shared project no Task, orchestrator or missing-orchestrator notice", () => {
		const worker = boardSession({ id: "s1", title: "Upgrade to React 20", status: "working", cloud: { orgId: "org-elsewhere", shared: true } });
		workspaceQueryMock.mockReturnValue({ data: [{ ...workspaceWithSessions([worker]), kind: "cloud", cloudOrgId: "org-elsewhere", shared: { byName: "Asha" } }], isSuccess: true });
		renderBoard("p1");
		expect(screen.queryByRole("button", { name: /^(Task|New task)$/ })).toBeNull();
		expect(screen.queryByText("No orchestrator is running for this project.")).toBeNull();
	});

	it("keeps Task and the missing-orchestrator notice on the owner's own project", () => {
		const worker = boardSession({ id: "s1", title: "Upgrade to React 20", status: "working", cloud: { orgId: "org-mine" } });
		workspaceQueryMock.mockReturnValue({ data: [{ ...workspaceWithSessions([worker]), kind: "cloud", cloudOrgId: "org-mine" }], isSuccess: true });
		renderBoard("p1");
		expect(screen.getAllByRole("button", { name: /^(Task|New task)$/ }).length).toBeGreaterThan(0);
	});

	it("leaves an unflagged cloud project on its board", () => {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "p1", name: "Cloud project", kind: "cloud", path: "", sessions: [] }], isSuccess: true });
		cloudProjectsMock.mockReturnValue([{ id: "p1", config: { sandboxProvider: "freestyle" } }]);
		spawnCloudOrchestratorMock.mockReset();
		try {
			renderBoard("p1");
			expect(screen.getByTestId("board")).toBeInTheDocument();
			expect(spawnCloudOrchestratorMock).not.toHaveBeenCalled();
		} finally {
			cloudProjectsMock.mockReturnValue(undefined);
		}
	});

	it("shows the orchestrator launch surface immediately after project setup", () => {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "p1", name: "New project", path: "/tmp/new-project", sessions: [] }], isSuccess: true });
		useUiStore.getState().setProjectProvisioning("p1", true);
		try {
			renderBoard("p1");
			expect(screen.getByText("Starting your orchestrator")).toBeInTheDocument();
			expect(screen.getByLabelText("Message the agent")).toBeInTheDocument();
			expect(screen.getByTestId("session-workspace-topbar")).toBeInTheDocument();
		} finally {
			useUiStore.getState().setProjectProvisioning("p1", false);
		}
	});

	it("renames a session from its card's context menu", async () => {
		const user = userEvent.setup();
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s1", title: "Fix login", status: "working" })])],
			isSuccess: true, isError: false,
		});
		renderBoard("p1");

		fireEvent.contextMenu(await screen.findByText("Fix login"));
		await user.click(await screen.findByRole("menuitem", { name: "Rename Fix login" }));
		const dialog = await screen.findByRole("dialog");
		const input = within(dialog).getByRole("textbox", { name: "Rename Fix login" });
		await user.clear(input);
		await user.type(input, "Fix login redirect");
		await user.click(within(dialog).getByRole("button", { name: "Rename" }));

		await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
			params: { path: { sessionId: "s1" } },
			body: { displayName: "Fix login redirect" },
		}));
		expect(navigateMock).not.toHaveBeenCalled();
	});

	describe("sharing from a card", () => {
		const shareName = (title: string) => `Share “${title}”`;
		function cloudBoard(sessions: WorkspaceSession[], project: Partial<WorkspaceSummary> = {}) {
			offeringMock.mockReturnValue({ defaultProvider: "coder", features: ["share"], org: { id: "org-mine" } });
			workspaceQueryMock.mockReturnValue({
				data: [{ ...workspaceWithSessions(sessions), name: "acme/web", kind: "cloud", cloudOrgId: "org-mine", ...project }],
				isSuccess: true, isError: false,
			});
		}

		it("opens the session's share dialog from its card, without opening the session", async () => {
			cloudBoard([boardSession({ id: "s1", title: "Fix login", status: "working", cloud: { orgId: "org-mine" } })]);
			renderBoard("p1");
			expect(screen.queryByRole("dialog", { name: "Share dialog" })).toBeNull();
			await userEvent.click(await screen.findByRole("button", { name: shareName("Fix login") }));
			const dialog = await screen.findByRole("dialog", { name: "Share dialog" });
			expect(dialog.dataset).toMatchObject({ session: "s1", sessionName: "Fix login", projectName: "acme/web", org: "org-mine", project: "p1" });
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("opens the same dialog from the people circles on an already shared session", async () => {
			cloudBoard([boardSession({ id: "s1", title: "Fix login", status: "working", cloud: { orgId: "org-mine" } })]);
			participantsMock.mockReturnValue(new Map([["s1", [{ userId: "v", name: "Vaanya", present: true, state: "idle" }]]]));
			renderBoard("p1");
			const circles = await screen.findByRole("button", { name: /Vaanya/ });
			await userEvent.click(circles);
			expect((await screen.findByRole("dialog", { name: "Share dialog" })).dataset.session).toBe("s1");
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it.each([
			["a local session", boardSession({ id: "s1", title: "Fix login", status: "working" }), {}, { features: ["share"], org: { id: "org-mine" } }],
			["a session shared from another organization", boardSession({ id: "s1", title: "Fix login", status: "working", cloud: { orgId: "org-elsewhere", shared: true } }), {}, { features: ["share"], org: { id: "org-mine" } }],
			["an organization without sharing", boardSession({ id: "s1", title: "Fix login", status: "working", cloud: { orgId: "org-mine" } }), {}, { features: [], org: { id: "org-mine" } }],
			["a terminated session", boardSession({ id: "s1", title: "Fix login", status: "working", isTerminated: true, cloud: { orgId: "org-mine" } }), {}, { features: ["share"], org: { id: "org-mine" } }],
		])("offers no Share on %s", async (_name, session, project, offering) => {
			cloudBoard([session, boardSession({ id: "s2", title: "Other task", status: "working" })], project);
			offeringMock.mockReturnValue({ defaultProvider: "coder", ...offering });
			renderBoard("p1");
			await screen.findByText("Other task");
			expect(screen.queryByRole("button", { name: shareName("Fix login") })).toBeNull();
			expect(document.querySelector("[data-board-share-session]")).toBeNull();
		});
	});

	it("says a session's status could not be verified, without offering a retry", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "unverified", title: "Unverified task", status: "unknown", displayStatus: "Working", statusReadiness: "unavailable" })])],
			isSuccess: true, isError: false,
		});
		renderBoard("p1");
		expect(screen.queryByText("Working")).not.toBeInTheDocument();
		expect(screen.getByText("Unable to verify")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Retry status check" })).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("shows what a session costs the machine on its card", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				boardSession({ id: "running", title: "Running task", status: "idle", activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" } }),
				boardSession({ id: "quiet", title: "Quiet task", status: "idle", activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" } }),
			])],
			isSuccess: true, isError: false,
		});
		sessionMemoryMock.mockReturnValue({
			isError: false,
			data: new Map([["running", { sessionId: "running", rssBytes: 641_728_512, processCount: 3, cpuPercent: 82.4, sampledAt: "", processes: [] }]]),
		});
		renderBoard("p1");
		// The card says what the session costs the machine right now; an unsampled one shows nothing, never 0 MB.
		expect(screen.getAllByTestId("session-resource")).toHaveLength(1);
		expect(screen.getByTestId("session-resource")).toHaveTextContent("612 MB");
		expect(screen.getByTestId("session-resource")).toHaveAttribute("data-resource-tone", "neutral");
	});

	it.each([
		["outside Developer mode", { developerMode: false, diagnostics: true }],
		["with Diagnostics off in Developer mode", { developerMode: true, diagnostics: false }],
	])("shows no memory light or card memory %s", async (_case, flags) => {
		useUiStore.setState(flags);
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "running", title: "Running task", status: "idle", activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" } })])],
			isSuccess: true, isError: false,
		});
		sessionMemoryMock.mockReturnValue({
			isError: false,
			data: new Map([["running", { sessionId: "running", rssBytes: 641_728_512, processCount: 3, cpuPercent: 82.4, sampledAt: "", processes: [] }]]),
		});
		appMemoryMock.mockReturnValue({ isError: false, data: { app: { rssBytes: 2 * 1024 ** 3, processCount: 4, cpuPercent: 0 }, liveCount: 1 } });
		renderBoard("p1");
		expect(await screen.findByText("Running task")).toBeInTheDocument();
		expect(screen.queryByTestId("session-resource")).not.toBeInTheDocument();
		expect(screen.queryByTestId("app-memory-indicator")).not.toBeInTheDocument();
	});

	it("shows AO memory pressure before the Task button, and no archive bar with nothing archived", async () => {
		const GIB = 1024 ** 3;
		appMemoryMock.mockReturnValue({
			isError: false,
			data: {
				app: { rssBytes: 12 * GIB, processCount: 20, cpuPercent: 40 },
				system: { totalBytes: 32 * GIB, availableBytes: 2 * GIB, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: 8, load1: 1, cpuPercent: 0, pressureRaw: 35, pressureSource: "psi" },
				liveCount: 1,
			},
		});
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "live", title: "Live task", status: "working" })])],
			isSuccess: true, isError: false,
		});
		renderBoard("p1");
		expect(screen.queryByRole("button", { name: /^Archived sessions, \d+$/ })).not.toBeInTheDocument();
		const indicator = screen.getByTestId("app-memory-indicator");
		// In the board's action row, ahead of the Task button.
		expect(indicator.closest(".workspace-topbar-actions")).not.toBeNull();
		const task = screen.getByRole("button", { name: "New task" });
		expect(indicator.compareDocumentPosition(task) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(indicator).toHaveTextContent("12.0 GB");
		expect(indicator).toHaveAttribute("data-memory-state", "tight");
		expect(indicator).toHaveAttribute("aria-label", "Tight · 2.0 GB free of 32.0 GB · Orchestrator holds 12.0 GB · pressure 35.0");
		await userEvent.click(indicator);
		expect(await screen.findByTestId("session-memory-stacked")).toBeInTheDocument();
	});

	it.each(["single_repo", "multi_repo", "cloud", "standalone", undefined] as const)("hides the cue runner for %s projects", (kind) => {
		workspaceQueryMock.mockReturnValue({
			data: [{ ...workspaceWithSessions([]), kind }],
			isError: false,
		});
		renderBoard("p1");
		expect(screen.queryByRole("button", { name: "Run a cue" })).not.toBeInTheDocument();
	});

	it("uses the last human message time rather than generic session updatedAt", () => {
		const presentation = toBoardSessionPresentation(
			boardSession({
				id: "timestamp-session",
				lastUserMessageAt: "2026-01-01T09:00:00Z",
				status: "idle",
				title: "timestamp task",
				updatedAt: "2026-01-01T10:00:00Z",
			}),
		);

		expect(presentation.lastUserMessageAt).toBe("2026-01-01T09:00:00Z");
	});

	it("localizes dynamic card actions and pull request lifecycle labels", async () => {
		await appI18n.changeLanguage("zh-CN");
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-localized",
						title: "localized worker",
						status: "pr_open",
						prs: [
							{
								url: "https://github.com/acme/repo/pull/42",
								number: 42,
								state: "open",
								ci: "passing",
								review: "approved",
								mergeability: "mergeable",
								reviewComments: false,
								updatedAt: "2026-01-01T00:00:00Z",
							},
						],
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		try {
			renderBoard("p1");
			expect(screen.getByRole("button", { name: "归档 localized worker" })).toBeInTheDocument();
			expect(screen.getByRole("link", { name: "PR #42 已打开" })).toHaveAttribute(
				"href",
				"https://github.com/acme/repo/pull/42",
			);
		} finally {
			await appI18n.changeLanguage("en");
		}
	});

	it("does not show an agent setup warning on the board", () => {
		renderBoard();

		expect(screen.queryByText(/reload agents/i)).not.toBeInTheDocument();
	});

	it("shows the Board identity and compact actions in the in-panel board chrome", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "solkit-ui",
					path: "/tmp/solkit-ui",
					kind: "single_repo",
					sessions: [
						{
							id: "s1",
							workspaceId: "p1",
							workspaceName: "solkit-ui",
							title: "test",
							provider: "codex",
							branch: "ao/dev/solkit-ui-5/root",
							status: "running",
							activity: { state: "working", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		expect(screen.getByTestId("board-topbar-label").textContent).toContain("Board");
		expect(screen.queryByText("solkit-ui")).toBeNull();
		expect(screen.getByRole("button", { name: "New task" }).closest(".center-panel-titlebar")).toHaveClass(
			"workspace-topbar-container",
		);
		expect(
			within(screen.getByRole("button", { name: "New task" })).getByText("Task").hasAttribute("data-compact-label"),
		).toBe(true);
		expect(screen.queryByRole("button", { name: "Run a cue" })).not.toBeInTheDocument();
	});

	it.each([
		["active", "Working"],
		["idle", "Idle"],
	] as const)("shows %s orchestrator activity in the in-panel board toolbar", (state, label) => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "solkit-ui",
					path: "/tmp/solkit-ui",
					sessions: [
						{
							id: "orch-1",
							workspaceId: "p1",
							workspaceName: "solkit-ui",
							title: "orchestrator",
							provider: "codex",
							kind: "orchestrator",
							branch: "main",
							status: "working",
							activity: { state, lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const button = screen.getByRole("button", { name: `Orchestrator, ${label}` });
		const indicator = button.querySelector("span.size-dot-sm");
		expect(within(button).getByText("Orchestrator").hasAttribute("data-compact-label")).toBe(true);
		expect(indicator).toBeNull();
	});

	it("shows the Board crumb on the root board when actions live in the panel", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "solkit-ui",
					path: "/tmp/solkit-ui",
					sessions: [],
				},
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard();

		expect(screen.getByText("Board")).toBeInTheDocument();
	});

	it("labels an idle session as Idle, not Working", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "radic",
					path: "/tmp/radic",
					sessions: [
						{
							id: "s1",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "brand-font-pipeline",
							provider: "claude-code",
							branch: "ao/radic-5",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
			],
			isError: false,
		});

		renderBoard("p1");

		const idleCard = screen
			.getByText("brand-font-pipeline")
			.closest('[data-testid="board-session-card"]') as HTMLElement;
		expect(within(idleCard).getByText("Idle")).toBeInTheDocument();
		const terminateButton = within(idleCard).getByRole("button", { name: "Archive brand-font-pipeline" });
		expect(terminateButton).toHaveClass("opacity-0", "group-hover:opacity-100", "group-focus-within:opacity-100");
		expect(terminateButton.querySelector("svg")).toHaveClass("icon-archive");
		expect(within(idleCard).getByText("Idle").parentElement?.parentElement).toHaveClass("flex");
		expect(within(idleCard).getByText("brand-font-pipeline")).toHaveClass("font-semibold", "line-clamp-2");
	});

	it("styles a working card from its building lane without inferring from runtime activity", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-active",
						title: "active-card-task",
						status: "working",
						activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("active-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const working = within(card).getByText("Working").parentElement as HTMLElement;
		expect(working).toHaveAttribute("data-kanban-column", "building");
		expect(working).toHaveClass("text-status-working");
		expect(working.style.getPropertyValue("--session-status-tone")).toBe("");
		expect(working.querySelector('[aria-hidden="true"]')).toHaveClass("animate-spin");
	});

	// A multi-PR session aggregates `status` from its worst open PR while
	// `displayStatus` comes from its best one, so a settled "Mergeable" card can
	// carry status `review_pending`. The loader must follow the label the card
	// actually shows, not the hidden aggregate, or an idle session spins forever.
	it("does not spin a settled Mergeable card whose worst PR aggregates to review_pending", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-mergeable",
						title: "mergeable-card-task",
						status: "review_pending",
						displayStatus: "Mergeable",
						kanbanColumn: "ready",
						activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("mergeable-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const status = within(card).getByTestId("session-status");
		expect(status).toHaveTextContent("Mergeable");
		expect(status.querySelector(".animate-spin")).toBeNull();
	});

	it("keeps the loader on a Retrying review card", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-review-pending",
						title: "review-pending-card-task",
						status: "review_pending",
						displayStatus: "Retrying review",
						kanbanColumn: "validating",
						activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("review-pending-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const status = within(card).getByTestId("session-status");
		expect(status).toHaveTextContent("Retrying review");
		expect(status.querySelector(".animate-spin")).not.toBeNull();
	});

	// The loader follows the phrase: a working agent gets the "Working" phrase
	// from the daemon, so a card that still reads "Mergeable" describes a settled PR.
	it("does not spin a Mergeable card even while its agent is active", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-mergeable-active",
						title: "mergeable-active-task",
						status: "working",
						displayStatus: "Mergeable",
						kanbanColumn: "ready",
						activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("mergeable-active-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const status = within(card).getByTestId("session-status");
		expect(status.querySelector(".animate-spin")).toBeNull();
	});

	it("paints a Merged card purple and holds the loader even while its agent keeps running", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-merged-active",
						title: "merged-active-task",
						status: "working",
						displayStatus: "Merged",
						kanbanColumn: "ready",
						activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("merged-active-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const status = within(card).getByTestId("session-status");
		expect(status).toHaveTextContent("Merged");
		expect(status).toHaveClass("text-status-merged");
		expect(status).not.toHaveClass("text-success", "text-status-ready");
		expect(status.querySelector(".animate-spin")).toBeNull();
	});

	it("paints Closed without merge red while keeping merged status purple", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-closed-without-merge",
						title: "closed-without-merge-task",
						status: "idle",
						displayStatus: "Closed without merge",
						kanbanColumn: "ready",
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("closed-without-merge-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const status = within(card).getByTestId("session-status");
		expect(status).toHaveTextContent("Closed without merge");
		expect(status).toHaveClass("text-status-exited");
		expect(status).not.toHaveClass("text-status-ready", "text-status-merged");
	});

	it("labels a provisioning card with the agent being started", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-spawning",
						title: "spawning-card-task",
						status: "working",
						provisionState: "provisioning",
						branch: "ao/s-spawning/root",
						provisionSteps: [{ id: "worktree", status: "running" }],
						activity: { state: "exited", lastActivityAt: "2026-01-01T00:00:00Z" },
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("spawning-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		expect(within(card).getByText("Starting Claude Code…")).toBeInTheDocument();
		expect(within(card).getByRole("status")).toHaveClass("chat-working-shimmer");
		expect(within(card).queryByText("ao/s-spawning/root")).not.toBeInTheDocument();
		expect(within(card).queryByText("Working")).not.toBeInTheDocument();
		expect(within(card).queryByText("Exited")).not.toBeInTheDocument();
	});

	it("shows naming shimmer on a synchronous orchestrator-created worker until its worktree is ready", () => {
		const worker = boardSession({
			id: "s-delegated",
			title: "Review: composer correctness",
			status: "idle",
			provisionState: "ready",
			branch: undefined,
			provisionSteps: [{ id: "worktree", status: "running" }],
		});
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([worker])], isError: false, isSuccess: true,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = renderBoardWithClient(queryClient, "p1");
		const card = () => screen.getByText(worker.title).closest('[data-testid="board-session-card"]') as HTMLElement;
		expect(within(card()).getByRole("status")).toHaveClass("chat-working-shimmer");
		expect(within(card()).getByRole("status").textContent).not.toMatch(/…|\.\.\./);

		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([{ ...worker, branch: "ao/composer-review/root", provisionSteps: [{ id: "worktree", status: "done" }] }])],
			isError: false, isSuccess: true,
		});
		view.rerender(<QueryClientProvider client={queryClient}><TooltipProvider><SessionsBoard projectId="p1" /></TooltipProvider></QueryClientProvider>);
		expect(within(card()).queryByRole("status")).not.toBeInTheDocument();
		expect(within(card()).getByText("composer-review")).toBeInTheDocument();
	});

	it("shows start failure instead of Idle on a failed card", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({
				id: "s-setup-failed",
				title: "failed-setup-task",
				status: "idle",
				displayStatus: "Idle",
				provisionState: "failed",
			})])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");
		const card = screen.getByText("failed-setup-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		expect(within(card).getByTestId("session-status")).toHaveTextContent("Start failed");
		expect(within(card).queryByText("Idle")).not.toBeInTheDocument();
	});

	it("styles legacy statuses from their status-implied Kanban columns", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "radic",
					path: "/tmp/radic",
					sessions: [
						{
							id: "s0",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "idle-card-task",
							provider: "claude-code",
							branch: "ao/radic-5",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
						{
							id: "s1",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "no-signal-card-task",
							provider: "claude-code",
							branch: "ao/radic-6",
							status: "no_signal",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
						{
							id: "s2",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "draft-card-task",
							provider: "claude-code",
							branch: "ao/radic-7",
							status: "draft",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
			],
			isError: false,
		});

		renderBoard("p1");
		const idleCard = screen.getByText("idle-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const noSignalCard = screen.getByText("no-signal-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;
		const draftCard = screen.getByText("draft-card-task").closest('[data-testid="board-session-card"]') as HTMLElement;

		expect(within(idleCard).getByText("Idle").parentElement).toHaveAttribute(
			"data-kanban-column",
			"building",
		);
		expect(within(noSignalCard).getByText("No signal").parentElement).toHaveAttribute(
			"data-kanban-column",
			"building",
		);
		expect(within(draftCard).getByText("Draft PR").parentElement).toHaveAttribute(
			"data-kanban-column",
			"validating",
		);
	});

	it("places a no-signal display status alongside idle even with a review column", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				boardSession({
					id: "no-signal-review",
					title: "silent reviewer",
					status: "review_pending",
					kanbanColumn: "needs_review",
					displayStatus: "No signal",
				}),
			])],
			isError: false,
		});

		renderBoard("p1");
		const card = screen.getByText("silent reviewer").closest('[data-testid="board-session-card"]') as HTMLElement;
		expect(within(card).getByText("No signal").parentElement).toHaveAttribute("data-kanban-column", "building");
		const building = screen.getAllByTestId("board-column").find((column) => column.dataset.column === "building");
		expect(building).toBeDefined();
		expect(within(building!).getByText("silent reviewer")).toBeInTheDocument();
	});

	it("keeps a PR-less exited session in the building lane with an Exited badge", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					{
						id: "s-exited",
						workspaceId: "p1",
						workspaceName: "radic",
						title: "agent-exited-task",
						provider: "codex",
						branch: "ao/exited",
						status: "exited",
						// What the daemon derives for a worker with no PR, whatever its
						// runtime status. Set explicitly so this covers the daemon path,
						// not the older-daemon fallback.
						kanbanColumn: "building",
						activity: { state: "exited", lastActivityAt: "2026-01-01T00:00:00Z" },
						updatedAt: "2026-01-01T00:00:00Z",
						prs: [],
					},
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		// Lanes follow the daemon's column: a worker with no PR is still building,
		// whatever its runtime status. The card keeps its Exited badge.
		const buildingColumn = screen.getByLabelText("Building sessions");
		expect(within(buildingColumn).getByText("agent-exited-task")).toBeInTheDocument();
		expect(within(buildingColumn).getByText("Exited").parentElement).toHaveAttribute(
			"data-kanban-column",
			"building",
		);
	});

	it("swaps the building lane's cards when navigating between project boards", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "p1",
					name: "radic",
					path: "/tmp/radic",
					sessions: [
						{
							id: "p1-active",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "p1 active",
							provider: "claude-code",
							branch: "ao/radic-active",
							status: "working",
							activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
						{
							id: "p1-idle",
							workspaceId: "p1",
							workspaceName: "radic",
							title: "p1 idle",
							provider: "claude-code",
							branch: "ao/radic-idle",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
				{
					id: "p2",
					name: "other",
					path: "/tmp/other",
					sessions: [
						{
							id: "p2-active",
							workspaceId: "p2",
							workspaceName: "other",
							title: "p2 active",
							provider: "claude-code",
							branch: "ao/other-active",
							status: "working",
							activity: { state: "active", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
						{
							id: "p2-idle",
							workspaceId: "p2",
							workspaceName: "other",
							title: "p2 idle",
							provider: "claude-code",
							branch: "ao/other-idle",
							status: "idle",
							activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" },
							updatedAt: "2026-01-01T00:00:00Z",
							prs: [],
						},
					],
				},
			],
			isError: false,
		});
		const view = renderBoardWithClient(queryClient, "p1");

		const p1Lane = screen.getByRole("region", { name: "Building sessions" });
		expect(p1Lane).toHaveTextContent("p1 idle");
		expect(p1Lane).toHaveTextContent("p1 active");

		view.rerender(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<SessionsBoard projectId="p2" />
				</TooltipProvider>
			</QueryClientProvider>,
		);

		const p2Lane = screen.getByRole("region", { name: "Building sessions" });
		expect(screen.queryByText("p1 idle")).not.toBeInTheDocument();
		expect(p2Lane).toHaveTextContent("p2 idle");
		expect(p2Lane).toHaveTextContent("p2 active");
	});

	it("opens the archive as a searchable modal of session cards", async () => {
		const archivedSession = terminatedSession();
		const mergedPr = archivedSession.prs[0];
		if (!mergedPr) throw new Error("Archived-session fixture requires a pull request");
		archivedSession.prs = [
			{
				...mergedPr,
				number: 41,
				state: "open",
				url: "https://github.com/example/radic/pull/41",
			},
			mergedPr,
		];
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([archivedSession, terminatedSession({ id: "s-other", title: "other worker", branch: "ao/other", prs: [] })])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		// The board keeps only a quiet entry point; no cards render until it opens.
		const archiveButton = screen.getByRole("button", { name: "Archived sessions, 2" });
		expect(archiveButton).toHaveAttribute("aria-haspopup", "dialog");
		expect(within(archiveButton).getByText("2")).toBeInTheDocument();
		expect(screen.queryByText("dead worker")).not.toBeInTheDocument();

		const archive = await expandArchive();
		const search = within(archive).getByRole("combobox");
		expect(search).toHaveFocus();
		const row = within(archive).getByText("dead worker").closest<HTMLElement>("[role='gridcell']");
		expect(row).not.toBeNull();
		const card = within(row!).getByTestId("board-session-card");
		expect(within(card).getByRole("img", { name: "claude-code" })).toBeInTheDocument();
		expect(within(card).getByText("ao/dead-worker")).toBeInTheDocument();
		// Everything here is archived, so the card does not say so.
		expect(within(card).queryByText("Terminated")).not.toBeInTheDocument();
		// The PR links already carry their state; the "n of m merged" line is dropped.
		expect(within(card).queryByTestId("session-pr-progress")).not.toBeInTheDocument();
		expect(within(card).getByRole("link", { name: "PR #42 merged" })).toHaveAttribute(
			"href",
			"https://github.com/example/radic/pull/42",
		);
		expect(within(card).getByRole("button", { name: "Copy branch ao/dead-worker" })).toBeInTheDocument();
		expect(within(card).getByRole("button", { name: "Restore dead worker" })).toBeInTheDocument();
		expect(within(card).getByRole("button", { name: "Delete dead worker permanently" })).toBeInTheDocument();

		// The card's own controls act alone; they never open the session.
		await userEvent.click(within(card).getByRole("button", { name: "Copy branch ao/dead-worker" }));
		expect(navigateMock).not.toHaveBeenCalled();

		await userEvent.type(search, "#41");
		await waitFor(() => expect(within(archive).queryByText("other worker")).not.toBeInTheDocument());
		expect(within(archive).getByText("dead worker")).toBeInTheDocument();

		await userEvent.clear(search);
		await userEvent.type(search, "ao/other");
		await waitFor(() => expect(within(archive).queryByText("dead worker")).not.toBeInTheDocument());
		expect(within(archive).getByText("other worker")).toBeInTheDocument();

		await userEvent.clear(search);
		await userEvent.type(search, "nothing like this");
		expect(await within(archive).findByText("No results.")).toBeInTheDocument();

		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument());
	});

	it("opens over any page from a request, without a board or navigation", async () => {
		workspaceQueryMock.mockReturnValue({
			data: twoProjectsWithArchives(),
			isError: false,
			isSuccess: true,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ArchiveModalHost />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument();

		// Where Cmd+K was opened from; its own search is gone by the time the archive closes.
		const opener = document.createElement("button");
		opener.textContent = "opener";
		document.body.append(opener);

		// What the sidebar's menu and Cmd+K do.
		act(() => useUiStore.getState().openArchive({ projectId: "p1", query: "dead", returnFocus: opener }));

		const archive = await screen.findByRole("dialog", { name: "Archived sessions" });
		const search = within(archive).getByRole("combobox");
		expect(search).toHaveValue("dead");
		expect(await within(archive).findByText("dead worker")).toBeInTheDocument();
		expect(within(archive).queryByText("p2 worker")).not.toBeInTheDocument();
		expect(search).toHaveFocus();

		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument());
		expect(useUiStore.getState().archiveModal).toBeNull();
		expect(navigateMock).not.toHaveBeenCalled();
		// Closing hands focus back to where the opener came from.
		await waitFor(() => expect(opener).toHaveFocus());
		opener.remove();
	});

	it("hides PR progress for a live merged session that has not actually terminated", () => {
		const liveMergedSession = terminatedSession({
			id: "s-live-merged",
			title: "live merged worker",
			status: "merged",
			isTerminated: false,
			kanbanColumn: "ready",
		});
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([liveMergedSession])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const card = screen.getByText("live merged worker").closest<HTMLElement>("[data-testid='board-session-card']");
		expect(card).not.toBeNull();
		expect(within(card!).queryByTestId("session-pr-progress")).not.toBeInTheDocument();
	});

	it("restores a terminated session, refreshes workspace data, and opens the restored terminal", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});
		const queryClient = renderBoard("p1");
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue(undefined);

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/restore", {
				params: { path: { sessionId: "s-dead" } },
			}),
		);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["workspaces"] });
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "p1", sessionId: "s-dead" },
		});
	});

	it("shows a toast when restore falls back to a saved-prompt conversation", async () => {
		postMock.mockResolvedValueOnce({ data: { restoreMode: "saved_prompt" } });
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		await waitFor(() =>
			expect(notificationShowMock).toHaveBeenCalledWith(
				expect.objectContaining({
					title: "Started from saved prompt",
					body: expect.stringContaining("started a new conversation from the saved prompt"),
				}),
			),
		);
	});

	it("does not show a fallback toast when restore uses native resume", async () => {
		postMock.mockResolvedValueOnce({ data: { restoreMode: "native" } });
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		await waitFor(() => expect(postMock).toHaveBeenCalled());
		expect(notificationShowMock).not.toHaveBeenCalled();
	});

	it("keeps restore actions visible and disables siblings while one session is restoring", async () => {
		let finishRestore: ((value: { data: Record<string, never> }) => void) | undefined;
		postMock.mockReturnValueOnce(
			new Promise((resolve) => {
				finishRestore = resolve;
			}),
		);
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession(), terminatedSession({ id: "s-other", title: "other worker" })])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		const restoringButton = screen.getByRole("button", { name: "Restore dead worker" });
		const otherButton = screen.getByRole("button", { name: "Restore other worker" });
		expect(restoringButton.querySelector("svg")).toHaveClass("animate-spin");
		expect(otherButton).toBeDisabled();
		expect(otherButton).not.toHaveClass("opacity-0");

		await act(async () => {
			finishRestore?.({ data: {} });
		});
	});

	it("opens the restore-unavailable dialog when a session is not resumable", async () => {
		postMock.mockResolvedValueOnce({ error: { code: "SESSION_NOT_RESUMABLE" } });
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		expect(await screen.findByText("Session can no longer be restored")).toBeInTheDocument();
	});

	it("shows the archive's cards without fetching a PR summary for each", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions(Array.from({ length: 5 }, (_, index) => terminatedSession({ id: `s-${index}`, title: `archived ${index}` })))],
			isError: false,
			isSuccess: true,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderBoardWithClient(queryClient, "p1");

		const archive = await expandArchive();
		expect(within(archive).getAllByTestId("session-archive-card")).toHaveLength(5);
		// Each card shows the PRs its session recorded; none starts its own
		// request (or a poll) for a live summary.
		const summaries = queryClient.getQueryCache().findAll({ queryKey: sessionScmSummaryQueryKey() });
		expect(summaries.every((query) => query.state.fetchStatus === "idle" && query.state.dataUpdateCount === 0 && query.state.errorUpdateCount === 0)).toBe(true);
		expect(within(archive).getAllByRole("link", { name: /^PR #\d+/ }).length).toBeGreaterThan(0);
	});

	it("shows an archive row error when restore fails", async () => {
		postMock.mockResolvedValueOnce({ error: { code: "RESTORE_FAILED", message: "boom" } });
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		expect(await screen.findByText("Unable to restore session")).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("opens an archived session from the archive without restoring it", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const archive = await expandArchive();
		await userEvent.click(within(archive).getByText("dead worker"));

		expect(postMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledTimes(1);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "p1", sessionId: "s-dead" },
		});
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument());
	});

	it("restores the selected archived session with the keyboard", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		await expandArchive();
		await waitFor(() => expect(screen.getByText("dead worker").closest("[role='gridcell']")).toHaveAttribute("aria-selected", "true"));
		const mod = isMacPlatform() ? "Meta" : "Control";
		await userEvent.keyboard(`{${mod}>}{Enter}{/${mod}}`);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/restore", {
				params: { path: { sessionId: "s-dead" } },
			}),
		);
	});

	it("moves the archive selection through the grid as laid out, with the search keeping focus", async () => {
		const sessions = Array.from({ length: 5 }, (_, index) =>
			terminatedSession({ id: `s-${index}`, title: `archived ${index}`, branch: `ao/archived-${index}`, prs: [], updatedAt: `2026-01-0${9 - index}T00:00:00Z` }),
		);
		workspaceQueryMock.mockReturnValue({ data: [workspaceWithSessions(sessions)], isError: false, isSuccess: true });
		renderBoard("p1");

		const archive = await expandArchive();
		const search = within(archive).getByRole("combobox");
		const grid = within(archive).getByRole("grid", { name: "Archived sessions" });
		// Three columns, as the responsive grid would lay out a medium window:
		//   0 1 2
		//   3 4
		const realGetComputedStyle = window.getComputedStyle.bind(window);
		const styleSpy = vi.spyOn(window, "getComputedStyle").mockImplementation((element, pseudo) => {
			const style = realGetComputedStyle(element, pseudo);
			if (element !== grid) return style;
			return {
				display: style.display,
				pointerEvents: style.pointerEvents,
				visibility: style.visibility,
				getPropertyValue: (name: string) => (name === "grid-template-columns" ? "100px 100px 100px" : style.getPropertyValue(name)),
			} as CSSStyleDeclaration;
		});
		const selected = () => within(grid).getAllByRole("gridcell").find((option) => option.getAttribute("aria-selected") === "true");
		const expectSelected = (title: string) => {
			const option = selected();
			expect(option).toHaveAccessibleName(title);
			expect(search).toHaveAttribute("aria-activedescendant", option?.id);
			expect(search).toHaveFocus();
			expect(within(grid).getAllByTestId("board-session-card").filter((card) => card.hasAttribute("data-selected"))).toHaveLength(1);
		};

		expectSelected("archived 0");
		await userEvent.keyboard("{ArrowRight}");
		expectSelected("archived 1");
		await userEvent.keyboard("{ArrowDown}");
		expectSelected("archived 4");
		// The last row is the floor.
		await userEvent.keyboard("{ArrowDown}");
		expectSelected("archived 4");
		await userEvent.keyboard("{ArrowUp}");
		expectSelected("archived 1");
		// Down from a column the short last row lacks lands on its last card.
		await userEvent.keyboard("{ArrowRight}{ArrowDown}");
		expectSelected("archived 4");
		await userEvent.keyboard("{ArrowLeft}");
		expectSelected("archived 3");
		await userEvent.keyboard("{ArrowUp}");
		expectSelected("archived 0");
		// The first row is the ceiling.
		await userEvent.keyboard("{ArrowUp}");
		expectSelected("archived 0");
		await userEvent.keyboard("{End}");
		expectSelected("archived 4");
		await userEvent.keyboard("{Home}");
		expectSelected("archived 0");

		// A new search starts again at its first result.
		await userEvent.keyboard("{End}");
		await userEvent.type(search, "archived");
		await waitFor(() => expectSelected("archived 0"));
		styleSpy.mockRestore();

		// Unmeasured, the grid is one column: Down steps to the next card.
		await userEvent.keyboard("{ArrowDown}{Enter}");
		expect(postMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "p1", sessionId: "s-1" },
		});
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument());
	});

	it("selects the card under the pointer only when the pointer moves", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				terminatedSession({ id: "s-0", title: "archived 0", prs: [], updatedAt: "2026-01-03T00:00:00Z" }),
				terminatedSession({ id: "s-1", title: "archived 1", prs: [], updatedAt: "2026-01-02T00:00:00Z" }),
				terminatedSession({ id: "s-2", title: "archived 2", prs: [], updatedAt: "2026-01-01T00:00:00Z" }),
			])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const archive = await expandArchive();
		const [first, second, third] = within(archive).getAllByRole("gridcell");
		const pointerMove = (target: HTMLElement, x: number, y: number) =>
			act(() => {
				target.dispatchEvent(new MouseEvent("pointermove", { bubbles: true, clientX: x, clientY: y }));
			});

		pointerMove(within(third!).getByText("archived 2"), 10, 10);
		expect(third).toHaveAttribute("aria-selected", "true");
		await userEvent.keyboard("{ArrowUp}");
		expect(second).toHaveAttribute("aria-selected", "true");
		// A card scrolling under a still pointer does not take the selection back.
		pointerMove(within(third!).getByText("archived 2"), 10, 10);
		expect(second).toHaveAttribute("aria-selected", "true");
		pointerMove(within(first!).getByText("archived 0"), 12, 10);
		expect(first).toHaveAttribute("aria-selected", "true");
		expect(third).toHaveAttribute("aria-selected", "false");
	});

	it("tabs through the selected archive card's controls and deletes it with Mod+Backspace", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				terminatedSession({ updatedAt: "2026-01-02T00:00:00Z" }),
				terminatedSession({ id: "s-other", title: "other worker", branch: "ao/other", prs: [] }),
			])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const archive = await expandArchive();
		const search = within(archive).getByRole("combobox");
		await userEvent.keyboard("{ArrowDown}");
		expect(within(archive).getByRole("gridcell", { name: "other worker" })).toHaveAttribute("aria-selected", "true");

		// Tab walks only the selected card: restore, delete, copy branch, then the close button.
		await userEvent.tab();
		expect(screen.getByRole("button", { name: "Restore other worker" })).toHaveFocus();
		await userEvent.tab();
		expect(screen.getByRole("button", { name: "Delete other worker permanently" })).toHaveFocus();
		await userEvent.tab();
		expect(screen.getByRole("button", { name: "Copy branch ao/other" })).toHaveFocus();
		await userEvent.tab();
		expect(within(archive).getByRole("button", { name: "Close" })).toHaveFocus();
		await userEvent.tab();
		expect(search).toHaveFocus();
		await userEvent.tab({ shift: true });
		expect(within(archive).getByRole("button", { name: "Close" })).toHaveFocus();
		await userEvent.tab({ shift: true });
		expect(screen.getByRole("button", { name: "Copy branch ao/other" })).toHaveFocus();

		// An arrow from a card's control moves the selection and hands focus back to the search.
		await userEvent.keyboard("{ArrowUp}");
		expect(search).toHaveFocus();
		expect(within(archive).getByRole("gridcell", { name: "dead worker" })).toHaveAttribute("aria-selected", "true");

		// With text before the caret, Mod+Backspace is still the search's own editing.
		const mod = isMacPlatform() ? "Meta" : "Control";
		await userEvent.type(search, "worker");
		await userEvent.keyboard(`{${mod}>}{Backspace}{/${mod}}`);
		expect(screen.queryByRole("dialog", { name: "Delete this session permanently?" })).not.toBeInTheDocument();
		await userEvent.clear(search);

		await userEvent.keyboard(`{${mod}>}{Backspace}{/${mod}}`);
		const confirm = await screen.findByRole("dialog", { name: "Delete this session permanently?" });
		expect(confirm).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("ignores archive keys while an IME is composing", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				terminatedSession({ updatedAt: "2026-01-02T00:00:00Z" }),
				terminatedSession({ id: "s-other", title: "other worker", branch: "ao/other", prs: [] }),
			])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const archive = await expandArchive();
		const search = within(archive).getByRole("combobox");
		fireEvent.keyDown(search, { key: "ArrowDown", isComposing: true });
		fireEvent.keyDown(search, { key: "Enter", isComposing: true });
		expect(within(archive).getByRole("gridcell", { name: "dead worker" })).toHaveAttribute("aria-selected", "true");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("confirms before deleting an archived session from the archive", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const archive = await expandArchive();
		await userEvent.click(within(archive).getByRole("button", { name: "Delete dead worker permanently" }));

		expect(await screen.findByRole("dialog", { name: "Delete this session permanently?" })).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("ignores restore completion after the archive closes", async () => {
		let finishRestore: ((value: { data: Record<string, never> }) => void) | undefined;
		postMock.mockReturnValueOnce(
			new Promise((resolve) => {
				finishRestore = resolve;
			}),
		);
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([terminatedSession()]),
				{
					id: "p2",
					name: "other",
					path: "/tmp/other",
					sessions: [],
				},
			],
			isError: false,
			isSuccess: true,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderBoardWithClient(queryClient, "p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		// Closed, the modal no longer stands for where the user is.
		act(() => useUiStore.getState().closeArchive());
		await act(async () => {
			finishRestore?.({ data: {} });
		});

		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByText("Session can no longer be restored")).not.toBeInTheDocument();
	});

	it("ignores restore-unavailable completion after the archive closes", async () => {
		let finishRestore: ((value: { error: { code: string } }) => void) | undefined;
		postMock.mockReturnValueOnce(
			new Promise((resolve) => {
				finishRestore = resolve;
			}),
		);
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([terminatedSession()]),
				{
					id: "p2",
					name: "other",
					path: "/tmp/other",
					sessions: [],
				},
			],
			isError: false,
			isSuccess: true,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderBoardWithClient(queryClient, "p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		// Closed, the modal no longer stands for where the user is.
		act(() => useUiStore.getState().closeArchive());
		await act(async () => {
			finishRestore?.({ error: { code: "SESSION_NOT_RESUMABLE" } });
		});

		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByText("Session can no longer be restored")).not.toBeInTheDocument();
	});

	it("closes when the user moves to another page, and drops a restore still running", async () => {
		let finishRestore: ((value: { data: Record<string, never> }) => void) | undefined;
		postMock.mockReturnValueOnce(
			new Promise((resolve) => {
				finishRestore = resolve;
			}),
		);
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([terminatedSession()])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await expandArchive();
		await userEvent.click(screen.getByRole("button", { name: "Restore dead worker" }));

		const navigateAway = (pathChanged: boolean) => act(() => {
			for (const listener of [...routerListeners]) listener({ pathChanged });
		});
		// The same page with new search params is not leaving it.
		navigateAway(false);
		expect(screen.getByRole("dialog", { name: "Archived sessions" })).toBeInTheDocument();

		// A shortcut, history or a notification moving to another page.
		navigateAway(true);
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archived sessions" })).not.toBeInTheDocument());
		expect(useUiStore.getState().archiveModal).toBeNull();
		await act(async () => {
			finishRestore?.({ data: {} });
		});
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("keeps a live merged session in the ready lane and opens its card without restore", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-merged", title: "merged worker", status: "merged" })])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const readyLane = screen.getByRole("region", { name: "Ready sessions" });
		expect(within(readyLane).getByText("Ready")).toHaveClass("text-status-ready");
		expect(within(readyLane).getByText("merged worker")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Archived sessions, \d+$/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Restore merged worker" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByText("merged worker"));

		expect(postMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "p1", sessionId: "s-merged" },
		});
	});

	it("groups lanes by the daemon's Kanban column, not by display status", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					// Both are "working" on the card; only the column decides the lane.
					boardSession({
						id: "s-validating",
						title: "validating worker",
						status: "working",
						kanbanColumn: "validating",
					}),
					boardSession({
						id: "s-building",
						title: "building worker",
						status: "working",
						kanbanColumn: "building",
					}),
					// Mergeable on the card, but no AO loop is turning it, so the
					// review-feedback loop is on a person's turn.
					boardSession({
						id: "s-needs-review",
						title: "in review worker",
						status: "mergeable",
						kanbanColumn: "needs_review",
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const lane = (label: string) => screen.getByLabelText(label);
		expect(within(lane("Building sessions")).getByText("building worker")).toBeInTheDocument();
		expect(within(lane("Validating sessions")).getByText("validating worker")).toBeInTheDocument();
		expect(within(lane("In review sessions")).getByText("in review worker")).toBeInTheDocument();
		expect(within(lane("Ready sessions")).queryByText("in review worker")).toBeNull();
	});

	it("renders the daemon's display status on the card", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-ci",
						title: "ci worker",
						status: "ci_failed",
						kanbanColumn: "validating",
						displayStatus: "Fixing CI failures",
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		expect(screen.getByText("Fixing CI failures")).toBeInTheDocument();
		expect(screen.queryByText("CI failed")).not.toBeInTheDocument();
	});

	it("highlights every user-attention status while leaving ordinary cards neutral", () => {
		const attentionStatuses = [
			"ci_failed",
			"changes_requested",
		] as const;
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					...attentionStatuses.map((status) =>
						boardSession({
							id: `s-${status}`,
							kanbanColumn: "building",
							status,
							title: `${status} worker`,
						}),
					),
					boardSession({ id: "s-idle", title: "idle worker", status: "idle" }),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		for (const status of attentionStatuses) {
			const card = screen
				.getByText(`${status} worker`)
				.closest('[data-testid="board-session-card"]');
			expect(card).toHaveClass(
				"animate-attention-card-pulse",
				"border-status-needs-you",
				"bg-[color-mix(in_srgb,var(--color-status-needs-you)_8%,var(--color-surface))]",
			);
		}

		const ordinaryCard = screen
			.getByText("idle worker")
			.closest('[data-testid="board-session-card"]');
		expect(ordinaryCard).toHaveClass("border-border", "bg-surface");
		expect(ordinaryCard).not.toHaveClass("animate-attention-card-pulse");
	});

	// Mixed-version upgrade: an older daemon sends no kanbanColumn at all. Cards
	// must stay in the lanes their status already put them in, not pile into the
	// leftmost one.
	it("keeps an older daemon's sessions in their status-implied lanes", () => {
		const legacy = (id: string, title: string, status: WorkspaceSession["status"]): WorkspaceSession => {
			const session = boardSession({ id, title, status });
			delete session.kanbanColumn;
			return session;
		};
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					legacy("s-ready", "legacy ready worker", "mergeable"),
					legacy("s-action", "legacy action worker", "changes_requested"),
					legacy("s-review", "legacy review worker", "review_pending"),
					legacy("s-working", "legacy working worker", "working"),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const lane = (label: string) => screen.getByLabelText(label);
		expect(within(lane("Building sessions")).getByText("legacy working worker")).toBeInTheDocument();
		expect(within(lane("Validating sessions")).getByText("legacy review worker")).toBeInTheDocument();
		expect(within(lane("In review sessions")).getByText("legacy action worker")).toBeInTheDocument();
		expect(within(lane("Ready sessions")).getByText("legacy ready worker")).toBeInTheDocument();
	});

	it("orders the lanes building, validating, in review, then ready", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-one", title: "worker one", status: "idle" })])],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		expect(screen.getAllByTestId("board-column").map((column) => column.dataset.column)).toEqual([
			"building",
			"validating",
			"needs_review",
			"ready",
		]);
	});

	it("uses the shared minimal scrollbar styling for every Kanban lane", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({ id: "s-idle", title: "idle worker", status: "idle" }),
					boardSession({ id: "s-working", title: "working worker", status: "working" }),
					boardSession({ id: "s-action", title: "action worker", status: "needs_input" }),
					boardSession({ id: "s-review", title: "review worker", status: "review_pending" }),
					boardSession({ id: "s-ready", title: "ready worker", status: "mergeable" }),
					boardSession({ id: "s-merged", title: "merged worker", status: "merged" }),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const laneScrollers = screen
			.getAllByTestId("board-column")
			.flatMap((column) => Array.from(column.querySelectorAll<HTMLElement>(".overflow-y-auto")));
		expect(laneScrollers).toHaveLength(4);
		for (const scroller of laneScrollers) {
			expect(scroller).toHaveClass("board-scrollbar", "overflow-y-auto");
		}
	});

	it("archives a terminated merged runtime without duplicating it in the ready lane", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({ id: "s-live-merged", title: "live merged worker", status: "merged" }),
					terminatedSession({ id: "s-archived-merged", title: "archived merged worker", status: "merged" }),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderBoard("p1");

		const readyLane = screen.getByRole("region", { name: "Ready sessions" });
		expect(within(readyLane).getByText("live merged worker")).toBeInTheDocument();
		expect(within(readyLane).queryByText("archived merged worker")).not.toBeInTheDocument();

		const archive = await expandArchive();
		const archivedMergedRow = within(archive)
			.getByText("archived merged worker")
			.closest<HTMLElement>("[role='gridcell']");
		expect(archivedMergedRow).not.toBeNull();
		expect(
			within(archivedMergedRow!).queryByRole("button", { name: "Archive archived merged worker" }),
		).not.toBeInTheDocument();
		expect(within(archive).getByRole("button", { name: "Restore archived merged worker" })).toBeInTheDocument();
	});

	it("asks for confirmation when terminating an ordinary live session from its card", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-idle", title: "idle worker", status: "idle" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await userEvent.click(screen.getByRole("button", { name: "Archive idle worker" }));

		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" })).toBeInTheDocument();
	});

	it("returns focus to the archive control after backing out of the confirm", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-merged", title: "merged worker", status: "merged" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await userEvent.click(screen.getByRole("button", { name: "Archive merged worker" }));
		await screen.findByRole("dialog");
		await userEvent.click(screen.getByRole("button", { name: "No" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

		// Radix aims close-time focus at a DialogTrigger; these confirms open
		// programmatically, so without an explicit restore the keyboard user is
		// stranded on <body> instead of the control they came from.
		expect(screen.getByRole("button", { name: "Archive merged worker" })).toHaveFocus();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("closes the card's archive confirm on the first Escape", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-merged", title: "merged worker", status: "merged" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await userEvent.click(screen.getByRole("button", { name: "Archive merged worker" }));
		await screen.findByRole("dialog");

		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("terminates a live merged session from its card without opening the session", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-merged", title: "merged worker", status: "merged" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const terminateButton = screen.getByRole("button", { name: "Archive merged worker" });
		expect(terminateButton).toHaveClass("opacity-100");
		expect(terminateButton).not.toHaveClass("opacity-0");
		await userEvent.click(terminateButton);
		expect(navigateMock).not.toHaveBeenCalled();
		const dialog = screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" });
		await userEvent.click(within(dialog).getByRole("button", { name: "Confirm, archive session" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "s-merged" } },
			}),
		);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("keeps only the targeted card disabled while its termination is pending", async () => {
		let finishKill!: (value: { data: { ok: boolean; sessionId: string }; error: undefined }) => void;
		postMock.mockReturnValueOnce(
			new Promise((resolve) => {
				finishKill = resolve;
			}),
		);
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({ id: "s-one", title: "worker one", status: "working" }),
					boardSession({ id: "s-two", title: "worker two", status: "merged" }),
				]),
			],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await userEvent.click(screen.getByRole("button", { name: "Archive worker one" }));
		await userEvent.click(
			within(screen.getByRole("dialog")).getByRole("button", { name: "Confirm, archive session" }),
		);

		expect(screen.getByRole("button", { name: "Archiving worker one" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Archiving worker one" })).toHaveClass("opacity-100");
		expect(screen.getByRole("button", { name: "Archive worker two" })).toBeEnabled();
		expect(postMock).toHaveBeenCalledTimes(1);

		finishKill({ data: { ok: true, sessionId: "s-one" }, error: undefined });
		await waitFor(() => expect(screen.getByRole("button", { name: "Archive worker one" })).toBeEnabled());
	});

	it("keeps the merged-card confirmation dismissed and surfaces termination failures", async () => {
		postMock.mockResolvedValueOnce({ error: { message: "runtime failed" }, response: { status: 500 } });
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({ id: "s-merged", title: "merged worker", status: "merged" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await userEvent.click(screen.getByRole("button", { name: "Archive merged worker" }));
		await userEvent.click(
			within(screen.getByRole("dialog")).getByRole("button", { name: "Confirm, archive session" }),
		);

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		const alerts = await screen.findAllByRole("alert");
		expect(alerts.some((alert) => alert.textContent?.includes("Failed to terminate session (500)"))).toBe(true);
		expect(screen.getByRole("button", { name: "Archive merged worker" })).toBeEnabled();
	});

	it("shows a folder-missing banner when the project root no longer exists on disk", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ ...workspaceWithSessions([]), folderMissing: true }],
		});
		renderBoard("p1");
		expect(screen.getByText(appI18n.t("home.folderMissing"))).toBeInTheDocument();
	});

	it("does not show the folder-missing banner when the project folder exists", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ ...workspaceWithSessions([]), folderMissing: false }],
		});
		renderBoard("p1");
		expect(screen.queryByText(appI18n.t("home.folderMissing"))).not.toBeInTheDocument();
	});
});

describe("SessionsBoard artifacts", () => {
	beforeEach(() => {
		useUiStore.setState({ inspectorSessions: {}, workspaceFileOpenRequest: null });
	});

	const artifact = (name: string, updatedAt: string, kind: "html" | "markdown" = "markdown") => ({
		kind,
		name,
		path: `out/${name}`,
		size: 10,
		updatedAt,
	});

	it("shows only the latest artifacts and opens the session with the clicked one", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-art",
						title: "Write the report",
						status: "working",
						outputType: "artifact",
						artifactFiles: [
							artifact("old.md", "2026-01-01T00:00:00Z"),
							artifact("newest.html", "2026-01-03T00:00:00Z", "html"),
							artifact("middle.md", "2026-01-02T00:00:00Z"),
							artifact("oldest.md", "2025-12-01T00:00:00Z"),
						],
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const list = await screen.findByTestId("board-session-artifacts");
		const links = within(list).getAllByRole("button", { name: /^Open artifact / });
		expect(links.map((link) => link.textContent)).toEqual(["newest.html", "middle.md"]);
		expect(list).toHaveTextContent("+2 more");

		await userEvent.click(screen.getByRole("button", { name: "Open artifact middle.md" }));

		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "s-art",
			path: "out/middle.md",
			source: "artifact",
		});
		expect(navigateMock).toHaveBeenCalledTimes(1);
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "p1", sessionId: "s-art" },
		});
	});

	it.each([3, 10, 12])("expands and collapses %i artifacts in place, capped at ten", async (count) => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({
				id: "s-art",
				title: "Write the report",
				status: "working",
				outputType: "artifact",
				artifactFiles: Array.from({ length: count }, (_, index) =>
					artifact(`file-${String(index + 1).padStart(2, "0")}.md`, "2026-01-01T00:00:00Z")),
			})])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		const list = await screen.findByTestId("board-session-artifacts");
		const more = within(list).getByRole("button", { name: `+${count - 2} more` });
		expect(more).toHaveAttribute("aria-expanded", "false");
		more.focus();
		await userEvent.keyboard("{Enter}");

		expect(more).toHaveAttribute("aria-expanded", "true");
		expect(document.getElementById(more.getAttribute("aria-controls")!)).toBeInTheDocument();
		expect(within(list).getAllByRole("button", { name: /^Open artifact / })).toHaveLength(Math.min(count, 10));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();

		if (count > 10) {
			await userEvent.click(within(list).getByRole("button", { name: "Open in inspector" }));
			// SessionView initializes the inspector when navigation mounts a session.
			useUiStore.getState().initializeInspectorSession("s-art", false, true);
			expect(useUiStore.getState().inspectorSessions["s-art"]).toMatchObject({
				initialized: true, isOpen: true, view: "artifacts",
			});
			expect(useUiStore.getState().workspaceFileOpenRequest).toBeNull();
			expect(navigateMock).toHaveBeenCalledTimes(1);
		} else {
			expect(within(list).queryByRole("button", { name: "Open in inspector" })).not.toBeInTheDocument();
		}

		await userEvent.click(within(list).getByRole("button", { name: "Show less" }));
		expect(within(list).getAllByRole("button", { name: /^Open artifact / })).toHaveLength(2);
		expect(more).toHaveAttribute("aria-expanded", "false");
	});

	it.each([1, 2])("shows %i artifacts without expansion controls", async (count) => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({
				id: "s-art", title: "Write the report", status: "working", outputType: "artifact",
				artifactFiles: Array.from({ length: count }, (_, index) => artifact(`file-${index}.md`, "2026-01-01T00:00:00Z")),
			})])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");
		const list = await screen.findByTestId("board-session-artifacts");
		expect(within(list).getAllByRole("button")).toHaveLength(count);
		expect(within(list).queryByRole("button", { name: /more|Show less|Open in inspector/ })).not.toBeInTheDocument();
	});

	it.each(["pointer", "keyboard"])("suppresses previews after expansion until intentional %s interaction", async (interaction) => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([boardSession({
				id: "s-art", title: "Write the report", status: "working", outputType: "artifact",
				artifactFiles: ["a.md", "b.md", "c.md"].map((name) => artifact(name, "2026-01-01T00:00:00Z")),
			})])],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");
		await userEvent.click(await screen.findByRole("button", { name: "+1 more" }));
		const row = screen.getByRole("button", { name: "Open artifact c.md" });
		fireEvent.pointerOver(row, { pointerType: "mouse", clientX: 0, clientY: 0 });
		fireEvent.pointerMove(row, { pointerType: "mouse", clientX: 0, clientY: 0 });
		await act(() => new Promise((resolve) => setTimeout(resolve, 350)));
		expect(screen.queryByRole("status", { name: "c.md" })).not.toBeInTheDocument();

		if (interaction === "pointer") {
			fireEvent.pointerMove(row, { pointerType: "mouse", clientX: 10, clientY: 10 });
			expect(screen.queryByRole("status", { name: "c.md" })).not.toBeInTheDocument();
			await act(() => new Promise((resolve) => setTimeout(resolve, 200)));
			fireEvent.pointerMove(row, { pointerType: "mouse", clientX: 20, clientY: 20 });
			await act(() => new Promise((resolve) => setTimeout(resolve, 200)));
			expect(screen.queryByRole("status", { name: "c.md" })).not.toBeInTheDocument();
		} else {
			fireEvent.focus(row);
		}
		expect(await screen.findByRole("status", { name: "c.md" })).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("orders artifacts with equal or unparseable timestamps by path", () => {
		const latest = latestSessionArtifacts(
			[
				artifact("b.md", "2026-01-01T00:00:00Z"),
				artifact("bad.md", "not-a-date"),
				artifact("a.md", "2026-01-01T00:00:00Z"),
			],
			3,
		);
		expect(latest.map((file) => file.name)).toEqual(["a.md", "b.md", "bad.md"]);
	});

	it("shows no artifacts for sessions whose output is not artifacts", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					boardSession({
						id: "s-pr",
						title: "Fix the bug",
						status: "working",
						artifactFiles: [artifact("notes.md", "2026-01-01T00:00:00Z")],
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});
		renderBoard("p1");

		await screen.findByText("Fix the bug");
		expect(screen.queryByTestId("board-session-artifacts")).not.toBeInTheDocument();
	});
});

/** p1 ("radic") and p2 ("other"), each with one archived worker. */
function twoProjectsWithArchives(): WorkspaceSummary[] {
	return [
		workspaceWithSessions([terminatedSession()]),
		{
			id: "p2",
			name: "other",
			path: "/tmp/other",
			kind: "single_repo",
			sessions: [terminatedSession({ id: "s-p2", workspaceId: "p2", workspaceName: "other", title: "p2 worker", branch: "ao/p2-worker", prs: [] })],
		},
	];
}

function workspaceWithSessions(sessions: WorkspaceSession[]): WorkspaceSummary {
	return {
		id: "p1",
		name: "radic",
		path: "/tmp/radic",
		kind: "single_repo",
		sessions,
	};
}

function boardSession(
	overrides: Pick<WorkspaceSession, "id" | "title" | "status"> & Partial<WorkspaceSession>,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		branch: `ao/${overrides.id}`,
		kanbanColumn: toKanbanColumn(undefined, overrides.status),
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function terminatedSession(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "s-dead",
		workspaceId: "p1",
		workspaceName: "radic",
		title: "dead worker",
		issueId: "github:INT-17",
		provider: "claude-code",
		kind: "worker",
		branch: "ao/dead-worker",
		status: "terminated",
		kanbanColumn: "archive",
		isTerminated: true,
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [
			{
				url: "https://github.com/example/radic/pull/42",
				number: 42,
				state: "merged",
				ci: "passing",
				review: "approved",
				mergeability: "mergeable",
				reviewComments: false,
				updatedAt: "2026-01-01T00:00:00Z",
			},
		],
		...overrides,
	};
}
