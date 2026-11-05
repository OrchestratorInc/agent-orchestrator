import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import type { AgentId } from "../../../lib/agent-options";
import type { WorkspaceSummary } from "../../../types/workspace";
import { IMPORTABLE_AGENTS, SessionImportPanel } from "./SessionImportPanel";
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
it("gates session import to the agents with a readable local history", () => {
	expect(IMPORTABLE_AGENTS.has("claude-code")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("codex")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("droid")).toBe(false);
	expect(IMPORTABLE_AGENTS.has("gemini")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("omp")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("kimchi")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("aider")).toBe(true);
});

it("offers OpenCode imports, which resume from a private copy bound to AO's workspace", () => {
	expect(IMPORTABLE_AGENTS.has("opencode")).toBe(true);
});

it("does not offer Kimi imports while resumed tools use the saved directory", () => {
	expect(IMPORTABLE_AGENTS.has("kimi")).toBe(false);
});

it("does not offer Droid imports while resumed tool cwd is unverified", () => {
	expect(IMPORTABLE_AGENTS.has("droid")).toBe(false);
});
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
it("lists OMP chats for import", async () => {
	api.GET.mockResolvedValue({
		data: { candidates: [{ ...chat, id: "omp-1", title: "OMP chat", projectId: "project", harness: "omp" }] },
	});
	renderPanel("omp");
	expect(await screen.findByRole("checkbox", { name: "Import OMP chat" })).toBeInTheDocument();
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
	fireEvent.click(screen.getByRole("checkbox", { name: "Import Old discussion" }));
	fireEvent.click(screen.getByRole("button", { name: "Import 1 conversation" }));
	expect(await screen.findByText("1 conversation imported.")).toBeInTheDocument();
	// A Standalone chat has no project to show.
	expect(screen.queryByRole("button", { name: "Show project" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Open chat" }));
	expect(openSession).toHaveBeenLastCalledWith(undefined, "standalone-1");
	fireEvent.click(screen.getByRole("button", { name: "Open" }));
	expect(openSession).toHaveBeenCalledTimes(2);
});

it("gates importable harnesses to the ones with a verified resume path", () => {
	expect(IMPORTABLE_AGENTS.has("pi")).toBe(true);
	expect(IMPORTABLE_AGENTS.has("qwen")).toBe(true);
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

it("imports only selected Copilot chats through the existing import flow", async () => {
	expect(IMPORTABLE_AGENTS.has("copilot")).toBe(true);
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
