import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentEndpointStatus } from "../../../hooks/useAgentEndpoint";
import { CustomEndpointMethod, endpointHost, endpointOrigin, endpointSaved } from "./CustomEndpointMethod";

const endpointMocks = vi.hoisted(() => ({
	setAgentEndpoint: vi.fn(),
	clearAgentEndpoint: vi.fn(),
}));

vi.mock("../../../hooks/useAgentEndpoint", () => ({
	setAgentEndpoint: endpointMocks.setAgentEndpoint,
	clearAgentEndpoint: endpointMocks.clearAgentEndpoint,
}));

const unconfigured: AgentEndpointStatus = {
	configured: false,
	hasApiKey: false,
	authStyles: ["bearer", "api-key"],
	defaultAuthStyle: "bearer",
	supportsModel: true,
};

const configured: AgentEndpointStatus = {
	...unconfigured,
	configured: true,
	hasApiKey: true,
	baseUrl: "https://gateway.example.com",
	authStyle: "bearer",
};

function renderMethod(endpoint: AgentEndpointStatus, open = true) {
	const onToggle = vi.fn();
	const onChanged = vi.fn();
	render(<CustomEndpointMethod agentId="claude-code" endpoint={endpoint} hostId="host-a" open={open} onToggle={onToggle} onChanged={onChanged} />);
	return { onToggle, onChanged };
}

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
});

describe("CustomEndpointMethod", () => {
	it("is one collapsed sign-in option that says whether an endpoint is saved", () => {
		const { onToggle } = renderMethod(unconfigured, false);
		const row = screen.getByRole("button", { name: "Custom base URL" });
		expect(row).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByText("Not set")).toBeInTheDocument();
		expect(screen.queryByLabelText("Base URL")).not.toBeInTheDocument();
		expect(screen.queryByText("Send key as")).not.toBeInTheDocument();
		fireEvent.click(row);
		expect(onToggle).toHaveBeenCalled();

		cleanup();
		renderMethod(configured, false);
		expect(screen.getByText("Using gateway.example.com")).toBeInTheDocument();
	});

	it("needs a base URL and key before saving, then sends them to the host", async () => {
		endpointMocks.setAgentEndpoint.mockResolvedValue({ ...configured });
		const { onChanged } = renderMethod(unconfigured);
		const save = screen.getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();

		fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: " https://gateway.example.com " } });
		expect(save).toBeDisabled();
		fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
		expect(screen.queryByLabelText("Model (optional)")).not.toBeInTheDocument();
		fireEvent.click(save);

		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		// How the key is sent and the default model are left to the daemon,
		// which asks the endpoint; models are picked in the Models section.
		expect(endpointMocks.setAgentEndpoint).toHaveBeenCalledWith("claude-code", {
			baseUrl: "https://gateway.example.com",
			apiKey: "sk-test",
		}, "host-a");
	});

	it("keeps the saved key when the key field stays empty, and saves on Enter", async () => {
		endpointMocks.setAgentEndpoint.mockResolvedValue({ ...configured });
		const { onChanged } = renderMethod(configured);
		expect(screen.getByLabelText("Base URL")).toHaveValue("https://gateway.example.com");
		fireEvent.keyDown(screen.getByLabelText("Base URL"), { key: "Enter" });
		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		expect(endpointMocks.setAgentEndpoint.mock.calls[0][1]).not.toHaveProperty("apiKey");
	});

	it("asks for the key again before moving the endpoint to another host", async () => {
		endpointMocks.setAgentEndpoint.mockResolvedValue({ ...configured });
		renderMethod(configured);
		const save = screen.getByRole("button", { name: "Save" });
		// A new path on the same host keeps the saved key.
		fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: "https://gateway.example.com/anthropic" } });
		expect(save).toBeEnabled();
		fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: "https://other.example.com" } });
		expect(save).toBeDisabled();
		expect(screen.getByLabelText("API key")).toHaveAttribute("placeholder", "Enter the API key for the new host");
		fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-other" } });
		fireEvent.click(save);
		await waitFor(() => expect(endpointMocks.setAgentEndpoint).toHaveBeenCalledWith("claude-code", { baseUrl: "https://other.example.com", apiKey: "sk-other" }, "host-a"));
	});

	it("shows the daemon's error and stays open when saving fails", async () => {
		endpointMocks.setAgentEndpoint.mockRejectedValue(new Error("Use https for a remote endpoint."));
		const { onChanged } = renderMethod(configured);
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Use https for a remote endpoint.");
		expect(onChanged).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Base URL")).toBeInTheDocument();
	});

	it("removes a saved endpoint", async () => {
		endpointMocks.clearAgentEndpoint.mockResolvedValue({ ...unconfigured });
		const { onChanged } = renderMethod(configured);
		fireEvent.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		expect(endpointMocks.clearAgentEndpoint).toHaveBeenCalledWith("claude-code", "host-a");
	});

	it("offers no Remove before an endpoint is saved", () => {
		renderMethod(unconfigured);
		expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
	});

	it("marks an unusable endpoint, asks for a new key and offers Remove", async () => {
		endpointMocks.clearAgentEndpoint.mockResolvedValue({ ...unconfigured });
		const unusable = { ...configured, error: "The saved custom endpoint can't be used." };
		const { onChanged } = renderMethod(unusable);
		expect(screen.getByText("Saved endpoint can't be used")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
		fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-new" } });
		expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
		fireEvent.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onChanged).toHaveBeenCalled());
	});

	it("summarizes the endpoint by host", () => {
		expect(endpointHost("https://gateway.example.com:8443/v1")).toBe("gateway.example.com:8443");
		expect(endpointHost("not a url")).toBe("not a url");
		expect(endpointHost(undefined)).toBe("");
		expect(endpointOrigin("https://Gateway.example.com:443/v1")).toBe("https://gateway.example.com");
		expect(endpointOrigin(" not a url ")).toBe("not a url");
		expect(endpointSaved(unconfigured)).toBe(false);
		expect(endpointSaved({ ...unconfigured, error: "x" })).toBe(true);
	});
});
