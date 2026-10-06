import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionImportDialog } from "./SessionImportDialog";
const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: api,
	apiErrorMessage: () => "Import failed",
}));
beforeEach(() => {
	vi.clearAllMocks();
	api.GET.mockResolvedValue({
		data: {
			truncated: false,
			candidates: [
				{
					id: "recent",
					title: "Fix parser",
					projectId: "project",
					harness: "claude-code",
					suggested: true,
					lastActivity: "2026-10-06T10:00:00Z",
					messageCount: 2,
				},
				{
					id: "older",
					title: "Old discussion",
					harness: "codex",
					suggested: false,
					lastActivity: "2026-01-06T10:00:00Z",
					messageCount: 3,
				},
			],
		},
	});
	api.POST.mockResolvedValue({
		data: {
			results: [{ id: "older", status: "created", sessionId: "standalone-1" }],
		},
	});
});
it("preselects recent histories, lets older standalone work be selected and imports only the selection", async () => {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	render(
		<QueryClientProvider client={client}>
			<SessionImportDialog projects={[{ id: "project", name: "Project" }]} />
		</QueryClientProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Import sessions" }));
	const recent = await screen.findByRole("checkbox", {
		name: "Import Fix parser",
	});
	expect(recent).toBeChecked();
	const older = screen.getByRole("checkbox", { name: "Import Old discussion" });
	expect(older).not.toBeChecked();
	fireEvent.click(recent);
	fireEvent.change(screen.getByLabelText("Project"), {
		target: { value: "standalone" },
	});
	expect(screen.queryByText("Fix parser")).not.toBeInTheDocument();
	fireEvent.click(older);
	fireEvent.click(
		screen.getByRole("button", { name: "Import 1 conversation" }),
	);
	await waitFor(() =>
		expect(api.POST).toHaveBeenCalledWith("/api/v1/session-import", {
			body: { ids: ["older"] },
		}),
	);
	expect(await screen.findByText("Already in AO")).toBeInTheDocument();
	expect(api.GET).toHaveBeenCalledTimes(1);
});
