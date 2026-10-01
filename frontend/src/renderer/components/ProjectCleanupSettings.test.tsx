import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { ProjectCleanupSettings } from "./ProjectCleanupSettings";

const { getMock, putMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock, POST: postMock }, apiErrorMessage: () => "request failed" }));

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: { id: "p", name: "Example", kind: "single_repo", config: { preRemove: ["docker compose down", "rm -rf .cache/tmp"], autoReview: true } } } });
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
	postMock.mockReset().mockResolvedValue({ data: { ok: true, cleaned: ["session-1"], alreadyGone: [], skipped: [{ sessionId: "session-2", reason: "workspace has uncommitted changes" }] } });
});

it("retries cleanup for this project and shows preserved workspaces", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectCleanupSettings projectId="p" /></QueryClientProvider>);
	await userEvent.click(await screen.findByRole("button", { name: "Retry cleanup" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/cleanup", { params: { query: { project: "p" } } }));
	expect(await screen.findByText(/session-2: workspace has uncommitted changes/)).toBeInTheDocument();
});

it("keeps cleanup steps ordered and saves the rest of the config", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectCleanupSettings projectId="p" /></QueryClientProvider>);
	expect(await screen.findByLabelText("Step 1")).toHaveValue("docker compose down");
	expect(screen.getByLabelText("Step 2")).toHaveValue("rm -rf .cache/tmp");
	await userEvent.clear(screen.getByLabelText("Step 2"));
	await userEvent.type(screen.getByLabelText("Step 2"), "rm -rf .tmp");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config).toEqual({ preRemove: ["docker compose down", "rm -rf .tmp"], autoReview: true });
});
