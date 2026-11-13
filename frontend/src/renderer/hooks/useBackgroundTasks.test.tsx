import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBackgroundTaskReadings, useBackgroundTasks, useStopBackgroundTask } from "./useBackgroundTasks";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, POST: postMock } }));

function BoardConsumer({ projectId }: { projectId?: string }) {
	const query = useBackgroundTaskReadings(true, projectId);
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
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/background-tasks", { params: { query: {} }, signal: expect.any(AbortSignal) });
		view.unmount();
		client.clear();
	});

	it("filters board reads by project and keeps their caches separate", async () => {
		getMock.mockImplementation((_path, options) => Promise.resolve({ data: { sessions: [{
			sessionId: "s", availability: options.params.query.projectId === "one" ? "ready" : "partial", tasks: [],
		}] } }));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><BoardConsumer projectId="one" /><BoardConsumer projectId="two" /></QueryClientProvider>);
		await waitFor(() => {
			expect(screen.getByText("ready")).toBeVisible();
			expect(screen.getByText("partial")).toBeVisible();
		});
		expect(getMock).toHaveBeenCalledTimes(2);
		for (const projectId of ["one", "two"]) {
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/background-tasks", { params: { query: { projectId } }, signal: expect.any(AbortSignal) });
		}
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

describe("stopping a background task", () => {
	beforeEach(() => vi.clearAllMocks());
	it("posts the stop, treats a settled task as done, and rereads every task view", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const outcomes: string[] = [];
		function Stopper({ taskId }: { taskId: string }) {
			const stop = useStopBackgroundTask("s");
			return <button type="button" onClick={() => stop.mutate(taskId, { onSuccess: () => outcomes.push(`ok:${taskId}`), onError: () => outcomes.push(`error:${taskId}`) })}>{taskId}</button>;
		}
		postMock
			.mockResolvedValueOnce({ response: { status: 204 } })
			.mockResolvedValueOnce({ error: { code: "BACKGROUND_TASK_NOT_STOPPABLE" }, response: { status: 409 } })
			.mockResolvedValueOnce({ error: { code: "INTERNAL" }, response: { status: 500 } });
		const view = render(<QueryClientProvider client={client}><Stopper taskId="a" /><Stopper taskId="b" /><Stopper taskId="c" /></QueryClientProvider>);
		for (const id of ["a", "b", "c"]) {
			screen.getByRole("button", { name: id }).click();
			await waitFor(() => expect(outcomes).toHaveLength(["a", "b", "c"].indexOf(id) + 1));
		}
		expect(outcomes).toEqual(["ok:a", "ok:b", "error:c"]);
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/background-tasks/{taskId}/stop", { params: { path: { sessionId: "s", taskId: "a" } } });
		await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(3));
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["background-tasks"] });
		view.unmount();
		client.clear();
	});
});
