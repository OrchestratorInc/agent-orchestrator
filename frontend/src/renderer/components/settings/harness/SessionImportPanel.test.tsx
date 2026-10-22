import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import type { AgentId } from "../../../lib/agent-options";
import type { WorkspaceSummary } from "../../../types/workspace";
import { SessionImportPanel } from "./SessionImportPanel";
const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../../../lib/api-client", () => ({
	apiClient: api,
	apiErrorCode: (error: { code?: string }) => error.code,
	apiErrorMessage: () => "Import failed",
}));
const chat = { suggested: true, lastActivity: "2026-10-06T10:00:00Z", messageCount: 2 };
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
	window.localStorage.clear();
	api.GET.mockResolvedValue({
		data: {
			candidates: [
				{ ...chat, id: "recent", title: "Fix parser", projectId: "project", harness: "claude-code" },
				{ ...chat, id: "older", title: "Old discussion", harness: "claude-code", suggested: false, lastActivity: "2026-01-06T10:00:00Z", messageCount: 3, shortened: true },
				{ ...chat, id: "codex", title: "Codex refactor", projectId: "project", harness: "codex" },
			],
		},
	});
	api.POST.mockResolvedValue({ data: { results: [{ id: "older", status: "created", sessionId: "standalone-1" }] } });
});
it("lists only this agent's chats, preselects recent ones and imports only the selection", async () => {
	renderPanel();
	const recent = await screen.findByRole("checkbox", { name: "Import Fix parser" });
	expect(recent).toBeChecked();
	expect(screen.queryByText("Codex refactor")).not.toBeInTheDocument();
	const older = screen.getByRole("checkbox", { name: "Import Old discussion" });
	expect(older).not.toBeChecked();
	expect(screen.getAllByText("Some text in this chat is too large to import.")).toHaveLength(1);
	fireEvent.click(recent);
	const user = userEvent.setup();
	await user.click(screen.getByRole("combobox", { name: "Project" }));
	await user.click(screen.getByRole("option", { name: "Standalone" }));
	expect(screen.queryByText("Fix parser")).not.toBeInTheDocument();
	fireEvent.click(older);
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", { body: { ids: ["older"] } });
	expect(screen.getByText("Already in AO")).toBeInTheDocument();
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBe("true");
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
	fireEvent.click(await screen.findByRole("checkbox", { name: "Import Old discussion" }));
	fireEvent.click(screen.getByRole("button", { name: "Import 2 conversations" }));
	expect(await screen.findByText("1 already in AO. 1 could not be imported.")).toBeInTheDocument();
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBeNull();
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
