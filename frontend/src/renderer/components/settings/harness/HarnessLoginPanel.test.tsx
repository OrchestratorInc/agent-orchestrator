import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cancelAgentUILogin, getAgentUILogin, startAgentUILogin, type AgentLoginSnapshot } from "../../../hooks/useAgentAuth";
import { aoBridge } from "../../../lib/bridge";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

vi.mock("../../../hooks/useAgentAuth", () => ({ startAgentUILogin: vi.fn(), getAgentUILogin: vi.fn(), cancelAgentUILogin: vi.fn() }));

const methods = [
	{ id: "browser", label: "ChatGPT in browser", secret: false },
	{ id: "device_code", label: "Device code", secret: false },
	{ id: "api_key", label: "OpenAI API key", secret: true },
];
const snapshot: AgentLoginSnapshot = { id: "login-1", agentId: "codex", method: "device_code", status: "running", expiresAt: "2026-10-23T12:00:00Z", loginUrl: "https://auth.openai.com/codex/device", deviceCode: "ABCD-EFGHI" };

function setup() {
	const onVerify = vi.fn().mockResolvedValue(true);
	const onClose = vi.fn();
	const onTerminal = vi.fn();
	const view = render(<HarnessLoginPanel agentId="codex" methods={methods} hostId="remote-1" onVerify={onVerify} onClose={onClose} onTerminal={onTerminal} />);
	return { ...view, onVerify, onClose, onTerminal, user: userEvent.setup() };
}

beforeEach(() => {
	vi.mocked(startAgentUILogin).mockResolvedValue(snapshot);
	vi.mocked(getAgentUILogin).mockResolvedValue(snapshot);
	vi.mocked(cancelAgentUILogin).mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); });

describe("HarnessLoginPanel", () => {
	it("explains the sign-in methods and keeps terminal login under other options", async () => {
		const { user } = setup();
		expect(screen.getByRole("radio", { name: "ChatGPT in browser" })).toHaveAccessibleDescription("Use your ChatGPT account.");
		expect(screen.getByRole("button", { name: "Continue in browser" })).toBeEnabled();
		expect(screen.getByRole("button", { name: "Open terminal login" })).not.toBeVisible();
		await user.click(screen.getByText("Other options"));
		expect(screen.getByRole("button", { name: "Open terminal login" })).toBeVisible();
	});

	it("shows device prompts with browser and copy controls without a terminal", async () => {
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const { user } = setup();
		await user.click(screen.getByRole("radio", { name: "Device code" }));
		await user.click(screen.getByRole("button", { name: "Get sign-in code" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "device_code", "", "remote-1");
		expect(await screen.findByText("ABCD-EFGHI")).toBeVisible();
		expect(screen.queryByRole("radio")).toBeNull();
		expect(screen.getByRole("status")).toHaveTextContent("Enter this code on the sign-in page.");
		await user.click(screen.getByRole("button", { name: "Open sign-in page" }));
		expect(open).toHaveBeenCalledWith(snapshot.loginUrl);
		await user.click(screen.getByRole("button", { name: "Copy code" }));
		expect(copy).toHaveBeenCalledWith("ABCD-EFGHI");
		expect(screen.queryByTestId("harness-auth-terminal")).toBeNull();
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(screen.getByRole("region", { name: "Sign in to Codex" })).toBeVisible();
	});

	it("masks the API key, clears it on submit, and verifies native completion", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, method: "api_key", loginUrl: undefined, deviceCode: undefined, status: "succeeded" });
		const { user, onVerify } = setup();
		await user.click(screen.getByRole("radio", { name: "OpenAI API key" }));
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "api_key", "test-secret", "remote-1");
		await waitFor(() => expect(onVerify).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Connected")).toBeVisible();
		expect(document.body).not.toHaveTextContent("test-secret");
	});

	it("does not claim connection when verification fails", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, status: "succeeded" });
		const { user, onVerify } = setup();
		onVerify.mockResolvedValue(false);
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("could not confirm sign-in");
		expect(screen.queryByText("Connected")).toBeNull();
	});

	it("waits for cancellation before opening terminal login", async () => {
		let finish!: () => void;
		vi.mocked(cancelAgentUILogin).mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
		const { user, onTerminal } = setup();
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		await screen.findByText("ABCD-EFGHI");
		await user.click(screen.getByText("Other options"));
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1");
		expect(onTerminal).not.toHaveBeenCalled();
		await act(async () => finish());
		expect(onTerminal).toHaveBeenCalledTimes(1);
	});

	it("keeps the inline panel open when cancellation fails", async () => {
		vi.mocked(cancelAgentUILogin).mockRejectedValue(new Error("Login is still stopping"));
		const { user, onClose, onTerminal } = setup();
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		await screen.findByText("ABCD-EFGHI");
		await user.click(screen.getByText("Other options"));
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Login is still stopping");
		expect(onTerminal).not.toHaveBeenCalled();
		expect(onClose).not.toHaveBeenCalled();
	});

	it("cancels the background login when Settings unmounts", async () => {
		const { user, unmount } = setup();
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		await screen.findByText("ABCD-EFGHI");
		unmount();
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1");
	});

	it("polls the selected host and verifies when the process finishes", async () => {
		vi.mocked(getAgentUILogin).mockResolvedValue({ ...snapshot, status: "succeeded" });
		const { user, onVerify } = setup();
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		expect(await screen.findByText("Connected", {}, { timeout: 3000 })).toBeVisible();
		expect(getAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1");
		expect(onVerify).toHaveBeenCalledTimes(1);
	});

	it("cancels a late start response after Settings has unmounted", async () => {
		let finish!: (value: AgentLoginSnapshot) => void;
		vi.mocked(startAgentUILogin).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		const { user, unmount } = setup();
		await user.click(screen.getByRole("button", { name: "Continue in browser" }));
		unmount();
		await act(async () => finish(snapshot));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1");
	});
});
