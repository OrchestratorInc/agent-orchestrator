import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	GET: vi.fn(),
	DELETE: vi.fn(),
	getGitHubStatus: vi.fn(),
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: mocks.GET, DELETE: mocks.DELETE },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));

vi.mock("../../lib/github-daemon", () => ({ getGitHubStatus: mocks.getGitHubStatus }));

import { GitHubAccountRow } from "./GitHubAccountRow";

function renderRow() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<GitHubAccountRow />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.clearAllMocks();
	mocks.GET.mockResolvedValue({ data: { id: "github-auth", label: "GitHub access", satisfied: true, required: false } });
	mocks.DELETE.mockResolvedValue({ data: { status: "ok" } });
});

describe("GitHubAccountRow", () => {
	it("disconnects AO's own GitHub sign-in after confirmation", async () => {
		mocks.getGitHubStatus.mockResolvedValue({ connected: true, login: "octocat" });
		const user = userEvent.setup();
		renderRow();

		expect(await screen.findByText("Connected as @octocat")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Disconnect" }));
		expect(await screen.findByText("Disconnect GitHub?")).toBeInTheDocument();
		await user.click(screen.getAllByRole("button", { name: "Disconnect" }).at(-1)!);

		await waitFor(() => expect(mocks.DELETE).toHaveBeenCalledWith("/api/v1/github/pat"));
	});

	it("offers no disconnect for a sign-in AO does not own", async () => {
		mocks.getGitHubStatus.mockResolvedValue({ connected: false, login: "octocat" });
		renderRow();

		expect(await screen.findByText("Connected through GitHub CLI or an environment token")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Disconnect" })).not.toBeInTheDocument();
	});
});
