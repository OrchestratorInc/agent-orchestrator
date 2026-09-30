import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../../i18n";
import type { CloudCpProviderConnection } from "../../lib/cloud-cp";
import { useCredentialDialogStore } from "../../stores/credential-dialog-store";

const state = vi.hoisted(() => ({
	cloudEnabled: true,
	status: "authenticated",
	org: { id: "org-test" } as { id: string } | undefined,
	orgError: undefined as unknown,
	connections: [] as CloudCpProviderConnection[],
	isPending: false,
	isError: false,
}));
const client = vi.hoisted(() => ({
	listUserProviderConnections: vi.fn(),
	putGitHubPAT: vi.fn(),
	deleteGitHubPAT: vi.fn(),
}));
vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: state.cloudEnabled }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: state.status }) }));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: state.org, error: state.orgError }) }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client }) }));
vi.mock("../../hooks/useProviderConnections", () => ({
	useProviderConnections: () => ({ data: state.connections, isPending: state.isPending, isError: state.isError, isSuccess: !state.isPending && !state.isError }),
}));
import { CloudCredentialsSection } from "./CloudCredentialsSection";

function connection(provider: string, credentialType: string, validationState = "valid"): CloudCpProviderConnection {
	return { id: provider, provider, label: "default", config: { credentialType }, validationState, createdAt: "", updatedAt: "" };
}
function renderSettings() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<CloudCredentialsSection />, {
		wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
	});
}

beforeEach(() => {
	vi.clearAllMocks();
	state.cloudEnabled = true;
	state.status = "authenticated";
	state.org = { id: "org-test" };
	state.orgError = undefined;
	state.connections = [connection("codex", "auth_json"), connection("opencode", "openrouter_api_key", "invalid")];
	state.isPending = false;
	state.isError = false;
	client.listUserProviderConnections.mockResolvedValue({ providerConnections: [] });
	client.putGitHubPAT.mockResolvedValue({});
	client.deleteGitHubPAT.mockResolvedValue({});
	useCredentialDialogStore.getState().closeDialog();
});

describe("Cloud settings connections", () => {
	it("shows readable methods and opens Manage for the selected agent", async () => {
		renderSettings();
		expect(screen.getByText("ChatGPT account")).toBeInTheDocument();
		expect(screen.getByText("OpenRouter API key")).toBeInTheDocument();
		expect(screen.getByText("Needs attention")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Manage OpenCode connection" }));
		expect(useCredentialDialogStore.getState()).toMatchObject({ open: true, targetAgent: "opencode", targetCredentialType: "openrouter_api_key" });
		fireEvent.click(screen.getByRole("button", { name: "Connect agent" }));
		expect(useCredentialDialogStore.getState()).toMatchObject({ open: true, targetAgent: null, targetCredentialType: null });
		await waitFor(() => expect(client.listUserProviderConnections).toHaveBeenCalled());
	});

	it("keeps the manual token collapsed and preserves save and remove actions", async () => {
		renderSettings();
		const advanced = screen.getByRole("button", { name: "Advanced repository access" });
		expect(advanced).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("GitHub personal access token")).not.toBeInTheDocument();
		fireEvent.click(advanced);
		await screen.findByText("No manual token saved");
		expect(screen.queryByText("Not connected")).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: " example-token " } });
		client.listUserProviderConnections.mockResolvedValue({ providerConnections: [connection("github", "access_token")] });
		fireEvent.click(screen.getByRole("button", { name: "Save token" }));
		await waitFor(() => expect(client.putGitHubPAT).toHaveBeenCalledWith({ secret: "example-token" }));
		await screen.findByText("Token saved");
		expect(screen.getByLabelText("GitHub personal access token")).toHaveValue("");
		fireEvent.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(client.deleteGitHubPAT).toHaveBeenCalledOnce());
	});

	it("clears an unsaved manual token on collapse and sign-out", async () => {
		const view = renderSettings();
		const advanced = screen.getByRole("button", { name: "Advanced repository access" });
		fireEvent.click(advanced);
		fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "unsaved-token" } });
		fireEvent.click(advanced);
		fireEvent.click(advanced);
		expect(screen.getByLabelText("GitHub personal access token")).toHaveValue("");
		fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "another-unsaved-token" } });
		state.status = "unauthenticated";
		view.rerender(<CloudCredentialsSection />);
		state.status = "authenticated";
		view.rerender(<CloudCredentialsSection />);
		fireEvent.click(screen.getByRole("button", { name: "Advanced repository access" }));
		expect(screen.getByLabelText("GitHub personal access token")).toHaveValue("");
		expect(client.putGitHubPAT).not.toHaveBeenCalled();
		await act(async () => {});
	});

	it("does not present a failed token lookup as a missing connection", async () => {
		client.listUserProviderConnections.mockRejectedValue(new Error("offline"));
		renderSettings();
		fireEvent.click(screen.getByRole("button", { name: "Advanced repository access" }));
		await screen.findByText("Could not load token status");
		expect(screen.queryByText("No manual token saved")).not.toBeInTheDocument();
	});

	it("shows loading and failure states instead of an empty connection list", async () => {
		state.connections = [];
		state.isPending = true;
		const view = renderSettings();
		expect(screen.getByRole("status")).toHaveTextContent("Loading connections");
		expect(screen.queryByText("No coding agents connected yet.")).not.toBeInTheDocument();
		view.unmount();
		state.isPending = false;
		state.isError = true;
		renderSettings();
		expect(screen.getByRole("alert")).toHaveTextContent("Could not load coding agent connections");
		await act(async () => {});
	});

	it("does not fetch credentials when Cloud is disabled or signed out", () => {
		state.cloudEnabled = false;
		const view = renderSettings();
		expect(view.container).toBeEmptyDOMElement();
		view.unmount();
		state.cloudEnabled = true;
		state.status = "unauthenticated";
		renderSettings();
		expect(screen.getByText(/Sign in to AO Cloud/)).toBeInTheDocument();
		expect(client.listUserProviderConnections).not.toHaveBeenCalled();
	});
});
