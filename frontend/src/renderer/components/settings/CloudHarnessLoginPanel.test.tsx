import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudHarnessLoginPanel, type CloudHarness } from "./CloudHarnessLoginPanel";

const bridgeMocks = vi.hoisted(() => ({
	connectProviderAuth: vi.fn(),
	cancelProviderAuth: vi.fn(),
}));

const cloudMocks = vi.hoisted(() => ({
	putUserAgentConnection: vi.fn(),
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		cloud: {
			connectProviderAuth: bridgeMocks.connectProviderAuth,
			cancelProviderAuth: bridgeMocks.cancelProviderAuth,
		},
	},
}));

vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { putUserAgentConnection: cloudMocks.putUserAgentConnection },
		ready: true,
		baseUrl: "https://cloud.example.test",
	}),
}));

// Mirror the main process: cancelling aborts the pending login, whose IPC call
// then rejects with the cancellation error.
function pendingLogin() {
	let reject: (err: Error) => void = () => {};
	bridgeMocks.connectProviderAuth.mockImplementation(
		() =>
			new Promise((_resolve, rej) => {
				reject = rej;
			}),
	);
	bridgeMocks.cancelProviderAuth.mockImplementation(async () => {
		reject(new Error("Error invoking remote method 'cloud:connectProviderAuth': Error: Login was cancelled."));
	});
}

function renderPanel(agent: CloudHarness = "claude-code") {
	const onClose = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<CloudHarnessLoginPanel agent={agent} onClose={onClose} />
		</QueryClientProvider>,
	);
	return { onClose };
}

describe("CloudHarnessLoginPanel", () => {
	beforeEach(() => {
		bridgeMocks.connectProviderAuth.mockReset();
		bridgeMocks.cancelProviderAuth.mockReset();
		cloudMocks.putUserAgentConnection.mockReset();
	});

	it("defaults Claude Code to logging in with Anthropic and offers the fallbacks", () => {
		renderPanel();
		expect(screen.getByRole("button", { name: "Log in with Anthropic" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Setup token" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "API key" })).toBeInTheDocument();
		expect(screen.queryByRole("textbox")).toBeNull();
	});

	it("logs in through the browser as a personal credential that also covers local sessions", async () => {
		bridgeMocks.connectProviderAuth.mockResolvedValue(undefined);
		const user = userEvent.setup();
		const { onClose } = renderPanel();
		await user.click(screen.getByRole("button", { name: "Log in with Anthropic" }));

		expect(bridgeMocks.connectProviderAuth).toHaveBeenCalledWith({
			baseUrl: "https://cloud.example.test",
			provider: "claude-code",
			persistLocalClaudeToken: true,
		});
		await waitFor(() => expect(onClose).toHaveBeenCalled());
	});

	it("cancels a pending browser login without showing an error", async () => {
		pendingLogin();
		const user = userEvent.setup();
		const { onClose } = renderPanel();
		await user.click(screen.getByRole("button", { name: "Log in with Anthropic" }));
		expect(screen.getByRole("button", { name: "Waiting for browser…" })).toBeDisabled();

		await user.click(screen.getByRole("button", { name: "Cancel" }));

		expect(bridgeMocks.cancelProviderAuth).toHaveBeenCalledTimes(1);
		expect(await screen.findByRole("button", { name: "Log in with Anthropic" })).toBeEnabled();
		expect(screen.queryByRole("alert")).toBeNull();
		expect(onClose).not.toHaveBeenCalled();
	});

	it("still shows a real login failure", async () => {
		bridgeMocks.connectProviderAuth.mockRejectedValue(new Error("Claude sign-in did not complete."));
		const user = userEvent.setup();
		renderPanel();
		await user.click(screen.getByRole("button", { name: "Log in with Anthropic" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("Claude sign-in did not complete.");
	});

	it("explains how to get a setup token when pasting one", async () => {
		const user = userEvent.setup();
		renderPanel();
		await user.click(screen.getByRole("button", { name: "Setup token" }));

		expect(screen.getByText("claude setup-token")).toBeInTheDocument();
		expect(screen.getByText("sk-ant-oat")).toBeInTheDocument();
		expect(screen.getByLabelText("Setup token")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Log in with Anthropic" })).toBeInTheDocument();
	});

	it("saves a pasted Cursor API key as the user's personal connection", async () => {
		cloudMocks.putUserAgentConnection.mockResolvedValue({ providerConnection: { validationState: "valid" } });
		const user = userEvent.setup();
		const { onClose } = renderPanel("cursor");
		expect(screen.queryByText("Or use")).toBeNull();
		await user.type(screen.getByLabelText("API key"), " cursor-key ");
		await user.click(screen.getByRole("button", { name: "Connect" }));

		expect(cloudMocks.putUserAgentConnection).toHaveBeenCalledWith("cursor", { credentialType: "api_key", secret: "cursor-key" });
		await waitFor(() => expect(onClose).toHaveBeenCalled());
	});
});
