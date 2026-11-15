import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

// Real DndContext (Sidebar.test.tsx mocks it): proves session rows are wired to
// dnd-kit's KeyboardSensor with Space only, leaving Enter and F2 to the row.

const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));

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
	apiClient: { GET: getMock, POST: vi.fn() },
	hasTrustedApiBaseUrl: () => false,
	apiErrorMessage: () => "Request failed",
}));
vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => ({}),
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
	window.localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify([workspace.id]));
});

function renderSidebar() {
	const noop = () => vi.fn().mockResolvedValue(undefined);
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<SidebarProvider>
					<Sidebar
						workspaces={[workspace]}
						onCloneProject={noop()}
						onCreateProject={noop()}
						onInitializeProject={noop()}
						onRemoveProject={noop()}
						onCreateRemoteProject={noop()}
						onInitializeRemoteProject={noop()}
					/>
				</SidebarProvider>
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("Sidebar session rows with the real DndContext", () => {
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
