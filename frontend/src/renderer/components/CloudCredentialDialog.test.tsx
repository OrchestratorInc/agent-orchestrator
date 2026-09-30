import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCredentialDialog } from "./CloudCredentialDialog";
import { useCredentialDialogStore } from "../stores/credential-dialog-store";

const bridgeMocks = vi.hoisted(() => ({
	connectProviderAuth: vi.fn(),
	cancelProviderAuth: vi.fn(),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		cloud: {
			connectProviderAuth: bridgeMocks.connectProviderAuth,
			cancelProviderAuth: bridgeMocks.cancelProviderAuth,
		},
	},
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: {}, ready: true, baseUrl: "https://cloud.example.test" }),
}));

vi.mock("../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: { id: "org_1" }, isLoading: false, error: null, ready: true }),
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

function renderDialog() {
	useCredentialDialogStore.getState().openDialog("claude-code");
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<CloudCredentialDialog />
		</QueryClientProvider>,
	);
}

async function startAnthropicLogin(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole("button", { name: "Credential type" }));
	await user.click(await screen.findByRole("menuitem", { name: /Log in with Anthropic/ }));
	await user.click(screen.getByRole("button", { name: /Log in with Anthropic/ }));
	await waitFor(() => expect(bridgeMocks.connectProviderAuth).toHaveBeenCalled());
}

describe("CloudCredentialDialog browser login", () => {
	beforeEach(() => {
		bridgeMocks.connectProviderAuth.mockReset();
		bridgeMocks.cancelProviderAuth.mockReset();
		useCredentialDialogStore.getState().closeDialog();
	});

	it("cancels without showing the cancellation as an error", async () => {
		pendingLogin();
		const user = userEvent.setup();
		renderDialog();
		await startAnthropicLogin(user);

		await user.click(screen.getByRole("button", { name: /Cancel/ }));

		expect(bridgeMocks.cancelProviderAuth).toHaveBeenCalledTimes(1);
		expect(await screen.findByRole("button", { name: /Log in with Anthropic/ })).toBeEnabled();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("closes and cancels the login from the close button", async () => {
		pendingLogin();
		const user = userEvent.setup();
		renderDialog();
		await startAnthropicLogin(user);

		const close = screen.getByRole("button", { name: /Close/ });
		expect(close).toBeEnabled();
		await user.click(close);

		expect(bridgeMocks.cancelProviderAuth).toHaveBeenCalledTimes(1);
		expect(useCredentialDialogStore.getState().open).toBe(false);
	});

	it("still shows a real login failure", async () => {
		bridgeMocks.connectProviderAuth.mockRejectedValue(new Error("Claude sign-in did not complete."));
		const user = userEvent.setup();
		renderDialog();
		await startAnthropicLogin(user);

		expect(await screen.findByRole("alert")).toHaveTextContent("Claude sign-in did not complete.");
	});
});
