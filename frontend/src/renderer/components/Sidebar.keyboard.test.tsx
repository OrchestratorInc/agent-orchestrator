import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

// Real DndContext (Sidebar.test.tsx mocks it): proves session rows are wired to
// dnd-kit's KeyboardSensor with Space only, leaving Enter and F2 to the row.

const { getMock, postMock, navigateMock, mockParams } = vi.hoisted(() => ({
	getMock: vi.fn(), postMock: vi.fn(), navigateMock: vi.fn(),
	mockParams: { projectId: undefined as string | undefined, sessionId: undefined as string | undefined },
}));

vi.mock("motion/react", async (importOriginal) => {
	const actual = await importOriginal<typeof import("motion/react")>();
	return { ...actual, AnimatePresence: ({ children }: { children: React.ReactNode }) => children };
});
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ configured: false, session: null, status: "unauthenticated", signIn: vi.fn(), signOut: vi.fn() }),
}));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true, localEnabled: true, client: "" }) }));
vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { trackerIntakeEnabled: true }, isLoading: false, error: undefined }),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	hasTrustedApiBaseUrl: () => false,
	apiErrorMessage: () => "Request failed",
}));
vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => mockParams,
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
			select({ location: { pathname: "/" } }),
	};
});

const session = (id: string, title: string): WorkspaceSession => ({
	id,
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title,
	provider: "claude-code",
	kind: "worker",
	branch: `session/${id}`,
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
});

const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	orchestratorAgent: "claude-code",
	sessions: [session("proj-1-1", "fix login"), session("proj-1-2", "second task")],
};

beforeEach(() => {
	navigateMock.mockReset();
	getMock.mockReset().mockResolvedValue({ data: undefined, error: undefined });
	postMock.mockReset();
	mockParams.projectId = undefined;
	mockParams.sessionId = undefined;
	window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify([workspace.id]));
});

function renderSidebar(project = workspace) {
	const noop = () => vi.fn().mockResolvedValue(undefined);
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const tree = () => (
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<SidebarProvider>
					<Sidebar
						workspaces={[project]}
						onCloneProject={noop()}
						onCreateProject={noop()}
						onInitializeProject={noop()}
						onRemoveProject={noop()}
						onCreateRemoteProject={noop()}
						onInitializeRemoteProject={noop()}
					/>
				</SidebarProvider>
			</TooltipProvider>
		</QueryClientProvider>
	);
	const view = render(tree());
	return { rerender: () => view.rerender(tree()) };
}

describe("Sidebar session rows with the real DndContext", () => {
	it("已有主编排器时显示独立会话，并通过菜单创建新会话", async () => {
		const user = userEvent.setup();
		const primary = { ...session("proj-1-primary", "Orchestrator"), kind: "orchestrator" as const };
		const independent = { ...primary, id: "proj-1-independent", independentOrchestrator: true };
		postMock.mockResolvedValue({ data: { orchestrator: { id: "proj-1-new" } } });
		renderSidebar({ ...workspace, sessions: [primary, independent, ...workspace.sessions] });
		expect(screen.getAllByLabelText(/Open .*Orchestrator/i).length).toBeGreaterThanOrEqual(2);
		expect(screen.getByLabelText("Open Project One orchestrator")).toBeInTheDocument();
		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(screen.getByRole("menuitem", { name: "New orchestrator session" }));
		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ params: { projectId: "proj-1", sessionId: "proj-1-new" } })));
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", { body: { projectId: "proj-1", clean: false, newSession: true } });
	});

	it("将 worker 放入对应编排器，折叠不影响普通会话，打开子会话时自动展开", async () => {
		const user = userEvent.setup();
		const primary = { ...session("primary", "Plan login"), kind: "orchestrator" as const };
		const independent = { ...primary, id: "independent", title: "Plan payments", independentOrchestrator: true };
		const child = { ...session("worker-login", "Implement login"), parentOrchestratorId: primary.id };
		const otherChild = { ...session("worker-payment", "Implement payments"), parentOrchestratorId: independent.id };
		const view = renderSidebar({ ...workspace, sessions: [child, ...workspace.sessions, independent, otherChild, primary] });
		const childList = screen.getByTestId("session-list-proj-1-primary");
		expect(within(childList).getByLabelText("Open Implement login")).toBeInTheDocument();
		expect(within(childList).queryByLabelText("Open fix login")).not.toBeInTheDocument();
		expect(within(screen.getByTestId("session-list-proj-1-independent")).getByLabelText("Open Implement payments")).toBeInTheDocument();
		const toggle = screen.getByRole("button", { name: "Plan login workers (1)" });
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		await user.click(toggle);
		expect(screen.queryByLabelText("Open Implement login")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		expect(screen.getByLabelText("Open Implement payments")).toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = child.id;
		view.rerender();
		await waitFor(() => expect(screen.getByLabelText("Open Implement login")).toHaveAttribute("aria-current", "page"));
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		await user.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		view.rerender();
		expect(toggle).toHaveAttribute("aria-expanded", "false");
	});

	it("starts a keyboard drag on Space and cancels on Escape without opening the session", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const row = screen.getByLabelText("Open fix login");
		row.focus();
		await user.keyboard(" ");
		await waitFor(() => expect(screen.getAllByRole("status").some((el) => /fix login|proj-1-1/.test(el.textContent ?? ""))).toBe(true));
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.getAllByRole("status").some((el) => /cancelled/i.test(el.textContent ?? ""))).toBe(true));
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("对子会话分页，并在同组切换目标时重新展开被隐藏的父级和子行", async () => {
		const user = userEvent.setup();
		const parent = { ...session("parent", "Large task"), kind: "orchestrator" as const, createdAt: "2026-01-01T00:00:00Z" };
		const children = Array.from({ length: 8 }, (_, index) => ({
			...session(`worker-${index}`, `Step ${index}`), parentOrchestratorId: parent.id,
			createdAt: `2026-01-02T0${index}:00:00Z`,
		}));
		const ordinary = Array.from({ length: 6 }, (_, index) => ({
			...session(`ordinary-${index}`, `Ordinary ${index}`), createdAt: "2026-02-01T00:00:00Z",
		}));
		const view = renderSidebar({ ...workspace, sessions: [...ordinary, parent, ...children] });
		expect(screen.queryByLabelText("Open Large task")).not.toBeInTheDocument();
		mockParams.projectId = "proj-1";
		mockParams.sessionId = "worker-7";
		view.rerender();
		const childList = await screen.findByTestId("session-list-proj-1-parent");
		expect(within(childList).getAllByRole("button", { name: /^Open Step/ })).toHaveLength(6);
		await user.click(screen.getByRole("button", { name: "Show 2 more agents" }));
		expect(within(childList).getAllByRole("button", { name: /^Open Step/ })).toHaveLength(8);
		await user.click(screen.getAllByRole("button", { name: "Show fewer agents" }).at(-1)!);
		expect(screen.queryByLabelText("Open Large task")).not.toBeInTheDocument();
		mockParams.sessionId = "worker-0";
		view.rerender();
		await waitFor(() => expect(screen.getByLabelText("Open Step 0")).toHaveAttribute("aria-current", "page"));
	});

	it("leaves Enter to open the row and F2 to rename it", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const row = screen.getByLabelText("Open fix login");
		row.focus();
		await user.keyboard("{Enter}");
		expect(navigateMock).toHaveBeenCalledTimes(1);
		expect(screen.getAllByRole("status").every((el) => !/picked up|cancelled|moved/i.test(el.textContent ?? ""))).toBe(true);
		await user.keyboard("{F2}");
		expect(screen.getByLabelText("Rename fix login")).toHaveFocus();
	});
});
