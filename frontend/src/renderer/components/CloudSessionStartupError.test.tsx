import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudCpError } from "../lib/cloud-cp";
import { CloudSessionStartupError } from "./CloudSessionStartupError";
import { TooltipProvider } from "./ui/tooltip";

const retrySessionStartup = vi.hoisted(() => vi.fn());
const requestWorkspaceCheckout = vi.hoisted(() => vi.fn());
const listChatEvents = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { retrySessionStartup, requestWorkspaceCheckout, listChatEvents }, ready: true, baseUrl: "https://cloud.test" }),
}));
const subscribeSessionEvents = vi.hoisted(() => vi.fn());
vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeSessionEventsBridged: (options: unknown) => subscribeSessionEvents(options),
}));

function mount(problem: Parameters<typeof CloudSessionStartupError>[0]["problem"], coderWorkspaceUrl?: string, ended?: boolean) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const view = (next: typeof problem) => (
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<CloudSessionStartupError coderWorkspaceUrl={coderWorkspaceUrl} ended={ended} orgId="org-1" sessionId="sess-1" problem={next} />
			</TooltipProvider>
		</QueryClientProvider>
	);
	const { rerender } = render(view(problem));
	return { invalidate, rerender: (next: typeof problem) => rerender(view(next)) };
}

describe("CloudSessionStartupError", () => {
	beforeEach(() => {
		retrySessionStartup.mockReset();
		requestWorkspaceCheckout.mockReset();
		subscribeSessionEvents.mockReset();
		subscribeSessionEvents.mockResolvedValue(undefined);
		listChatEvents.mockReset();
		listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
	});

	it("adds the failed Setup run's step log and keeps the server detail one click away", async () => {
		const step = (sequence: number, payload: Record<string, unknown>) => ({
			sessionId: "sess-1", sequence, type: "workspace.script.step", createdAt: "2026-10-14T10:00:00Z",
			payload: { phase: "setup", runId: "run-1", total: 2, repository: "", ...payload },
		});
		listChatEvents.mockResolvedValue({
			events: [
				{ sessionId: "sess-1", sequence: 1, type: "worker.ready", payload: {}, createdAt: "2026-10-14T10:00:00Z" },
				step(2, { step: 1, status: "failed", command: "./bin/bootstrap", exitCode: 3, durationMs: 1200, output: "missing secret", hint: "Add the secret in Project settings." }),
				step(3, { step: 2, status: "skipped", command: "npm test" }),
			],
			hasMore: false, nextAfter: 3,
		});
		mount({ phase: "failed", code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", detail: "missing secret" });
		const panel = await screen.findByTestId("script-run-panel");
		expect(listChatEvents).toHaveBeenCalledWith("org-1", "sess-1", expect.objectContaining({ after: 0, limit: 500 }), expect.anything());
		expect(panel).toHaveTextContent("Failed at step 1");
		expect(panel).toHaveTextContent("Add the secret in Project settings.");
		expect(screen.getByRole("alert")).not.toContainElement(panel);
		expect(screen.queryByTestId("cloud-session-startup-detail")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByTestId("cloud-session-startup-detail")).toHaveTextContent("missing secret");
	});

	it("shows a failed project Setup step with its output and retries it through the worker", async () => {
		requestWorkspaceCheckout.mockResolvedValue({ requested: true });
		const message = "Setup step 1 failed: exit status 3. Fix the project's Setup script (Project settings > Scripts), then reopen this session to retry.";
		mount({ phase: "failed", code: "project_setup_failed", message, detail: "starting per-session stack\nmissing secret" });
		expect(screen.getByRole("heading", { name: "The project's Setup script failed" })).toBeInTheDocument();
		expect(screen.getByText(message)).toBeInTheDocument();
		const output = screen.getByTestId("cloud-session-startup-detail");
		expect(output).toHaveTextContent("missing secret");
		expect(output.className).toContain("font-mono");

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(requestWorkspaceCheckout).toHaveBeenCalledWith("org-1", "sess-1");
		expect(retrySessionStartup).not.toHaveBeenCalled();
	});

	it("shows the workspace being prepared again after Retry until a newer failure arrives", async () => {
		requestWorkspaceCheckout.mockResolvedValue({ requested: true });
		const failure = { phase: "failed" as const, code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", detail: "missing secret", at: "2026-10-14T10:00:00Z" };
		const { rerender } = mount(failure);
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText(/Preparing the workspace again/)).toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Preparing the workspace" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
		expect(screen.queryByTestId("cloud-session-startup-detail")).not.toBeInTheDocument();

		// The same step failed again: the new failure and its output show, and Retry is back.
		rerender({ ...failure, detail: "still missing", at: "2026-10-14T10:05:00Z" });
		expect(screen.getByRole("heading", { name: "The project's Setup script failed" })).toBeInTheDocument();
		expect(screen.getByText("Setup step 1 failed: exit status 3.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		expect(screen.getByTestId("cloud-session-startup-detail")).toHaveTextContent("still missing");
	});

	it("says the agent is starting again after Retry on a terminal agent that could not start", async () => {
		requestWorkspaceCheckout.mockResolvedValue({ requested: true });
		mount({ phase: "failed", code: "agent_start_failed", message: "The coding agent did not start.", at: "2026-10-14T10:00:00Z" });
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("Starting the agent again…")).toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Starting the agent" })).toBeInTheDocument();
		expect(screen.queryByText(/Setup is long/)).not.toBeInTheDocument();
		expect(requestWorkspaceCheckout).toHaveBeenCalledWith("org-1", "sess-1");
	});

	it("leaves the retrying state when the retried Setup fails again, even if the failure is re-sent unchanged", async () => {
		const step = (sequence: number, payload: Record<string, unknown>) => ({
			sessionId: "sess-1", sequence, type: "workspace.script.step", createdAt: "2026-10-14T10:00:00Z",
			payload: { phase: "setup", total: 1, step: 1, command: "./bin/bootstrap", repository: "", ...payload },
		});
		listChatEvents.mockResolvedValue({ events: [step(2, { runId: "run-1", status: "failed", exitCode: 3 })], hasMore: false, nextAfter: 2 });
		requestWorkspaceCheckout.mockResolvedValue({ requested: true });
		const failure = { phase: "failed" as const, code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", at: "2026-10-14T10:00:00Z" };
		const { rerender } = mount(failure);
		expect(await screen.findByText("Failed at step 1")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("heading", { name: "Preparing the workspace" })).toBeInTheDocument();
		await waitFor(() => expect(subscribeSessionEvents).toHaveBeenCalled());
		const { onEvent } = subscribeSessionEvents.mock.calls.at(-1)![0] as { onEvent: (event: unknown) => void };
		// The old failed run is not the retry's: still preparing.
		act(() => onEvent(step(2, { runId: "run-1", status: "failed", exitCode: 3 })));
		expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
		act(() => onEvent(step(5, { runId: "run-2", status: "running" })));
		expect(await screen.findByText("Running step 1 of 1")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
		act(() => onEvent(step(6, { runId: "run-2", status: "failed", exitCode: 3 })));
		// The same failure, re-sent with the same `at`.
		rerender({ ...failure });
		expect(await screen.findByRole("heading", { name: "The project's Setup script failed" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		expect(screen.getByText("Failed at step 1")).toBeInTheDocument();
		// Another Retry waits for a run after this one.
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("button", { name: "Retrying…" })).toBeDisabled();
	});

	it("stops waiting on a retry that nothing picked up", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			requestWorkspaceCheckout.mockResolvedValue({ requested: true });
			mount({ phase: "failed", code: "repository_checkout_failed", message: "Could not clone the repository.", at: "2026-10-14T10:00:00Z" });
			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			expect(await screen.findByRole("button", { name: "Retrying…" })).toBeDisabled();
			await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60_000); });
			expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps retrying, not failing, while an idle-paused workspace wakes for the Retry", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			// The control plane wakes the paused workspace on the first request
			// and refuses it until the worker reconnects.
			const waking = new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" });
			requestWorkspaceCheckout.mockRejectedValueOnce(waking).mockRejectedValueOnce(waking).mockResolvedValue({ requested: true });
			mount({ phase: "failed", code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", at: "2026-10-14T10:00:00Z" });
			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			expect(await screen.findByRole("button", { name: "Retrying…" })).toBeDisabled();
			expect(screen.queryByText(/Couldn't retry/)).not.toBeInTheDocument();
			await act(async () => { await vi.advanceTimersByTimeAsync(2_500); });
			expect(requestWorkspaceCheckout).toHaveBeenCalledTimes(3);
			expect(await screen.findByRole("heading", { name: "Preparing the workspace" })).toBeInTheDocument();
			expect(screen.queryByText(/Couldn't retry/)).not.toBeInTheDocument();
		} finally {
			vi.useRealTimers();
		}
	});

	it("reports a workspace that does not wake for the Retry", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			requestWorkspaceCheckout.mockRejectedValue(new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" }));
			mount({ phase: "failed", code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", at: "2026-10-14T10:00:00Z" });
			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			await act(async () => { await vi.advanceTimersByTimeAsync(3 * 60_000 + 2_000); });
			expect(await screen.findByText("Couldn't retry: The session worker is not connected.")).toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("offers no Retry for a deleted session whose Setup failed, and says to restore it", () => {
		mount({ phase: "failed", code: "project_setup_failed", message: "Setup step 1 failed: exit status 3.", detail: "missing secret" }, undefined, true);
		expect(screen.getByRole("heading", { name: "The project's Setup script failed" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
		expect(screen.getByText("This session was deleted. Restore it to retry.")).toBeInTheDocument();
		// The failure's output stays readable.
		expect(screen.getByTestId("cloud-session-startup-detail")).toHaveTextContent("missing secret");
		expect(requestWorkspaceCheckout).not.toHaveBeenCalled();
		expect(retrySessionStartup).not.toHaveBeenCalled();
	});

	it("names a repository checkout failure", () => {
		mount({ phase: "failed", code: "repository_checkout_failed", message: "Repository checkout failed. Check this project's GitHub access, then reopen this session to retry." });
		expect(screen.getByRole("heading", { name: "The repository checkout failed" })).toBeInTheDocument();
		expect(screen.queryByTestId("cloud-session-startup-detail")).not.toBeInTheDocument();
	});

	it("names a working folder the template already created", () => {
		const message = "~/work/alice already exists (created by your Coder template). Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it. Start a new session after changing it.";
		mount({ phase: "failed", code: "working_directory_occupied", message });
		expect(screen.getByRole("heading", { name: "The working folder is already in use" })).toBeInTheDocument();
		expect(screen.getByText(message)).toBeInTheDocument();
	});

	it("names a working folder outside the durable root", () => {
		const message = "The working folder must be inside your home folder (/home/ubuntu) so the agent's work survives restarts. This project's working folder is /workspace/alice. Change it in Project settings > Repositories, then start a new session.";
		mount({ phase: "failed", code: "working_directory_not_durable", message });
		expect(screen.getByRole("heading", { name: "The working folder isn't kept across restarts" })).toBeInTheDocument();
		expect(screen.getByText(message)).toBeInTheDocument();
	});

	it("shows a pending retry, then refetches the session", async () => {
		let resolve!: (value: unknown) => void;
		retrySessionStartup.mockReturnValue(new Promise((done) => { resolve = done; }));
		const { invalidate } = mount({ phase: "failed", message: "This workspace's CPU architecture (armv7l) isn't supported." });
		expect(screen.getByRole("heading", { name: "This session couldn't start" })).toBeInTheDocument();
		expect(screen.getByText("This workspace's CPU architecture (armv7l) isn't supported.")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(retrySessionStartup).toHaveBeenCalledWith("org-1", "sess-1");
		expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();

		await act(async () => resolve({ session: { id: "sess-1" } }));
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-sessions"] }));
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-session"] });
		expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
	});

	it("surfaces a retry failure inline and allows another attempt", async () => {
		retrySessionStartup.mockRejectedValueOnce(new Error("network down")).mockResolvedValueOnce({ session: { id: "sess-1" } });
		mount({ phase: "failed", message: "AO couldn't open a terminal in the workspace." });

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("Couldn't retry: network down")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(screen.queryByText("Couldn't retry: network down")).not.toBeInTheDocument());
		expect(retrySessionStartup).toHaveBeenCalledTimes(2);
	});

	it("explains a session that can no longer be retried", async () => {
		retrySessionStartup.mockRejectedValue(new CloudCpError("not retryable", { status: 409, code: "startup_retry_unavailable" }));
		mount({ phase: "failed", message: "Your Coder workspace wasn't ready after 20 minutes." });

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("This session can't be retried right now.")).toBeInTheDocument();
	});

	it("offers no retry for a runtime that ended without a startup reason", () => {
		mount({ phase: "unavailable" });
		expect(screen.getByRole("heading", { name: "This session isn't running" })).toBeInTheDocument();
		expect(screen.getByText("Its cloud workspace is no longer running.")).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("reveals and copies bring-your-own diagnostics", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		Object.assign(navigator, { clipboard: { writeText } });
		mount({ phase: "failed", code: "bootstrap_failed", message: "Your Coder workspace failed to start.", detail: "build log tail: quota exceeded" });
		expect(screen.queryByTestId("cloud-session-startup-detail")).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByTestId("cloud-session-startup-detail").textContent).toContain("quota exceeded");
		await userEvent.click(screen.getByRole("button", { name: "Copy details" }));
		expect(writeText).toHaveBeenCalledWith(expect.stringContaining("bootstrap_failed"));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
	});

	it("drops the Copied confirmation for a different failure", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		Object.assign(navigator, { clipboard: { writeText } });
		const failure = { phase: "failed" as const, code: "bootstrap_failed", message: "Your Coder workspace failed to start.", detail: "build log tail" };
		const { rerender } = mount(failure);
		await userEvent.click(screen.getByRole("button", { name: "Show details" }));
		await userEvent.click(screen.getByRole("button", { name: "Copy details" }));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		rerender({ ...failure, message: "The workspace failed to build." });
		expect(await screen.findByRole("button", { name: "Copy details" })).toBeTruthy();
	});

	it("resets the Copied confirmation after a moment", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		Object.assign(navigator, { clipboard: { writeText } });
		mount({ phase: "failed", code: "bootstrap_failed", message: "Your Coder workspace failed to start.", detail: "build log tail" });
		await userEvent.click(screen.getByRole("button", { name: "Show details" }));
		await userEvent.click(screen.getByRole("button", { name: "Copy details" }));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		expect(await screen.findByRole("button", { name: "Copy details" }, { timeout: 3000 })).toBeTruthy();
	});

	it("explains a workspace whose ready check never passed, with the probe output behind Show details", async () => {
		mount({
			phase: "failed",
			code: "workspace_setup_not_ready",
			message: "The workspace ready check (grep -q '^Ready' ~/.bootstrap-status) did not pass within 15 minutes.",
			detail: "bootstrap: installing docker...",
		});
		expect(screen.getByRole("heading", { name: "The workspace setup didn't finish" })).toBeInTheDocument();
		expect(screen.getByText(/did not pass within 15 minutes/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		await userEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByTestId("cloud-session-startup-detail").textContent).toContain("installing docker");
	});

	it("shows no details control without diagnostics", () => {
		mount({ phase: "failed", message: "AO couldn't open a terminal in the workspace." });
		expect(screen.queryByRole("button", { name: "Show details" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Open the workspace in Coder" })).toBeNull();
	});

	it("links the bring-your-own Coder workspace next to Show details", async () => {
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const url = "https://coder.acme.example.com/@dev/dev-3f2a9c1e7b4d";
		mount({ phase: "failed", code: "bootstrap_failed", message: "Your Coder workspace failed to start.", detail: "build log tail" }, url);
		const link = screen.getByRole("button", { name: "Open the workspace in Coder" });
		expect(link.parentElement).toBe(screen.getByRole("button", { name: "Show details" }).parentElement);
		await userEvent.click(link);
		expect(openExternal).toHaveBeenCalledWith(url);
		openExternal.mockRestore();
	});

	it("links the workspace even without diagnostics", () => {
		mount({ phase: "unavailable" }, "https://coder.acme.example.com/@ao-bot/ao-0123456789abcdef01234567");
		expect(screen.getByRole("button", { name: "Open the workspace in Coder" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show details" })).toBeNull();
	});
});
