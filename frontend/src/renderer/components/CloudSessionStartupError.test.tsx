import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudCpError } from "../lib/cloud-cp";
import { CloudSessionStartupError } from "./CloudSessionStartupError";
import { TooltipProvider } from "./ui/tooltip";

const retrySessionStartup = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { retrySessionStartup }, ready: true, baseUrl: "https://cloud.test" }),
}));

function mount(problem: Parameters<typeof CloudSessionStartupError>[0]["problem"], coderWorkspaceUrl?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<CloudSessionStartupError coderWorkspaceUrl={coderWorkspaceUrl} orgId="org-1" sessionId="sess-1" problem={problem} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return { invalidate };
}

describe("CloudSessionStartupError", () => {
	beforeEach(() => {
		retrySessionStartup.mockReset();
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
