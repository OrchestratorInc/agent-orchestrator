import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { useSessionImportStore } from "../stores/session-import-store";
import { useUiStore } from "../stores/ui-store";
import { ProjectImportOffer } from "./ProjectImportOffer";
import { SessionImportDialog } from "./SessionImportDialog";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: api,
	apiErrorCode: (error: { code?: string }) => error.code,
	apiErrorMessage: () => "Import failed",
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	workspaceQueryOptions: {
		queryKey: ["workspaces"],
		queryFn: () => Promise.resolve([{ id: "todo", name: "todo-api", path: "/code/todo-api", sessions: [] }]),
	},
}));

const chat = (id: string, harness: string, extra: Record<string, unknown>) => ({
	id,
	harness,
	title: `Chat ${id}`,
	lastActivity: `2026-11-0${id.length}T10:00:00Z`,
	messageCount: 2,
	suggested: true,
	...extra,
});

function renderWith(node: ReactNode, candidates: unknown[], agents = [{ id: "claude-code", installation: { state: "installed" }, authentication: { state: "authorized" } }]) {
	api.GET.mockResolvedValue({ data: { candidates } });
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(agentReadinessQueryKey, { agents });
	return render(
		<QueryClientProvider client={client}>
			{node}
			<SessionImportDialog />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	api.GET.mockReset();
	api.POST.mockReset();
	window.localStorage.clear();
	useSessionImportStore.setState({ dialog: null, dismissedProjects: [], importedCounts: {} });
	useUiStore.setState({ sessionImportOfferClosed: false, globalToasts: [] });
});

it("imports this project's chats from the row and confirms it", async () => {
	renderWith(<ProjectImportOffer projectId="todo" />, [
		chat("a", "claude-code", { projectId: "todo" }),
		chat("bb", "codex", { projectId: "todo" }),
		chat("ccc", "codex", { projectId: "todo", sessionId: "existing" }),
		chat("dddd", "codex", { projectId: "other" }),
	]);
	expect(await screen.findByText("2 earlier chats in this folder")).toBeInTheDocument();
	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "bb", status: "created", sessionId: "s1" }, { id: "a", status: "created", sessionId: "s2" }] } });
	fireEvent.click(screen.getByRole("button", { name: "Import" }));
	expect(await screen.findByText("Imported 2 chats into todo-api")).toBeInTheDocument();
	expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", { body: { ids: ["bb", "a"] } });
});

it("hides the row for this project once dismissed", async () => {
	renderWith(<ProjectImportOffer projectId="todo" />, [chat("a", "claude-code", { projectId: "todo" })]);
	fireEvent.click(await screen.findByRole("button", { name: "Hide for this project" }));
	expect(screen.queryByText("1 earlier chat in this folder")).not.toBeInTheDocument();
	expect(JSON.parse(window.localStorage.getItem("ao.sessionImport.dismissedProjects") ?? "[]")).toEqual(["todo"]);
});

it("reviews the project's chats, marks a signed-out agent and imports the checked ones", async () => {
	renderWith(
		<ProjectImportOffer projectId="todo" />,
		[chat("a", "claude-code", { projectId: "todo" }), chat("bb", "codex", { projectId: "todo" })],
		[
			{ id: "claude-code", installation: { state: "installed" }, authentication: { state: "authorized" } },
			{ id: "codex", installation: { state: "installed" }, authentication: { state: "unauthorized" } },
		],
	);
	fireEvent.click(await screen.findByRole("button", { name: "Review" }));
	const dialog = await screen.findByRole("dialog", { name: "Import chats into todo-api" });
	expect(within(dialog).getByText(/Sign in to continue/)).toBeInTheDocument();
	fireEvent.click(within(dialog).getByRole("checkbox", { name: "Import Chat a" }));
	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "bb", status: "created", sessionId: "s1" }] } });
	fireEvent.click(within(dialog).getByRole("button", { name: "Import 1 chat" }));
	await waitFor(() => expect(useSessionImportStore.getState().dialog).toBeNull());
	expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", { body: { ids: ["bb"] } });
	expect(useSessionImportStore.getState().importedCounts).toEqual({ todo: 1 });
});

it("preselects busy folders, folds the rest and adds the picked ones as projects", async () => {
	useSessionImportStore.setState({ dialog: { kind: "folders" } });
	renderWith(null, [
		chat("a", "claude-code", { folder: "/code/alpha" }),
		chat("bb", "claude-code", { folder: "/code/alpha" }),
		chat("ccc", "claude-code", { folder: "/code/alpha" }),
		chat("dddd", "claude-code", { folder: "/code/beta" }),
		chat("eeeee", "claude-code", {}),
	]);
	const dialog = await screen.findByRole("dialog", { name: "Import projects and their chats" });
	expect(await within(dialog).findByRole("checkbox", { name: "Import alpha" })).toBeChecked();
	expect(within(dialog).queryByRole("checkbox", { name: "Import beta" })).not.toBeInTheDocument();
	fireEvent.click(within(dialog).getByRole("button", { name: "Other folders (1)" }));
	expect(within(dialog).getByRole("checkbox", { name: "Import beta" })).not.toBeChecked();
	api.POST.mockResolvedValueOnce({ data: {} });
	api.POST.mockResolvedValueOnce({ data: { results: ["a", "bb", "ccc"].map((id) => ({ id, status: "created", sessionId: id })) } });
	fireEvent.click(within(dialog).getByRole("button", { name: "Import 1 folder" }));
	await waitFor(() => expect(useSessionImportStore.getState().dialog).toBeNull());
	expect(api.POST).toHaveBeenNthCalledWith(1, "/api/v1/projects", { body: { path: "/code/alpha" } });
	expect(api.POST).toHaveBeenNthCalledWith(2, "/api/v1/session-import", { body: { ids: ["a", "bb", "ccc"] } });
	expect(useUiStore.getState().sessionImportOfferClosed).toBe(true);
	expect(useUiStore.getState().globalToasts.map((toast) => toast.title)).toEqual(["Imported 3 chats"]);
});
