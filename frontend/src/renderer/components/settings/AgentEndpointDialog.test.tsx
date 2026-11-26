import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentEndpointStatus } from "../../hooks/useAgentEndpoint";
import { AgentEndpointDialog, endpointHost } from "./AgentEndpointDialog";

const endpointMocks = vi.hoisted(() => ({
	setAgentEndpoint: vi.fn(),
	clearAgentEndpoint: vi.fn(),
}));

vi.mock("../../hooks/useAgentEndpoint", () => ({
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

function renderDialog(endpoint: AgentEndpointStatus) {
	const onOpenChange = vi.fn();
	const onChanged = vi.fn();
	render(<AgentEndpointDialog agentId="claude-code" agentName="Claude Code" endpoint={endpoint} hostId="host-a" open onOpenChange={onOpenChange} onChanged={onChanged} />);
	return { onOpenChange, onChanged };
}

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
});

describe("AgentEndpointDialog", () => {
	it("needs a base URL and key before saving, then sends them to the host", async () => {
		endpointMocks.setAgentEndpoint.mockResolvedValue({ ...configured });
		const { onOpenChange, onChanged } = renderDialog(unconfigured);
		const save = screen.getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();

		fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: " https://gateway.example.com " } });
		expect(save).toBeDisabled();
		fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
		fireEvent.change(screen.getByLabelText("Model (optional)"), { target: { value: "claude-sonnet" } });
		fireEvent.click(save);

		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		expect(endpointMocks.setAgentEndpoint).toHaveBeenCalledWith("claude-code", {
			baseUrl: "https://gateway.example.com",
			authStyle: "bearer",
			apiKey: "sk-test",
			model: "claude-sonnet",
		}, "host-a");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("keeps the saved key when the key field stays empty", async () => {
		endpointMocks.setAgentEndpoint.mockResolvedValue({ ...configured });
		renderDialog(configured);
		expect(screen.getByLabelText("Base URL")).toHaveValue("https://gateway.example.com");
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(endpointMocks.setAgentEndpoint).toHaveBeenCalled());
		expect(endpointMocks.setAgentEndpoint.mock.calls[0][1]).not.toHaveProperty("apiKey");
	});

	it("shows the daemon's error and stays open when saving fails", async () => {
		endpointMocks.setAgentEndpoint.mockRejectedValue(new Error("base URL must use http or https"));
		const { onOpenChange, onChanged } = renderDialog(configured);
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("base URL must use http or https");
		expect(onChanged).not.toHaveBeenCalled();
		expect(onOpenChange).not.toHaveBeenCalledWith(false);
	});

	it("removes a configured endpoint", async () => {
		endpointMocks.clearAgentEndpoint.mockResolvedValue({ ...unconfigured });
		const { onChanged } = renderDialog(configured);
		fireEvent.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		expect(endpointMocks.clearAgentEndpoint).toHaveBeenCalledWith("claude-code", "host-a");
	});

	it("offers Remove and asks for a new key when the saved endpoint can't be used", async () => {
		endpointMocks.clearAgentEndpoint.mockResolvedValue({ ...unconfigured });
		const { onChanged } = renderDialog({ ...unconfigured, error: "The saved custom endpoint can't be used." });
		fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: "https://gateway.example.com" } });
		expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
		fireEvent.click(screen.getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(onChanged).toHaveBeenCalled());
		expect(endpointMocks.clearAgentEndpoint).toHaveBeenCalledWith("claude-code", "host-a");
	});

	it("summarizes the endpoint by host", () => {
		expect(endpointHost("https://gateway.example.com:8443/v1")).toBe("gateway.example.com:8443");
		expect(endpointHost("not a url")).toBe("not a url");
		expect(endpointHost(undefined)).toBe("");
	});
});
