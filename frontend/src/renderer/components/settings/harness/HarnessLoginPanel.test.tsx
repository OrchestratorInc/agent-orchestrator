import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cancelAgentUILogin, getAgentUILogin, startAgentUILogin, submitAgentUILoginCode, type AgentLoginSnapshot } from "../../../hooks/useAgentAuth";
import { aoBridge } from "../../../lib/bridge";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

vi.mock("../../../hooks/useAgentAuth", () => ({ startAgentUILogin: vi.fn(), getAgentUILogin: vi.fn(), cancelAgentUILogin: vi.fn(), submitAgentUILoginCode: vi.fn() }));

const methods = [
	{ id: "browser", label: "ChatGPT in browser", secret: false },
	{ id: "device_code", label: "Device code", secret: false },
	{ id: "api_key", label: "OpenAI API key", secret: true },
];
const snapshot: AgentLoginSnapshot = { id: "login-1", agentId: "codex", method: "device_code", status: "running", expiresAt: "2026-10-23T12:00:00Z", loginUrl: "https://auth.openai.com/codex/device", deviceCode: "ABCD-EFGHI" };

function setup(hostId?: string) {
	const onVerify = vi.fn().mockResolvedValue(true);
	const onClose = vi.fn();
	const onTerminal = vi.fn();
	const view = render(<HarnessLoginPanel agentId="codex" methods={methods} hostId={hostId} onVerify={onVerify} onClose={onClose} onTerminal={onTerminal} />);
	return { ...view, onVerify, onClose, onTerminal, user: userEvent.setup() };
}

beforeEach(() => {
	vi.mocked(startAgentUILogin).mockResolvedValue(snapshot);
	vi.mocked(getAgentUILogin).mockResolvedValue(snapshot);
	vi.mocked(cancelAgentUILogin).mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); });

describe("HarnessLoginPanel", () => {
	it.each([undefined, "local"])("explains local sign-in methods and keeps terminal login under other options (%s)", async (hostId) => {
		const { user } = setup(hostId);
		expect(screen.getByRole("button", { name: "ChatGPT in browser" })).toHaveAccessibleDescription("Use your ChatGPT account.");
		expect(screen.getByRole("button", { name: "ChatGPT in browser" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: "Continue in browser" })).toBeNull();
		expect(screen.getByRole("button", { name: "Open terminal login" })).not.toBeVisible();
		await user.click(screen.getByText("Other options"));
		expect(screen.getByRole("button", { name: "Open terminal login" })).toBeVisible();
	});

	it("uses device-code or credentials on a remote host instead of a localhost browser callback", async () => {
		const { user } = setup("remote-1");
		expect(screen.queryByRole("button", { name: "ChatGPT in browser" })).toBeNull();
		await user.click(screen.getByRole("button", { name: "Device code" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "device_code", "", "remote-1");
	});

	it("submits a masked Claude fallback code from a remote host and verifies native completion", async () => {
		const claude: AgentLoginSnapshot = { ...snapshot, agentId: "claude-code", method: "console", acceptsCode: true, loginUrl: "https://platform.claude.com/oauth/authorize?code=true", deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(claude);
		vi.mocked(getAgentUILogin).mockResolvedValue(claude);
		vi.mocked(submitAgentUILoginCode).mockResolvedValue({ ...claude, acceptsCode: false, loginUrl: undefined });
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const onVerify = vi.fn().mockResolvedValue(true);
		render(<HarnessLoginPanel agentId="claude-code" methods={[{ id: "console", label: "Anthropic Console", secret: false }]} hostId="remote-1" onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: "Anthropic Console" }));
		const codeInput = await screen.findByLabelText("Sign-in code");
		expect(open).toHaveBeenCalledOnce();
		expect(open).toHaveBeenCalledWith(claude.loginUrl);
		expect(codeInput).toHaveAttribute("type", "password");
		await user.type(codeInput, "one-time-code#state");
		await user.click(screen.getByRole("button", { name: "Submit code" }));
		expect(submitAgentUILoginCode).toHaveBeenCalledWith("claude-code", "login-1", "one-time-code#state", "remote-1");
		expect(screen.queryByLabelText("Sign-in code")).toBeNull();
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
		vi.mocked(getAgentUILogin).mockResolvedValue({ ...claude, status: "succeeded", acceptsCode: false, loginUrl: undefined });
		expect(await screen.findByText("Connected", {}, { timeout: 3_000 })).toBeVisible();
		expect(onVerify).toHaveBeenCalledTimes(1);
	});

	it("starts device-code sign-in on selection and opens the sign-in page without a terminal", async () => {
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const { user } = setup();
		await user.click(screen.getByRole("button", { name: "Device code" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "device_code", "", undefined);
		expect(await screen.findByText("ABCD-EFGHI")).toBeVisible();
		expect(screen.queryByRole("button", { name: "Device code" })).toBeNull();
		expect(screen.getByRole("status")).toHaveTextContent("Enter this code on the sign-in page.");
		await waitFor(() => expect(open).toHaveBeenCalledOnce());
		expect(open).toHaveBeenCalledWith(snapshot.loginUrl);
		await user.click(screen.getByRole("button", { name: "Open sign-in page" }));
		expect(open).toHaveBeenCalledTimes(2);
		await user.click(screen.getByRole("button", { name: "Copy code" }));
		expect(copy).toHaveBeenCalledWith("ABCD-EFGHI");
		expect(screen.queryByTestId("harness-auth-terminal")).toBeNull();
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(screen.getByRole("region", { name: "Sign in to Codex" })).toBeVisible();
	});

	it("masks the API key, clears it on submit, and verifies native completion", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, method: "api_key", loginUrl: undefined, deviceCode: undefined, status: "succeeded" });
		const { user, onVerify } = setup();
		await user.click(screen.getByRole("button", { name: "OpenAI API key" }));
		expect(startAgentUILogin).not.toHaveBeenCalled();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "api_key", "test-secret", undefined);
		await waitFor(() => expect(onVerify).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Connected")).toBeVisible();
		expect(document.body).not.toHaveTextContent("test-secret");
	});

	it.each([
		["browser", "ChatGPT in browser"],
		["device_code", "Device code"],
	])("discards an unsaved API key when starting %s sign-in", async (method, label) => {
		const { user } = setup();
		await user.click(screen.getByRole("button", { name: "OpenAI API key" }));
		await user.type(screen.getByLabelText("API key"), "unsaved-test-secret");
		await user.click(screen.getByRole("button", { name: label }));
		expect(startAgentUILogin).toHaveBeenCalledOnce();
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", method, "", undefined);
		expect(screen.queryByLabelText("API key")).toBeNull();
	});

	it("starts browser sign-in on selection and leaves opening the browser to the CLI", async () => {
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const browser = { ...snapshot, method: "browser", loginUrl: "https://auth.openai.com/oauth/authorize", deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(browser);
		vi.mocked(getAgentUILogin).mockResolvedValue(browser);
		const { user } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		expect(startAgentUILogin).toHaveBeenCalledOnce();
		expect(startAgentUILogin).toHaveBeenCalledWith("codex", "browser", "", undefined);
		expect(await screen.findByRole("button", { name: "Open sign-in page" })).toBeVisible();
		expect(open).not.toHaveBeenCalled();
	});

	it("does not claim connection when verification fails", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, status: "succeeded" });
		const { user, onVerify } = setup();
		onVerify.mockResolvedValue(false);
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("could not confirm sign-in");
		expect(screen.queryByText("Connected")).toBeNull();
	});

	it("waits for cancellation before opening terminal login", async () => {
		let finish!: () => void;
		vi.mocked(cancelAgentUILogin).mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
		const { user, onTerminal } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		await screen.findByText("ABCD-EFGHI");
		await user.click(screen.getByText("Other options"));
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", undefined);
		expect(onTerminal).not.toHaveBeenCalled();
		await act(async () => finish());
		expect(onTerminal).toHaveBeenCalledTimes(1);
	});

	it("keeps the inline panel open when cancellation fails", async () => {
		vi.mocked(cancelAgentUILogin).mockRejectedValue(new Error("Login is still stopping"));
		const { user, onClose, onTerminal } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		await screen.findByText("ABCD-EFGHI");
		await user.click(screen.getByText("Other options"));
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Login is still stopping");
		expect(onTerminal).not.toHaveBeenCalled();
		expect(onClose).not.toHaveBeenCalled();
	});

	it("cancels the background login when Settings unmounts", async () => {
		const { user, unmount } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		await screen.findByText("ABCD-EFGHI");
		unmount();
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", undefined);
	});

	it("polls the selected host and verifies when the process finishes", async () => {
		vi.mocked(getAgentUILogin).mockResolvedValue({ ...snapshot, status: "succeeded" });
		const { user, onVerify } = setup("remote-1");
		await user.click(screen.getByRole("button", { name: "Device code" }));
		expect(await screen.findByText("Connected", {}, { timeout: 3000 })).toBeVisible();
		expect(getAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1");
		expect(onVerify).toHaveBeenCalledTimes(1);
	});

	it("cancels a late start response after Settings has unmounted", async () => {
		let finish!: (value: AgentLoginSnapshot) => void;
		vi.mocked(startAgentUILogin).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		const { user, unmount } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		unmount();
		await act(async () => finish(snapshot));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", undefined);
	});

	it("expires a missing login operation and stops polling", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, method: "browser", deviceCode: undefined });
		vi.mocked(getAgentUILogin).mockRejectedValue(Object.assign(new Error("Login no longer exists"), { code: "AGENT_AUTH_OPERATION_NOT_FOUND" }));
		const { user } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		expect(await screen.findByText("Sign-in expired. Start again to get a fresh sign-in link or code.")).toBeVisible();
		expect(screen.getByRole("button", { name: "ChatGPT in browser" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
		await new Promise((resolve) => setTimeout(resolve, 1_100));
		expect(getAgentUILogin).toHaveBeenCalledTimes(1);
	});

	it("retries a transient polling failure and still verifies completion", async () => {
		vi.mocked(getAgentUILogin).mockRejectedValueOnce(new Error("Temporarily unavailable")).mockResolvedValue({ ...snapshot, status: "succeeded" });
		const { user, onVerify } = setup();
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		expect(await screen.findByText("Connected", {}, { timeout: 4_000 })).toBeVisible();
		expect(onVerify).toHaveBeenCalledTimes(1);
	});
});
