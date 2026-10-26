import { expect, it, vi } from "vitest";
const { load, options } = vi.hoisted(() => ({ load: vi.fn(), options: vi.fn((id: string) => ({ queryKey: ["conversation", id] })) }));
vi.mock("@tanstack/react-router", () => ({ createFileRoute: () => (config: unknown) => config }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"], cloudProjectsQueryKey: ["cloud-projects"] }));
vi.mock("./session-view-chunk", () => ({ loadSessionView: load }));
vi.mock("../hooks/useConversation", () => ({ conversationQueryOptions: options }));

it.each([
	"../routes/_shell.projects.$projectId_.sessions.$sessionId",
	"../routes/_shell.sessions.$sessionId",
])("starts conversation loading before the session view resolves: %s", async (path) => {
	let finish!: () => void;
	load.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
	const prefetchInfiniteQuery = vi.fn().mockResolvedValue(undefined);
	const { Route } = await import(path);
	const pending = Route.loader({ context: { queryClient: { prefetchInfiniteQuery, getQueryData: () => [], getQueriesData: () => [] } }, params: { sessionId: "ended" } });
	expect(prefetchInfiniteQuery).toHaveBeenCalledWith({ queryKey: ["conversation", "ended"] });
	finish();
	await pending;
});


it("skips local prefetch for a known Cloud route or TUI session", async () => {
	const { prefetchLocalSessionConversation } = await import("./prefetch-session-conversation");
	const prefetchInfiniteQuery = vi.fn();
	const client = { prefetchInfiniteQuery, getQueryData: () => [{ id: "cloud", kind: "cloud", sessions: [] }, { id: "local", sessions: [{ id: "tui", mode: "tui" }] }] };
	prefetchLocalSessionConversation(client as unknown as import("@tanstack/react-query").QueryClient, "cloud-id", "cloud");
	prefetchLocalSessionConversation(client as unknown as import("@tanstack/react-query").QueryClient, "tui", "local");
	expect(prefetchInfiniteQuery).not.toHaveBeenCalled();
});


it("recognizes Cloud projects in their separate query cache", async () => {
	const { prefetchLocalSessionConversation } = await import("./prefetch-session-conversation");
	const prefetchInfiniteQuery = vi.fn();
	const client = { prefetchInfiniteQuery, getQueryData: () => [], getQueriesData: () => [[[], [{ id: "cloud" }]]] };
	prefetchLocalSessionConversation(client as unknown as import("@tanstack/react-query").QueryClient, "ended-cloud", "cloud");
	expect(prefetchInfiniteQuery).not.toHaveBeenCalled();
});
