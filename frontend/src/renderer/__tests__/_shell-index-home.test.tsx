import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "../lib/bridge";
import {
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

vi.mock("../components/NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const routeMocks = vi.hoisted(() => ({
	createProjectFlowProps: null as null | {
		firstRunClone?: boolean;
		onFirstRunDismiss?: () => void;
		onFirstRunProgress?: () => void;
		onCreateProject?: (input: never) => Promise<void>;
		existingProjectPaths?: readonly string[];
		onOpenExistingProject?: (path: string) => void | Promise<void>;
		sourceSignal?: { source: string; nonce: number } | null;
	},
	navigate: vi.fn(),
	workspaces: [] as WorkspaceSummary[],
	requirements: [] as Array<{ id: string; label: string; satisfied: boolean; required: boolean; detail: string }>,
	authRequirement: undefined as { id: string; label: string; satisfied: boolean; required: boolean; detail: string } | undefined,
	startGitHubAuth: vi.fn(),
	markAutoLoginOffered: vi.fn(),
	closeTerminal: vi.fn(),
	createProject: vi.fn(),
	cloudEnabled: false,
	migrationOfferShown: false,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => routeMocks.navigate,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: routeMocks.workspaces, isSuccess: true }),
}));

vi.mock("../hooks/useCloudGate", () => ({
	useCloudGate: () => ({ cloudEnabled: routeMocks.cloudEnabled }),
}));

vi.mock("../hooks/useMigrationOffer", () => ({
	useMigrationOffer: () => ({ isPending: false, data: { show: routeMocks.migrationOfferShown } }),
}));

vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({ blocked: false, requirements: routeMocks.requirements, query: { refetch: vi.fn() } }),
	useGitHubAuthRequirement: () => ({ data: routeMocks.authRequirement, isFetching: false, refetch: vi.fn() }),
	useGitHubAuthAutoLoginOffered: () => ({ offered: false, markOffered: routeMocks.markAutoLoginOffered }),
	useGitHubAuthTerminal: () => ({ data: null, clear: vi.fn() }),
	useStartGitHubAuthTerminal: () => ({ mutate: routeMocks.startGitHubAuth, isPending: false, isError: false }),
}));

vi.mock("../hooks/useShellTerminals", () => ({
	useCloseShellTerminal: () => ({ mutate: routeMocks.closeTerminal }),
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({
		daemonStatus: { state: "ready" },
		workspaceStartupState: "ready",
		cloneProject: vi.fn(),
		createProject: routeMocks.createProject,
		initializeProjectRepository: vi.fn(),
	}),
	useShellMaybe: () => ({ daemonStatus: { state: "ready" } }),
}));

vi.mock("../components/CreateProjectFlow", () => ({
	CreateProjectFlow: (props: NonNullable<typeof routeMocks.createProjectFlowProps>) => {
		routeMocks.createProjectFlowProps = props;
		return null;
	},
}));

import { HomePage } from "../components/HomePage";

const standaloneSession = (overrides: Partial<WorkspaceSession>): WorkspaceSession => ({
	id: "standalone-1",
	workspaceId: STANDALONE_WORKSPACE_ID,
	workspaceName: "Scratchpad",
	title: "Ad hoc task",
	provider: "codex",
	kind: "worker",
	status: "idle",
	updatedAt: "2026-06-15T00:00:00Z",
	prs: [],
	...overrides,
});

beforeEach(() => {
	vi.restoreAllMocks();
	useUiStore.setState({ developerMode: false, newTaskRequest: null });
	routeMocks.navigate.mockReset();
	routeMocks.workspaces = [];
	routeMocks.createProjectFlowProps = null;
	routeMocks.requirements = [];
	routeMocks.authRequirement = undefined;
	routeMocks.startGitHubAuth.mockReset();
	routeMocks.markAutoLoginOffered.mockReset();
	routeMocks.closeTerminal.mockReset();
	routeMocks.createProject.mockReset().mockResolvedValue(undefined);
	routeMocks.cloudEnabled = false;
	routeMocks.migrationOfferShown = false;
});

describe("shell index route", () => {
	it("offers the clone prompt once to a fresh empty install", async () => {
		const markSeen = vi.spyOn(aoBridge.appState, "markFirstRunProjectPromptSeen").mockResolvedValue();
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockResolvedValue("pending");
		render(<HomePage />);
		await waitFor(() => expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(true));
		expect(routeMocks.createProjectFlowProps?.sourceSignal?.source).toBe("clone");
		expect(markSeen).not.toHaveBeenCalled();
		act(() => routeMocks.createProjectFlowProps?.onFirstRunDismiss?.());
		await waitFor(() => expect(markSeen).toHaveBeenCalledOnce());
		expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(false);
		expect(screen.queryByRole("button", { name: /replay/i })).not.toBeInTheDocument();
	});

	it("keeps dismissal across a home remount", async () => {
		let status: "pending" | "seen" = "pending";
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockImplementation(async () => status);
		vi.spyOn(aoBridge.appState, "markFirstRunProjectPromptSeen").mockImplementation(async () => { status = "seen"; });
		const view = render(<HomePage />);
		await waitFor(() => expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(true));
		act(() => routeMocks.createProjectFlowProps?.onFirstRunDismiss?.());
		await waitFor(() => expect(status).toBe("seen"));
		view.unmount();
		render(<HomePage />);
		await waitFor(() => expect(aoBridge.appState.getFirstRunProjectPrompt).toHaveBeenCalledTimes(2));
		expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(false);
	});

	it("marks a clone complete only after project registration succeeds", async () => {
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockResolvedValue("pending");
		const markSeen = vi.spyOn(aoBridge.appState, "markFirstRunProjectPromptSeen").mockResolvedValue();
		routeMocks.createProject.mockRejectedValueOnce(new Error("registration failed"));
		render(<HomePage />);
		await waitFor(() => expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(true));
		act(() => routeMocks.createProjectFlowProps?.onFirstRunProgress?.());
		await expect(routeMocks.createProjectFlowProps?.onCreateProject?.({} as never)).rejects.toThrow("registration failed");
		expect(markSeen).not.toHaveBeenCalled();
		await routeMocks.createProjectFlowProps?.onCreateProject?.({} as never);
		await waitFor(() => expect(markSeen).toHaveBeenCalledOnce());
	});

	it("does not offer the prompt to existing users with no projects", async () => {
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockResolvedValue("seen");
		render(<HomePage />);
		await waitFor(() => expect(aoBridge.appState.getFirstRunProjectPrompt).toHaveBeenCalledOnce());
		expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(false);
	});

	it("marks a preexisting project as seen without opening the prompt", async () => {
		routeMocks.workspaces = [{ id: "project", name: "Project", kind: "single_repo", path: "/repo", sessions: [] }];
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockResolvedValue("pending");
		const markSeen = vi.spyOn(aoBridge.appState, "markFirstRunProjectPromptSeen").mockResolvedValue();
		render(<HomePage />);
		await waitFor(() => expect(markSeen).toHaveBeenCalledOnce());
		expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(false);
	});

	it("lets the legacy import offer take priority", async () => {
		routeMocks.migrationOfferShown = true;
		vi.spyOn(aoBridge.appState, "getFirstRunProjectPrompt").mockResolvedValue("pending");
		const markSeen = vi.spyOn(aoBridge.appState, "markFirstRunProjectPromptSeen").mockResolvedValue();
		render(<HomePage />);
		await waitFor(() => expect(markSeen).toHaveBeenCalledOnce());
		expect(routeMocks.createProjectFlowProps?.firstRunClone).toBe(false);
	});
	it("shows the home actions when no projects exist", () => {
		render(<HomePage />);

		expect(screen.getByText("Get started")).toBeInTheDocument();
		expect(screen.queryByText("Jump back right in")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Clone a repo" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open a local folder" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open a workspace" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Start a standalone agent" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Clone to the cloud" })).not.toBeInTheDocument();
		expect(routeMocks.navigate).not.toHaveBeenCalled();
	});

	it("opens the clone flow from the empty home page", () => {
		render(<HomePage />);

		fireEvent.click(screen.getByRole("button", { name: "Clone a repo" }));
		expect(routeMocks.createProjectFlowProps?.sourceSignal?.source).toBe("clone");
	});

	it("swaps standalone for cloud project creation when Developer Mode and Cloud are enabled", () => {
		useUiStore.setState({ developerMode: true });
		routeMocks.cloudEnabled = true;
		render(<HomePage />);

		fireEvent.click(screen.getByRole("button", { name: "Clone to the cloud" }));
		expect(routeMocks.createProjectFlowProps?.sourceSignal?.source).toBe("cloud");
		expect(screen.queryByRole("button", { name: "Start a standalone agent" })).not.toBeInTheDocument();
	});

	it.each([
		{ developerMode: false, cloudEnabled: true },
		{ developerMode: true, cloudEnabled: false },
	])("keeps standalone creation when either toggle is off: %j", ({ developerMode, cloudEnabled }) => {
		useUiStore.setState({ developerMode });
		routeMocks.cloudEnabled = cloudEnabled;
		render(<HomePage />);

		fireEvent.click(screen.getByRole("button", { name: "Start a standalone agent" }));
		expect(useUiStore.getState().newTaskRequest?.projectId).toBe(STANDALONE_WORKSPACE_ID);
		expect(screen.queryByRole("button", { name: "Clone to the cloud" })).not.toBeInTheDocument();
	});

	it("shows cloud creation when standalone sessions exist without a registered project", () => {
		useUiStore.setState({ developerMode: true });
		routeMocks.cloudEnabled = true;
		routeMocks.workspaces = [{
			id: STANDALONE_WORKSPACE_ID,
			name: "Scratchpad",
			kind: STANDALONE_PROJECT_KIND,
			path: "Not attached to a project",
			sessions: [standaloneSession({})],
		}];
		render(<HomePage />);

		fireEvent.click(screen.getByRole("button", { name: "Clone to the cloud" }));
		expect(routeMocks.createProjectFlowProps?.sourceSignal?.source).toBe("cloud");
	});

	it("renders the home page instead of redirecting to a scratch board when projects exist", async () => {
		useUiStore.setState({ developerMode: true });
		routeMocks.cloudEnabled = true;
		routeMocks.workspaces = [
			{
				id: "scratch",
				name: "Scratch",
				kind: "scratch",
				path: "/home/me/.ao/scratch/default",
				sessions: [],
			},
		];

		render(<HomePage />);

		expect(screen.getByText("Jump back right in")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Clone to the cloud" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Start a standalone agent" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Connect mobile" })).not.toBeInTheDocument();
		expect(routeMocks.navigate).not.toHaveBeenCalled();
	});

	it("surfaces missing GitHub authentication on the seeded first-run home page", () => {
		routeMocks.workspaces = [
			{ id: "scratch", name: "Scratch", kind: "scratch", path: "/scratch", sessions: [] },
		];
		routeMocks.requirements = [
			{ id: "gh", label: "gh", satisfied: true, required: false, detail: "/usr/bin/gh" },
		];
		routeMocks.authRequirement = { id: "github-auth", label: "GitHub access", satisfied: false, required: false, detail: "Sign in." };

		render(<HomePage />);

		expect(screen.getByText("Connect GitHub for pull requests")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Sign in with GitHub" })).toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: /Scratch/ }).compareDocumentPosition(
				screen.getByText("Connect GitHub for pull requests"),
			) & Node.DOCUMENT_POSITION_FOLLOWING,
		).toBeTruthy();
	});

	it("opens a project from the recent-project list", async () => {
		routeMocks.workspaces = [
			{ id: "scratch", name: "Scratch", kind: "scratch", path: "/scratch", sessions: [] },
			{ id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one", sessions: [] },
		];

		render(<HomePage />);

		fireEvent.click(screen.getByRole("button", { name: /Project One/ }));
		expect(routeMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		});
	});

	it("keeps the Scratchpad out of recent projects and the heading count", () => {
		routeMocks.workspaces = [
			{
				id: STANDALONE_WORKSPACE_ID,
				name: "Scratchpad",
				kind: STANDALONE_PROJECT_KIND,
				path: "Not attached to a project",
				sessions: [standaloneSession({ status: "terminated", isTerminated: true })],
			},
		];

		render(<HomePage />);

		expect(screen.getByText("Get started")).toBeInTheDocument();
		expect(screen.queryByText("Jump back right in")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Scratchpad/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Start a standalone agent" })).toBeInTheDocument();
	});

	it("lists real projects without the Scratchpad", () => {
		routeMocks.workspaces = [
			{
				id: STANDALONE_WORKSPACE_ID,
				name: "Scratchpad",
				kind: STANDALONE_PROJECT_KIND,
				path: "Not attached to a project",
				sessions: [standaloneSession({})],
			},
			{ id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one", sessions: [] },
		];

		render(<HomePage />);

		expect(screen.queryByText("Get started")).not.toBeInTheDocument();
		expect(screen.getAllByRole("heading").map((heading) => heading.textContent)).toEqual(["Jump back right in"]);
		expect(screen.getByRole("button", { name: /Project One/ })).toBeInTheDocument();
		expect(screen.queryByText("/repo/project-one")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Scratchpad/ })).not.toBeInTheDocument();
	});

	it("opens an already registered path from the import flow", async () => {
		routeMocks.workspaces = [
			{ id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one", sessions: [] },
		];

		render(<HomePage />);

		expect(routeMocks.createProjectFlowProps?.existingProjectPaths).toEqual(["/repo/project-one"]);
		await act(async () => routeMocks.createProjectFlowProps?.onOpenExistingProject?.("/repo/project-one"));
		expect(routeMocks.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId",
			params: { projectId: "proj-1" },
		});
	});

	it("shows only the first three projects", () => {
		routeMocks.workspaces = [
			{ id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one", sessions: [] },
			{ id: "proj-2", name: "Project Two", kind: "single_repo", path: "/repo/project-two", sessions: [] },
			{ id: "proj-3", name: "Project Three", kind: "single_repo", path: "/repo/project-three", sessions: [] },
			{ id: "proj-4", name: "Project Four", kind: "single_repo", path: "/repo/project-four", sessions: [] },
		];

		render(<HomePage />);

		expect(screen.getByRole("button", { name: /Project One/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Project Three/ })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Project Four/ })).not.toBeInTheDocument();
	});
});
