import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { conversationQueryKey } from "../hooks/useConversation";
import { sessionWorkspaceFilesQueryKey, type WorkspaceFilesResponse } from "../hooks/useSessionWorkspaceFiles";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import {
	QUICK_DETAILS_INNER_PADDING_PX,
	QUICK_DETAILS_INSET_PX,
	QUICK_DETAILS_RESERVE_PX,
	SessionQuickDetailsFloatingPanel,
	SessionQuickDetailsPanel,
	SessionQuickDetailsTrigger,
} from "./SessionQuickDetails";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("motion/react", () => ({ useReducedMotion: () => false }));

vi.mock("./ArtifactThumbnail", () => ({
	ArtifactThumbnail: ({ row }: { row: { name: string } }) => <span data-testid="mock-artifact-thumb">{row.name}</span>,
}));

const openExternalMock = vi.fn().mockResolvedValue(undefined);

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			openExternal: (...args: unknown[]) => openExternalMock(...args),
		},
		clipboard: {
			writeText: vi.fn().mockResolvedValue(undefined),
		},
		editorHandoff: {
			getState: vi.fn().mockResolvedValue({ workspaceAvailable: true, targets: [] }),
			open: vi.fn().mockResolvedValue(undefined),
			copyWorkspacePath: vi.fn().mockResolvedValue(undefined),
		},
	},
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: {}, ready: false, baseUrl: "" }),
}));

function makeSession(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "proj-1",
		workspaceName: "agent-orchestrator",
		title: "Quick details panel",
		provider: "claude-code",
		kind: "worker",
		mode: "chat",
		branch: "ao/session-panel/quick-details",
		status: "working",
		displayStatus: "Working",
		activity: { state: "active", lastActivityAt: "2026-11-23T08:00:00Z" },
		updatedAt: "2026-11-23T08:00:00Z",
		prs: [
			{
				url: "https://github.com/org/repo/pull/42",
				number: 42,
				state: "open",
				ci: "passing",
				review: "approved",
				mergeability: "mergeable",
				reviewComments: false,
				updatedAt: "2026-11-23T08:00:00Z",
			},
		],
		artifactFiles: [
			{
				id: "art-1",
				kind: "markdown",
				name: "summary.md",
				path: "summary.md",
				size: 120,
				updatedAt: "2026-11-23T08:00:00Z",
				registered: true,
			},
		],
		...overrides,
	};
}

function makeQueryClient() {
	return new QueryClient({
		defaultOptions: {
			queries: { retry: false, staleTime: Infinity },
		},
	});
}

function renderWithProviders(ui: ReactNode, queryClient = makeQueryClient()) {
	return {
		queryClient,
		...render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>{ui}</TooltipProvider>
			</QueryClientProvider>,
		),
	};
}

beforeEach(() => {
	openExternalMock.mockClear();
	useUiStore.setState({ inspectorSessions: {} });
});

describe("SessionQuickDetailsPanel", () => {
	it("shows project, diff stats, separate status & agent/model rows, PR, artifact previews, and quick links without Browser", () => {
		const session = makeSession();
		const queryClient = makeQueryClient();
		const filesData: WorkspaceFilesResponse = {
			sessionId: session.id,
			commits: [],
			summary: { files: 2, additions: 7026, deletions: 767 },
			truncated: false,
			files: [
				{ path: "src/a.ts", status: "modified", additions: 7000, deletions: 700, size: 1000, binary: false },
				{ path: "src/b.ts", status: "added", additions: 26, deletions: 67, size: 500, binary: false },
			],
			sections: { committed: [], staged: [], unstaged: [], untracked: [] },
			compareBaseRef: "origin/main",
		};
		queryClient.setQueryData(sessionWorkspaceFilesQueryKey(session.id), filesData);
		queryClient.setQueryData(conversationQueryKey(session.id), {
			pages: [{ settings: { model: "claude-opus-5-5", reasoningEffort: "medium" } }],
			pageParams: [undefined],
		});

		renderWithProviders(
			<SessionQuickDetailsPanel
				onOpenInspectorView={vi.fn()}
				orchestrator={false}
				session={session}
				subagentsAvailable
			/>,
			queryClient,
		);

		expect(screen.getByText("agent-orchestrator")).toBeInTheDocument();
		const diffStats = screen.getByTestId("quick-details-diff-stats");
		expect(within(diffStats).getByText("+7,026")).toBeInTheDocument();
		expect(within(diffStats).getByText("-767")).toBeInTheDocument();

		// Agent/model row is rendered with human-readable model + effort, without a separate status row above it.
		expect(screen.queryByTestId("quick-details-status-row")).not.toBeInTheDocument();
		expect(screen.getByTestId("quick-details-agent-row")).toHaveTextContent("Claude Code");
		expect(screen.getByTestId("quick-details-agent")).toHaveTextContent("Opus 5.5Medium");
		expect(screen.getByText("#42")).toBeInTheDocument();

		// Artifact preview tile is rendered and Browser link is absent.
		expect(screen.getByTestId("quick-details-link-artifacts")).toBeInTheDocument();
		expect(screen.getByTestId("quick-details-artifact-preview")).toBeInTheDocument();
		expect(screen.queryByTestId("quick-details-link-browser")).not.toBeInTheDocument();
		expect(screen.getByTestId("quick-details-link-reviews")).toBeInTheDocument();
		expect(screen.getByTestId("quick-details-link-subagents")).toBeInTheDocument();
		expect(screen.getByTestId("quick-details-link-summary")).toBeInTheDocument();
	});

	it("jumps to the target inspector tab or opens clicked artifact preview without dismissing quick details state", () => {
		const session = makeSession();
		const onOpenInspectorView = vi.fn();
		const onOpenArtifact = vi.fn();
		const onClose = vi.fn();
		renderWithProviders(
			<SessionQuickDetailsPanel
				onClose={onClose}
				onOpenArtifact={onOpenArtifact}
				onOpenInspectorView={onOpenInspectorView}
				orchestrator={false}
				session={session}
				subagentsAvailable
			/>,
		);

		fireEvent.click(screen.getByTestId("quick-details-changes-row"));
		expect(onClose).not.toHaveBeenCalled();
		expect(onOpenInspectorView).toHaveBeenLastCalledWith("files");

		fireEvent.click(screen.getByTestId("quick-details-artifact-preview"));
		expect(onOpenArtifact).toHaveBeenCalledWith({ path: "summary.md" });

		fireEvent.click(screen.getByTestId("quick-details-link-artifacts"));
		expect(onOpenInspectorView).toHaveBeenLastCalledWith("artifacts");

		fireEvent.click(screen.getByTestId("quick-details-link-reviews"));
		expect(onOpenInspectorView).toHaveBeenLastCalledWith("reviews");

		fireEvent.click(screen.getByTestId("quick-details-link-subagents"));
		expect(onOpenInspectorView).toHaveBeenLastCalledWith("subagents");

		fireEvent.click(screen.getByTestId("quick-details-link-summary"));
		expect(onOpenInspectorView).toHaveBeenLastCalledWith("summary");
	});

	it("lists all PRs with status and opens each PR URL externally when clicked", () => {
		const session = makeSession({
			prs: [
				{
					url: "https://github.com/org/repo/pull/142",
					number: 142,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-11-23T08:20:00Z",
				},
				{
					url: "https://github.com/org/repo/pull/141",
					number: 141,
					state: "open",
					ci: "failing",
					review: "none",
					mergeability: "unstable",
					reviewComments: false,
					updatedAt: "2026-11-23T08:15:00Z",
				},
				{
					url: "https://github.com/org/repo/pull/138",
					number: 138,
					state: "merged",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-11-23T08:00:00Z",
				},
			],
		});
		renderWithProviders(
			<SessionQuickDetailsPanel
				onOpenInspectorView={vi.fn()}
				orchestrator={false}
				session={session}
				subagentsAvailable={false}
			/>,
		);

		const rows = screen.getAllByTestId("quick-details-pr-row");
		expect(rows).toHaveLength(3);
		// sortedPRs orders urgent open PRs first (checks failing -> ready to merge -> merged).
		expect(rows[0]).toHaveTextContent("#141");
		expect(rows[0]).toHaveTextContent("Checks failing");
		expect(rows[1]).toHaveTextContent("#142");
		expect(rows[1]).toHaveTextContent("Ready to merge");
		expect(rows[2]).toHaveTextContent("#138");
		expect(rows[2]).toHaveTextContent("Merged");

		fireEvent.click(rows[0]);
		expect(openExternalMock).toHaveBeenLastCalledWith("https://github.com/org/repo/pull/141");

		fireEvent.click(rows[1]);
		expect(openExternalMock).toHaveBeenLastCalledWith("https://github.com/org/repo/pull/142");

		fireEvent.click(rows[2]);
		expect(openExternalMock).toHaveBeenLastCalledWith("https://github.com/org/repo/pull/138");
	});
});

describe("SessionQuickDetailsFloatingPanel and SessionQuickDetailsTrigger", () => {
	it("renders as a top-right floating card with equal inset and concentric radius (button radius + inner padding)", () => {
		const onDismiss = vi.fn();
		const { container, rerender } = renderWithProviders(
			<>
				<SessionQuickDetailsTrigger isOpen onToggle={vi.fn()} />
				<SessionQuickDetailsFloatingPanel isOpen onDismiss={onDismiss} reserveSpace sessionKey="s1">
					<div>Card body</div>
				</SessionQuickDetailsFloatingPanel>
			</>,
		);

		const card = screen.getByTestId("panel-quick-details");
		const gap = container.querySelector('[data-slot="quick-details-gap"]') as HTMLElement;
		expect(card).toHaveAttribute("data-state", "expanded");
		expect(card).toHaveAttribute("data-mode", "docked");
		expect(card.style.top).toBe(`${QUICK_DETAILS_INSET_PX}px`);
		expect(card.style.right).toBe(`${QUICK_DETAILS_INSET_PX}px`);
		expect(card.style.borderRadius).toBe(`calc(var(--radius-md) + ${QUICK_DETAILS_INNER_PADDING_PX}px)`);
		expect(gap.style.width).toBe(`${QUICK_DETAILS_RESERVE_PX}px`);
		expect(screen.getByText("Card body")).toBeInTheDocument();

		// Narrow mode: exact same top-right card, no reserved horizontal width, dismisses on Escape.
		rerender(
			<QueryClientProvider client={makeQueryClient()}>
				<TooltipProvider>
					<SessionQuickDetailsTrigger isOpen onToggle={vi.fn()} />
					<SessionQuickDetailsFloatingPanel isOpen onDismiss={onDismiss} reserveSpace={false} sessionKey="s1">
						<div>Card body</div>
					</SessionQuickDetailsFloatingPanel>
				</TooltipProvider>
			</QueryClientProvider>,
		);

		expect(card).toHaveAttribute("data-mode", "floating");
		expect(card.style.top).toBe(`${QUICK_DETAILS_INSET_PX}px`);
		expect(card.style.right).toBe(`${QUICK_DETAILS_INSET_PX}px`);
		expect(gap.style.width).toBe("0px");

		fireEvent.keyDown(document, { key: "Escape" });
		expect(onDismiss).toHaveBeenCalledTimes(1);
	});

	it("preserves quick details open state across opening and closing the inspector, and reveals quick details when toggled while paused", () => {
		act(() => {
			useUiStore.getState().toggleQuickDetails("s1", { closeInspector: true });
		});
		expect(useUiStore.getState().inspectorSessions.s1?.quickDetailsOpen).toBe(true);
		expect(useUiStore.getState().inspectorSessions.s1?.isOpen).toBe(false);

		act(() => {
			useUiStore.getState().toggleInspector("s1");
		});
		expect(useUiStore.getState().inspectorSessions.s1?.isOpen).toBe(true);
		expect(useUiStore.getState().inspectorSessions.s1?.quickDetailsOpen).toBe(true);

		act(() => {
			useUiStore.getState().toggleInspector("s1");
		});
		expect(useUiStore.getState().inspectorSessions.s1?.isOpen).toBe(false);
		expect(useUiStore.getState().inspectorSessions.s1?.quickDetailsOpen).toBe(true);

		act(() => {
			useUiStore.getState().setInspectorOpen("s1", true);
		});
		act(() => {
			useUiStore.getState().toggleQuickDetails("s1", { closeInspector: true });
		});
		expect(useUiStore.getState().inspectorSessions.s1?.isOpen).toBe(false);
		expect(useUiStore.getState().inspectorSessions.s1?.quickDetailsOpen).toBe(true);
	});
});
