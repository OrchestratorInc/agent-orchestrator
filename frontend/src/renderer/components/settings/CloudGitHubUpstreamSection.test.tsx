import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { CloudCpError } from "../../lib/cloud-cp";
import type { CloudCpGitHubUserConnection } from "../../lib/cloud-cp";
import { CloudGitHubUpstreamSection } from "./CloudGitHubUpstreamSection";

const mocks = vi.hoisted(() => ({
	client: {
		getGitHubUser: vi.fn(),
		startGitHubUpstreamAuthorization: vi.fn(),
		disconnectGitHubUpstream: vi.fn(),
	},
	openExternal: vi.fn(),
	cloudEnabled: true,
	ready: true,
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: mocks.cloudEnabled }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: mocks.client, ready: mocks.ready, baseUrl: "https://cloud.test" }),
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { app: { openExternal: mocks.openExternal } } }));

function user(upstream?: CloudCpGitHubUserConnection["upstream"], connected = true): CloudCpGitHubUserConnection {
	return { connected, login: connected ? "octocat" : undefined, installations: [], ...(upstream ? { upstream } : {}) };
}

function renderSection() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<CloudGitHubUpstreamSection />
		</QueryClientProvider>,
	);
}

describe("CloudGitHubUpstreamSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		vi.clearAllMocks();
		mocks.cloudEnabled = true;
		mocks.ready = true;
	});

	it("connects in the browser and waits for GitHub", async () => {
		mocks.client.getGitHubUser
			.mockResolvedValueOnce(user({ connected: false }))
			.mockResolvedValue(user({ connected: true, login: "octocat", scopes: "public_repo" }));
		mocks.client.startGitHubUpstreamAuthorization.mockResolvedValue({
			authorizeUrl: "https://github.com/login/oauth/authorize?scope=public_repo",
			expiresAt: "2026-11-25T12:10:00Z",
		});
		renderSection();
		await userEvent.click(await screen.findByRole("button", { name: "Connect" }));
		expect(mocks.openExternal).toHaveBeenCalledWith("https://github.com/login/oauth/authorize?scope=public_repo");
		expect(
			await screen.findByText(
				"Connected as octocat. AO uses this only to open pull requests on the repositories your forks came from.",
				undefined,
				{ timeout: 4000 },
			),
		).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Disconnect" })).toBeInTheDocument();
	});

	it("disconnects", async () => {
		mocks.client.getGitHubUser
			.mockResolvedValueOnce(user({ connected: true, login: "octocat" }))
			.mockResolvedValue(user({ connected: false }));
		mocks.client.disconnectGitHubUpstream.mockResolvedValue(undefined);
		renderSection();
		await userEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
		await waitFor(() => expect(mocks.client.disconnectGitHubUpstream).toHaveBeenCalledTimes(1));
		expect(await screen.findByRole("button", { name: "Connect" })).toBeInTheDocument();
	});

	it("asks to connect GitHub first", async () => {
		mocks.client.getGitHubUser.mockResolvedValue(user({ connected: false }, false));
		renderSection();
		expect(await screen.findByText("Connect GitHub to AO first.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();
	});

	it("says why a connect failed", async () => {
		mocks.client.getGitHubUser.mockResolvedValue(user({ connected: false }));
		mocks.client.startGitHubUpstreamAuthorization.mockRejectedValue(
			new CloudCpError("connect GitHub first", { status: 409, code: "github_not_connected" }),
		);
		renderSection();
		await userEvent.click(await screen.findByRole("button", { name: "Connect" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Connect GitHub to AO first.");
		expect(mocks.openExternal).not.toHaveBeenCalled();
	});

	it("is hidden where the control plane cannot connect it", async () => {
		mocks.client.getGitHubUser.mockResolvedValue(user());
		renderSection();
		await waitFor(() => expect(mocks.client.getGitHubUser).toHaveBeenCalled());
		expect(screen.queryByText("Pull requests on upstream repositories")).not.toBeInTheDocument();
	});

	it("is hidden when cloud is off", () => {
		mocks.cloudEnabled = false;
		renderSection();
		expect(mocks.client.getGitHubUser).not.toHaveBeenCalled();
		expect(screen.queryByText("Pull requests on upstream repositories")).not.toBeInTheDocument();
	});
});
