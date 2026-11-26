import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBackgroundTaskOutput, useBackgroundTaskReadings, useBackgroundTasks, useReleaseTaskOutputs, useStopBackgroundTask } from "./useBackgroundTasks";

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

	it("never presents a shell task's description as its command", async () => {
		getMock.mockResolvedValue({ data: { sessionId: "s", availability: "ready", controllerGeneration: "g", tasks: [
			{ id: "claude", kind: "shell", description: "Start the dev server", state: "running" },
			{ id: "codex", kind: "shell", description: "npm run dev", command: "npm run dev", state: "running" },
		] } });
		function Commands() {
			const { data } = useBackgroundTasks("s", true);
			return <span>{data ? data.tasks.map((task) => `${task.id}:${task.command ?? "none"}`).join(",") : "loading"}</span>;
		}
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Commands /></QueryClientProvider>);
		expect(await screen.findByText("claude:none,codex:npm run dev")).toBeVisible();
		view.unmount();
		client.clear();
	});

	it("rechecks a host that has no background task endpoint once a minute", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		getMock.mockResolvedValue({ error: { message: "not found" }, response: { status: 404 } });
		function Consumer() {
			const query = useBackgroundTasks("s", true);
			return <span>{query.isError ? "unavailable" : "loading"}</span>;
		}
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Consumer /></QueryClientProvider>);
		await waitFor(() => expect(screen.getByText("unavailable")).toBeVisible());
		await vi.advanceTimersByTimeAsync(20_000);
		expect(getMock).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(40_000);
		expect(getMock).toHaveBeenCalledTimes(2);
		view.unmount();
		client.clear();
		vi.useRealTimers();
	});

	it.each([
		["session", { status: "ok" }],
		["session", { sessionId: "s", availability: "ready", tasks: null }],
		["session", { sessionId: "s", availability: "ready", tasks: [null] }],
		["session", { sessionId: "s", availability: "ready", tasks: ["bad"] }],
		["board", { sessions: [{ sessionId: "s", availability: "ready", tasks: [null] }] }],
		["board", { sessions: [{ sessionId: "s", availability: "ready", tasks: [7] }] }],
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

describe("a background task's output", () => {
	beforeEach(() => vi.clearAllMocks());
	function Output({ runId = "r1", over = true }: { runId?: string; over?: boolean }) {
		const query = useBackgroundTaskOutput("s", "dev", runId, { enabled: true, live: false, over });
		const error = query.isError ? ":error" : "";
		return <span>{query.data ? `${query.data.availability}:${query.data.output}:${query.data.lastRead ? "last" : "live"}${error}` : "loading"}</span>;
	}
	const reply = (availability: string, output: string) => ({ data: { sessionId: "s", taskId: "dev", availability, output } });

	it("rechecks a live task's output once a minute on a host without the output route", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		getMock.mockResolvedValue({ error: { message: "not found" }, response: { status: 404 } });
		function Live() {
			const query = useBackgroundTaskOutput("s", "dev", "r1", { enabled: true, live: true, over: false });
			return <span>{query.isError ? "unavailable" : "loading"}</span>;
		}
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Live /></QueryClientProvider>);
		await waitFor(() => expect(screen.getByText("unavailable")).toBeVisible());
		await vi.advanceTimersByTimeAsync(5_000);
		expect(getMock).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(55_000);
		expect(getMock).toHaveBeenCalledTimes(2);
		view.unmount();
		client.clear();
		vi.useRealTimers();
	});

	it("keeps the last output read when the daemon no longer has it, across remounts", async () => {
		getMock.mockResolvedValueOnce(reply("ready", "ready on :3000"));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Output /></QueryClientProvider>);
		await screen.findByText("ready:ready on :3000:live");
		// The chat ended: the daemon answers unavailable.
		getMock.mockResolvedValue(reply("unavailable", ""));
		await client.refetchQueries();
		await screen.findByText("ready:ready on :3000:last");
		// Never collected, so reopening it minutes later still shows it.
		expect(client.getQueryCache().find({ queryKey: ["background-task-output", "s", "dev", "r1"] })?.gcTime).toBe(Infinity);
		// Reopening the task later still shows it.
		view.unmount();
		const again = render(<QueryClientProvider client={client}><Output /></QueryClientProvider>);
		await client.refetchQueries();
		await screen.findByText("ready:ready on :3000:last");
		again.unmount();
		client.clear();
		// Nothing read before: unavailable stays unavailable.
		const fresh = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={fresh}><Output /></QueryClientProvider>);
		await screen.findByText("unavailable::live");
		fresh.clear();
	});

	it("treats an unavailable read of a running task as a failed poll, and never shows another run's output", async () => {
		getMock.mockResolvedValueOnce(reply("ready", "watching"));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Output over={false} /></QueryClientProvider>);
		await screen.findByText("ready:watching:live");
		getMock.mockResolvedValue(reply("unavailable", ""));
		await client.refetchQueries();
		// Still running: the output read so far stays, not marked last read.
		await screen.findByText("ready:watching:live:error");
		view.unmount();
		// A rerun under the same task id starts with nothing read.
		render(<QueryClientProvider client={client}><Output runId="r2" /></QueryClientProvider>);
		await screen.findByText("unavailable::live");
		client.clear();
	});

	it("never stores a newer run's output under the run it was read for", async () => {
		getMock.mockResolvedValueOnce({ data: { ...reply("ready", "first run ready").data, runId: "r1" } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><Output over={false} /></QueryClientProvider>);
		await screen.findByText("ready:first run ready:live");
		// The provider reused the task ID: the daemon now reads run r2.
		getMock.mockResolvedValue({ data: { ...reply("ready", "second run ready").data, runId: "r2" } });
		await client.refetchQueries();
		await screen.findByText("ready:first run ready:live");
		view.unmount();
		client.clear();
		// Never read before, the earlier run shows none of it.
		const fresh = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={fresh}><Output /></QueryClientProvider>);
		await screen.findByText("unavailable::live");
		fresh.clear();
	});

	it("keeps the log read before when the daemon leaves it out, followed by what the task printed", async () => {
		getMock.mockResolvedValueOnce(reply("ready", "first run ready"));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><Output /></QueryClientProvider>);
		await screen.findByText("ready:first run ready:live");
		// A restart took the log over: the final read holds only the task's own marker.
		getMock.mockResolvedValue({ data: { ...reply("ready", "\n[killed]").data, logOmitted: true } });
		await client.refetchQueries();
		// The matcher collapses the line break to a space.
		await screen.findByText("ready:first run ready [killed]:live");
		// Later reads keep it, without repeating the marker.
		await client.refetchQueries();
		await screen.findByText("ready:first run ready [killed]:live");
		// An empty provider tail keeps the log rather than "No output yet".
		getMock.mockResolvedValue({ data: { ...reply("ready", "").data, logOmitted: true } });
		await client.refetchQueries();
		await screen.findByText("ready:first run ready:live");
		client.clear();
	});
});

describe("releasing pruned task output", () => {
	function Release({ tabs }: { tabs: { task: { id: string; runId?: string } }[] }) {
		useReleaseTaskOutputs("s", tabs);
		return null;
	}

	it("frees the output of runs the side panel no longer lists, and keeps the rest", () => {
		const client = new QueryClient();
		for (const [taskId, runId] of [["kept", "r1"], ["pruned", "r1"], ["kept", "r0"]]) {
			client.setQueryData(["background-task-output", "s", taskId, runId], { output: `${taskId} ${runId}` });
		}
		client.setQueryData(["background-task-output", "other", "pruned", "r1"], { output: "another session" });
		render(<QueryClientProvider client={client}><Release tabs={[{ task: { id: "kept", runId: "r1" } }]} /></QueryClientProvider>);
		expect(client.getQueryData(["background-task-output", "s", "kept", "r1"])).toEqual({ output: "kept r1" });
		expect(client.getQueryData(["background-task-output", "s", "pruned", "r1"])).toBeUndefined();
		expect(client.getQueryData(["background-task-output", "s", "kept", "r0"])).toBeUndefined();
		expect(client.getQueryData(["background-task-output", "other", "pruned", "r1"])).toEqual({ output: "another session" });
		client.clear();
	});
});

describe("stopping a background task", () => {
	beforeEach(() => vi.clearAllMocks());
	it("posts the stop, treats a settled task as done, and rereads only the stopped session", async () => {
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
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["background-tasks", "s"] });
		view.unmount();
		client.clear();
	});
});
