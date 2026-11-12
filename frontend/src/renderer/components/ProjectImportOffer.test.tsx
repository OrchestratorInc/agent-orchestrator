import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { importChats, MAX_IMPORT } from "../lib/session-import";
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

it("keeps a partial failure next to the confirmation", async () => {
	renderWith(<ProjectImportOffer projectId="todo" />, [chat("a", "claude-code", { projectId: "todo" }), chat("bb", "codex", { projectId: "todo" })]);
	expect(await screen.findByText("2 earlier chats in this folder")).toBeInTheDocument();
	api.POST.mockResolvedValueOnce({ data: { results: [{ id: "a", status: "created", sessionId: "s1" }, { id: "bb", status: "failed", error: "changed" }] } });
	fireEvent.click(screen.getByRole("button", { name: "Import" }));
	expect(await screen.findByText("Imported 1 chat into todo-api")).toBeInTheDocument();
	expect(screen.getByRole("alert")).toHaveTextContent("1 could not be imported.");
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

it("stays open while importing, so a failure that lands after Esc is still shown", async () => {
	renderWith(null, [chat("a", "claude-code", { projectId: "todo" })]);
	act(() => useSessionImportStore.getState().openProjectImport("todo"));
	const dialog = await screen.findByRole("dialog", { name: "Import chats into todo-api" });
	let respond!: (value: unknown) => void;
	api.POST.mockReturnValueOnce(new Promise((resolve) => (respond = resolve)));
	fireEvent.click(await within(dialog).findByRole("button", { name: "Import 1 chat" }));
	expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
	fireEvent.keyDown(dialog, { key: "Escape" });
	act(() => respond({ data: { results: [{ id: "a", status: "failed", error: "changed" }] } }));
	expect(await within(dialog).findByRole("alert")).toHaveTextContent("Chat a: changed");
	fireEvent.keyDown(dialog, { key: "Escape" });
	await waitFor(() => expect(useSessionImportStore.getState().dialog).toBeNull());
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
	api.GET.mockResolvedValueOnce({ data: { candidates: ["a", "bb", "ccc"].map((id) => chat(id, "claude-code", { projectId: "alpha" })) } });
	api.POST.mockResolvedValueOnce({ data: { results: ["a", "bb", "ccc"].map((id) => ({ id, status: "created", sessionId: `s-${id}` })) } });
	fireEvent.click(within(dialog).getByRole("button", { name: "Import 1 folder" }));
	await waitFor(() => expect(useSessionImportStore.getState().dialog).toBeNull());
	expect(api.POST).toHaveBeenNthCalledWith(1, "/api/v1/projects", { body: { path: "/code/alpha" } });
	expect(api.POST).toHaveBeenNthCalledWith(2, "/api/v1/session-import", { body: { ids: ["a", "bb", "ccc"] } });
	expect(useUiStore.getState().sessionImportOfferClosed).toBe(true);
	const [toast] = useUiStore.getState().globalToasts;
	expect(toast?.title).toBe("Imported 3 chats");
	expect(toast?.action).toBeUndefined();
	expect(useSessionImportStore.getState().importedCounts).toEqual({ alpha: 3 });
});

it("keeps a folder with a failed chat picked and says why", async () => {
	useSessionImportStore.setState({ dialog: { kind: "folders" } });
	renderWith(null, ["a", "bb", "ccc"].map((id) => chat(id, "claude-code", { folder: "/code/alpha" })));
	const dialog = await screen.findByRole("dialog", { name: "Import projects and their chats" });
	await within(dialog).findByRole("checkbox", { name: "Import alpha" });
	api.POST.mockResolvedValueOnce({ data: {} });
	api.GET.mockResolvedValue({
		data: { candidates: ["a", "bb", "ccc"].map((id) => chat(id, "claude-code", { projectId: "alpha", ...(id === "bb" ? {} : { sessionId: `s-${id}` }) })) },
	});
	api.POST.mockResolvedValueOnce({
		data: {
			results: [
				{ id: "a", status: "created", sessionId: "s-a" },
				{ id: "bb", status: "failed", error: "The chat changed on disk" },
				{ id: "ccc", status: "created", sessionId: "s-ccc" },
			],
		},
	});
	fireEvent.click(within(dialog).getByRole("button", { name: "Import 1 folder" }));
	expect(await within(dialog).findByText("Chat bb: The chat changed on disk")).toBeInTheDocument();
	expect(within(dialog).getByRole("checkbox", { name: "Import alpha" })).toBeChecked();
	expect(within(dialog).getByRole("button", { name: "Import 1 folder" })).toBeEnabled();
});

it("keeps earlier batches when a later one fails", async () => {
	const ids = Array.from({ length: MAX_IMPORT + 1 }, (_, i) => `c${i}`);
	api.POST.mockResolvedValueOnce({ data: { results: ids.slice(0, MAX_IMPORT).map((id) => ({ id, status: "created", sessionId: `s-${id}` })) } });
	api.POST.mockResolvedValueOnce({ error: { code: "INTERNAL" } });
	const results = await importChats(new QueryClient(), "project_offer", ids);
	expect(results.filter((result) => result.status === "created")).toHaveLength(MAX_IMPORT);
	expect(results.at(-1)).toEqual({ id: `c${MAX_IMPORT}`, status: "failed", error: "Import failed" });
});
