import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import type { AgentId } from "../../../lib/agent-options";
import type { WorkspaceSummary } from "../../../types/workspace";
import { useUiStore } from "../../../stores/ui-store";
import { SessionImportPanel } from "./SessionImportPanel";
const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../../../lib/api-client", () => ({
	apiClient: api,
	apiErrorCode: (error: { code?: string }) => error.code,
	apiErrorMessage: () => "Import failed",
}));
const openSession = vi.hoisted(() => vi.fn());
vi.mock("../../../lib/navigate-to-session", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../../lib/navigate-to-session")>()),
	useNavigateToSession: () => openSession,
}));

const chat = { lastActivity: "2026-10-06T10:00:00Z", messageCount: 2 };
function renderPanel(harness: AgentId = "claude-code") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(workspaceQueryKey, [{ id: "project", name: "Project", sessions: [] }] as unknown as WorkspaceSummary[]);
	render(
		<QueryClientProvider client={client}>
			<SessionImportPanel harness={harness} />
		</QueryClientProvider>,
	);
}
beforeEach(() => {
	vi.clearAllMocks();
	api.GET.mockReset();
	api.POST.mockReset();
	window.localStorage.clear();
	useUiStore.setState({ sessionImportOfferClosed: false });
	api.GET.mockResolvedValue({
		data: {
			candidates: [
				{ ...chat, id: "recent", title: "Fix parser", projectId: "project", harness: "claude-code" },
				{ ...chat, id: "older", title: "Old discussion", harness: "claude-code", lastActivity: "2026-01-06T10:00:00Z", messageCount: 3, shortened: true },
				{ ...chat, id: "codex", title: "Codex refactor", projectId: "project", harness: "codex" },
			],
		},
	});
	api.POST.mockResolvedValue({ data: { results: [{ id: "older", status: "created", sessionId: "standalone-1" }] } });
});
it("shows only the reading status until the first scan returns", async () => {
	api.GET.mockReturnValue(new Promise(() => {}));
	renderPanel();
	expect(await screen.findByText("Reading local histories…")).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: /^Import \d+/ })).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Scan again" })).not.toBeInTheDocument();
	expect(screen.queryByRole("checkbox", { name: "Select all shown conversations" })).not.toBeInTheDocument();
});
it("lists OMP chats for import", async () => {
	api.GET.mockResolvedValue({
		data: { candidates: [{ ...chat, id: "omp-1", title: "OMP chat", projectId: "project", harness: "omp" }] },
	});
	renderPanel("omp");
	expect(await screen.findByRole("checkbox", { name: "Import OMP chat" })).toBeInTheDocument();
});
it("lists only this agent's chats, preselects the ones not imported and imports only the selection", async () => {
	renderPanel();
	const recent = await screen.findByRole("checkbox", { name: "Import Fix parser" });
	expect(recent).toBeChecked();
	expect(screen.queryByText("Codex refactor")).not.toBeInTheDocument();
	const older = screen.getByRole("checkbox", { name: "Import Old discussion" });
	expect(older).toBeChecked();
	expect(screen.getAllByText("Some text in this chat is too large to import.")).toHaveLength(1);
	fireEvent.click(recent);
	const user = userEvent.setup();
	await user.click(screen.getByRole("combobox", { name: "Project" }));
	await user.click(screen.getByRole("option", { name: "Standalone" }));
	expect(screen.queryByText("Fix parser")).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", { body: { ids: ["older"] } });
	expect(screen.getByText("Already in Orchestrator")).toBeInTheDocument();
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBe("true");
	expect(useUiStore.getState().sessionImportOfferClosed).toBe(true);
});

it("filters the list by a search over titles and projects", async () => {
	renderPanel();
	await screen.findByRole("checkbox", { name: "Import Fix parser" });
	const search = screen.getByRole("searchbox", { name: "Search chats" });
	fireEvent.change(search, { target: { value: "parser" } });
	expect(screen.getByText("Fix parser")).toBeInTheDocument();
	expect(screen.queryByText("Old discussion")).not.toBeInTheDocument();
	fireEvent.change(search, { target: { value: "standalone" } });
	expect(screen.getByText("Old discussion")).toBeInTheDocument();
	fireEvent.change(search, { target: { value: "nothing like this" } });
	expect(screen.getByText("No chats match \u201cnothing like this\u201d.")).toBeInTheDocument();
});

it("opens an imported chat from the result line and from its row", async () => {
	renderPanel();
	fireEvent.click(await screen.findByRole("checkbox", { name: "Import Fix parser" }));
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	// A Standalone chat has no project to show.
	expect(screen.queryByRole("button", { name: "Show project" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Open chat" }));
	expect(openSession).toHaveBeenLastCalledWith(undefined, "standalone-1");
	fireEvent.click(screen.getByRole("button", { name: "Open" }));
	expect(openSession).toHaveBeenCalledTimes(2);
});

it("offers no single chat after a bulk import, and the project only when there is one", async () => {
	api.GET.mockResolvedValue({
		data: {
			candidates: [
				{ ...chat, id: "a", title: "Fix parser", projectId: "project", harness: "claude-code" },
				{ ...chat, id: "b", title: "Add tests", projectId: "project", harness: "claude-code" },
				{ ...chat, id: "c", title: "Loose notes", harness: "claude-code" },
			],
		},
	});
	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "a", status: "created", sessionId: "project-1" }, { id: "b", status: "created", sessionId: "project-2" }] } });
	renderPanel();
	fireEvent.click(await screen.findByRole("checkbox", { name: "Import Loose notes" }));
	fireEvent.click(screen.getByRole("button", { name: "Import 2 conversations" }));
	expect(await screen.findByText("2 conversations imported.")).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Open chat" })).not.toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Show project" })).toBeInTheDocument();
	await waitFor(() => expect(screen.getByRole("checkbox", { name: "Import Loose notes" })).toBeEnabled());

	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "c", status: "created", sessionId: "standalone-1" }] } });
	fireEvent.click(screen.getByRole("checkbox", { name: "Import Loose notes" }));
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Open chat" })).toBeInTheDocument();
});

it("offers neither a chat nor a project after a bulk import across projects", async () => {
	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "recent", status: "created", sessionId: "project-1" }, { id: "older", status: "created", sessionId: "standalone-1" }] } });
	renderPanel();
	fireEvent.click(await screen.findByRole("button", { name: "Import 2 conversations" }));
	expect(await screen.findByText("2 conversations imported.")).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Open chat" })).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Show project" })).not.toBeInTheDocument();
	expect(screen.getAllByRole("button", { name: "Open" })).toHaveLength(2);
});

it("reports new, existing and failed imports separately", async () => {
	api.POST.mockResolvedValue({
		data: {
			results: [
				{ id: "recent", status: "already_imported", sessionId: "project-1" },
				{ id: "older", status: "failed", error: "The source or project changed. Scan again before importing." },
			],
		},
	});
	renderPanel();
	fireEvent.click(await screen.findByRole("button", { name: "Import 2 conversations" }));
	expect(await screen.findByText("1 already in Orchestrator. 1 could not be imported.")).toBeInTheDocument();
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBeNull();
	expect(useUiStore.getState().sessionImportOfferClosed).toBe(false);
});

it("adds a new folder as a project, scans again and imports its chat there", async () => {
	const fresh = { ...chat, id: "new", title: "Add login", harness: "codex" };
	api.GET.mockResolvedValueOnce({ data: { candidates: [{ ...fresh, folder: "/Users/me/code/widgets" }] } })
		.mockResolvedValueOnce({ data: { candidates: [{ ...fresh, projectId: "widgets" }] } });
	api.POST.mockImplementation(async (path: string) =>
		path === "/api/v1/projects"
			? { data: { project: { id: "widgets" } } }
			: { data: { results: [{ id: "new", status: "created", sessionId: "widgets-1" }] } });
	renderPanel("codex");
	expect(await screen.findByText(/widgets \(new project\)/)).toBeInTheDocument();
	expect(screen.getByText("Adds 1 folder as a project: widgets")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	expect(api.POST.mock.calls).toEqual([
		["/api/v1/projects", { body: { path: "/Users/me/code/widgets" } }],
		["/api/v1/session-import", { body: { ids: ["new"] } }],
	]);
	expect(api.GET).toHaveBeenCalledTimes(2);
});

it("stops before importing when a folder cannot be added", async () => {
	api.GET.mockResolvedValue({ data: { candidates: [{ ...chat, id: "new", title: "Add login", harness: "codex", folder: "/Users/me/code/widgets" }] } });
	api.POST.mockResolvedValue({ error: { code: "NOT_A_GIT_REPO" } });
	renderPanel("codex");
	fireEvent.click(await screen.findByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Import failed");
	await waitFor(() => expect(api.POST).toHaveBeenCalledTimes(1));
});

it("imports only selected Copilot chats through the existing import flow", async () => {
	api.GET.mockResolvedValue({ data: { candidates: [
		{ ...chat, id: "copilot-chat", title: "Copilot parser work", harness: "copilot", projectId: "project" },
		{ ...chat, id: "other-chat", title: "Another agent", harness: "claude-code", projectId: "project" },
	] } });
	api.POST.mockResolvedValue({ data: { results: [{ id: "copilot-chat", status: "created", sessionId: "project-copilot-1" }] } });
	renderPanel("copilot");
	expect(await screen.findByRole("checkbox", { name: "Import Copilot parser work" })).toBeChecked();
	expect(screen.queryByText("Another agent")).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", { body: { ids: ["copilot-chat"] } });
});
