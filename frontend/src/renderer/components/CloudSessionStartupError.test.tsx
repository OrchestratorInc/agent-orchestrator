import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import { CloudSessionStartupError } from "./CloudSessionStartupError";

const retrySessionStartup = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { retrySessionStartup }, ready: true, baseUrl: "https://cloud.test" }),
}));

function mount(problem: Parameters<typeof CloudSessionStartupError>[0]["problem"]) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	render(
		<QueryClientProvider client={client}>
			<CloudSessionStartupError orgId="org-1" sessionId="sess-1" problem={problem} />
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
});
