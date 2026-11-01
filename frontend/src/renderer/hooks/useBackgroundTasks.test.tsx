import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBackgroundTaskReadings, useBackgroundTasks } from "./useBackgroundTasks";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock } }));

function BoardConsumer() {
	const query = useBackgroundTaskReadings(true);
	return <span>{query.data?.get("s")?.availability ?? "loading"}</span>;
}

describe("background task queries", () => {
	beforeEach(() => vi.clearAllMocks());
	it("shares one batch read between consumers and passes cancellation to the API", async () => {
		getMock.mockResolvedValue({ data: { sessions: [{ sessionId: "s", availability: "ready", tasks: [], controllerGeneration: "g" }] } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><BoardConsumer /><BoardConsumer /></QueryClientProvider>);
		await waitFor(() => expect(screen.getAllByText("ready")).toHaveLength(2));
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/background-tasks", { signal: expect.any(AbortSignal) });
		view.unmount();
		client.clear();
	});

	it("does not inspect a remote, terminated, or otherwise disabled session", () => {
		function Disabled() { useBackgroundTasks("s", false); return null; }
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><Disabled /></QueryClientProvider>);
		expect(getMock).not.toHaveBeenCalled();
		view.unmount();
		client.clear();
	});

	it.each([
		["session", { status: "ok" }],
		["session", { sessionId: "s", availability: "ready", tasks: null }],
		["board", { status: "ok" }],
		["board", { sessions: [{ sessionId: "s", availability: "ready" }] }],
	])("treats a malformed %s response as a query error without breaking Chat", async (scope, data) => {
		getMock.mockResolvedValue({ data });
		function Consumer() {
			const board = useBackgroundTaskReadings(scope === "board");
			const session = useBackgroundTasks("s", scope === "session");
			const query = scope === "board" ? board : session;
			return <span>{query.isError ? "unavailable" : "loading"}</span>;
		}
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Consumer /></QueryClientProvider>);
		await waitFor(() => expect(screen.getByText("unavailable")).toBeVisible());
		expect(getMock).toHaveBeenCalledTimes(1);
		view.unmount();
		client.clear();
	});
});
