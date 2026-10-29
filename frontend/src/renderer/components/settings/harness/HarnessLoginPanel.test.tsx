import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { actOnAgentUILogin, cancelAgentUILogin, getAgentUILogin, startAgentUILogin, submitAgentUILoginCode, type AgentLoginSnapshot, fetchCatalogProviders, startCatalogKeyLogin } from "../../../hooks/useAgentAuth";
import { aoBridge } from "../../../lib/bridge";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

vi.mock("../../../hooks/useAgentAuth", () => ({ startAgentUILogin: vi.fn(), startCatalogKeyLogin: vi.fn(), fetchCatalogProviders: vi.fn(), getAgentUILogin: vi.fn(), cancelAgentUILogin: vi.fn(), actOnAgentUILogin: vi.fn(), submitAgentUILoginCode: vi.fn() }));

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
	vi.mocked(actOnAgentUILogin).mockResolvedValue(snapshot);
	vi.mocked(submitAgentUILoginCode).mockResolvedValue(snapshot);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); });

describe("HarnessLoginPanel", () => {
	it("saves a provider key through the masked Account form and labels its device scope", async () => {
		const user = userEvent.setup();
		const onVerify = vi.fn().mockResolvedValue("configured");
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId: "goose", method: "env_OPENAI_API_KEY", status: "succeeded", loginUrl: undefined, deviceCode: undefined });
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "OpenAI API key" })).toHaveAccessibleDescription("Not set");
		expect(screen.getByText("AO passes these keys to the agent sessions it starts.")).toBeVisible();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "env_OPENAI_API_KEY", "test-secret", undefined);
		await waitFor(() => expect(onVerify).toHaveBeenCalledWith("env_OPENAI_API_KEY"));
		expect(screen.getByRole("button", { name: "Done" })).toBeVisible();
	});
	it.each(["qwen", "continue", "openhands"] as const)("requires a model for %s provider keys", async (agentId) => {
		const user = userEvent.setup();
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId, method: "env_OPENAI_API_KEY", status: "succeeded" });
		render(<HarnessLoginPanel agentId={agentId} methods={[{ id: agentId === "openhands" ? "env_LLM_API_KEY" : "env_OPENAI_API_KEY", label: "Provider API key", secret: true }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.type(screen.getByLabelText("API key"), "test-secret");
		const save = screen.getByRole("button", { name: "Save and verify" });
		expect(save).toBeDisabled();
		await user.type(screen.getByLabelText("Model"), "provider/model-1");
		await user.click(save);
		expect(startAgentUILogin).toHaveBeenCalledWith(agentId, agentId === "openhands" ? "env_LLM_API_KEY" : "env_OPENAI_API_KEY", "test-secret", undefined, "provider/model-1");
	});
	it("reuses a saved provider key and keeps it in the device environment", async () => {
		const user = userEvent.setup();
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId: "goose", method: "env_OPENAI_API_KEY", status: "succeeded", loginUrl: undefined, deviceCode: undefined });
		const onProviderKeysChanged = vi.fn();
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[{ name: "OPENAI_API_KEY", label: "OpenAI API key", configured: true, hint: "abcd", device: true, checkable: false, agents: ["goose"] }]} onProviderKeysChanged={onProviderKeysChanged} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "OpenAI API key" })).toHaveAccessibleDescription(/Saved, ending abcd · Also set outside AO/);
		expect(screen.getByRole("checkbox", { name: /Also set for apps outside AO/ })).toBeChecked();
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "env_OPENAI_API_KEY", "", undefined, undefined, true);
		await waitFor(() => expect(onProviderKeysChanged).toHaveBeenCalled());
	});
	it("warns that a Claude Code API key can replace the subscription", () => {
		render(<HarnessLoginPanel agentId="claude-code" methods={[{ id: "env_ANTHROPIC_API_KEY", label: "Anthropic API key", secret: true }]} providerKeys={[{ name: "ANTHROPIC_API_KEY", label: "Anthropic API key", configured: false, device: false, checkable: true, agents: ["claude-code", "crush"] }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByText(/instead of your Claude subscription/)).toBeVisible();
		expect(screen.getByRole("checkbox", { name: /Also set for apps outside AO/ })).not.toBeChecked();
	});
	it("offers the harness's provider catalog and needs a provider before saving", async () => {
		const user = userEvent.setup();
		vi.mocked(fetchCatalogProviders).mockResolvedValue([{ id: "zeta", name: "Zeta AI", env: "ZETA_API_KEY", models: 2 }]);
		render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "provider_key", label: "Provider API key", secret: true }, { id: "native", label: "Other native sign-in", secret: false }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await waitFor(() => expect(screen.getByRole("button", { name: "Provider API key" })).toHaveAccessibleDescription(/Choose from 1 providers in OpenCode's catalog/));
		expect(fetchCatalogProviders).toHaveBeenCalledWith("opencode", undefined);
		await user.type(screen.getByLabelText("API key"), "zeta-secret");
		expect(screen.getByRole("button", { name: "Save and verify" })).toBeDisabled();
		expect(startCatalogKeyLogin).not.toHaveBeenCalled();
	});
	it("runs a native login invisibly and presents reviewed menu controls", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "menu", selectedOption: "OpenAI", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		vi.mocked(getAgentUILogin).mockResolvedValue(native);
		vi.mocked(actOnAgentUILogin).mockResolvedValue({ ...native, nativePrompt: "waiting", selectedOption: undefined });
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Native sign-in" })).toBeVisible();
		await user.click(screen.getByRole("button", { name: "Native sign-in" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "native", "", undefined);
		expect(screen.getByText("Selected: OpenAI")).toBeVisible();
		await user.click(screen.getByRole("button", { name: "Next option" }));
		expect(actOnAgentUILogin).toHaveBeenCalledWith("goose", "login-1", "next", undefined);
		expect(screen.queryByText("Selected: OpenAI")).toBeNull();
	});

	it("verifies a native TUI login and stops its hidden PTY while the TUI is still running", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "waiting", loginUrl: undefined, deviceCode: undefined };
		const onVerify = vi.fn().mockResolvedValue(true);
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(screen.getByRole("button", { name: "Native sign-in" }));
		await user.click(screen.getByRole("button", { name: "Check sign-in" }));
		await waitFor(() => expect(cancelAgentUILogin).toHaveBeenCalledWith("goose", "login-1", undefined));
		expect(onVerify).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Done" })).toBeVisible();
	});

	it("offers terminal fallback when a native prompt is unrecognized", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "terminal_required", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		const onTerminal = vi.fn().mockResolvedValue(undefined);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={onTerminal} />);
		await user.click(screen.getByRole("button", { name: "Native sign-in" }));
		expect(screen.getByText("This native screen needs input that AO cannot safely identify.")).toBeVisible();
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("goose", "login-1", undefined);
		expect(onTerminal).toHaveBeenCalledTimes(1);
	});
	it("keeps an interactive-only harness in Account with an explicit terminal action", async () => {
		const onTerminal = vi.fn().mockResolvedValue(undefined);
		const user = userEvent.setup();
		render(<HarnessLoginPanel agentId="goose" methods={[]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={onTerminal} />);
		expect(screen.getByRole("region", { name: "Sign in to Goose" })).toBeVisible();
		expect(screen.getByText(/needs interactive input/)).toBeVisible();
		expect(screen.getByRole("region", { name: "Sign in to Goose" }).querySelector("[data-login-method]")).toBeNull();
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
		expect(onTerminal).toHaveBeenCalledTimes(1);
	});

	it("offers the Cline account browser method without a terminal", () => {
		render(<HarnessLoginPanel agentId="cline" methods={[{ id: "browser", label: "Cline account in browser", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Cline account in browser" })).toBeVisible();
		expect(screen.queryByRole("button", { name: "Continue in browser" })).toBeNull();
	});

	it.each(["kiro", "cursor", "continue", "grok", "command-code"] as const)("offers %s browser sign-in inline", (agentId) => {
		render(<HarnessLoginPanel agentId={agentId} methods={[{ id: "browser", label: `${agentId} account in browser`, secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: `${agentId} account in browser` })).toBeVisible();
		expect(screen.queryByRole("button", { name: "Continue in browser" })).toBeNull();
		expect(screen.queryByText("Use your ChatGPT account.")).toBeNull();
		expect(screen.getByRole("button", { name: "Open terminal login" })).not.toBeVisible();
	});

	it.each(["opencode", "opencode-v2"] as const)("offers %s provider keys as masked inline methods", async (agentId) => {
		const user = userEvent.setup();
		render(<HarnessLoginPanel agentId={agentId} methods={[{ id: "anthropic_key", label: "Anthropic API key", secret: true }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Anthropic API key" })).toBeVisible();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith(agentId, "anthropic_key", "test-secret", undefined);
	});
	it("labels Codewhale's masked provider method with its own credential store", () => {
		render(<HarnessLoginPanel agentId="codewhale" methods={[{ id: "codewhale_deepseek_key", label: "DeepSeek API key", secret: true }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "DeepSeek API key" })).toHaveAccessibleDescription("Save a DeepSeek key in CodeWhale's native credential store.");
		expect(screen.getByLabelText("API key")).toHaveAttribute("type", "password");
	});
	it("saves an Aider provider key directly without opening a terminal", async () => {
		const user = userEvent.setup();
		const methods = [
			{ id: "aider_openai_key", label: "OpenAI API key", secret: true },
			{ id: "aider_anthropic_key", label: "Anthropic API key", secret: true },
			{ id: "aider_openrouter_key", label: "OpenRouter API key", secret: true },
			{ id: "aider_deepseek_key", label: "DeepSeek API key", secret: true },
			{ id: "aider_gemini_key", label: "Gemini API key", secret: true },
		];
		const saved: AgentLoginSnapshot = { ...snapshot, agentId: "aider", method: "aider_openrouter_key", status: "succeeded", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(saved);
		render(<HarnessLoginPanel agentId="aider" methods={methods} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "OpenRouter API key" })).toHaveAccessibleDescription("Save an OpenRouter key in Aider's configuration file.");
		await user.click(screen.getByRole("button", { name: "OpenRouter API key" }));
		expect(startAgentUILogin).not.toHaveBeenCalled();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "sk-or");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("aider", "aider_openrouter_key", "sk-or", undefined);
		expect(await screen.findByText("Configured", {}, { timeout: 3_000 })).toBeVisible();
	});
	it("saves a Google AI Studio key for Gemini alongside native sign-in", async () => {
		const user = userEvent.setup();
		const saved: AgentLoginSnapshot = { ...snapshot, agentId: "gemini", method: "gemini_api_key", status: "succeeded", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(saved);
		render(<HarnessLoginPanel agentId="gemini" methods={[{ id: "gemini_api_key", label: "Google AI Studio API key", secret: true }, { id: "native", label: "Other native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Google AI Studio API key" })).toHaveAccessibleDescription("Save a Google AI Studio key for Gemini CLI.");
		const input = screen.getByLabelText("API key");
		await user.type(input, "sk-gemini");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("gemini", "gemini_api_key", "sk-gemini", undefined);
		expect(await screen.findByText("Configured", {}, { timeout: 3_000 })).toBeVisible();
	});
	it("drives a recognized numbered native menu with one button per option", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "menu", selectedOption: undefined, nativeOptions: ["Anthropic", "OpenAI", "OpenRouter"], loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		vi.mocked(getAgentUILogin).mockResolvedValue(native);
		vi.mocked(actOnAgentUILogin).mockResolvedValue({ ...native, nativePrompt: "waiting", nativeOptions: undefined });
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(screen.getByRole("button", { name: "Native sign-in" }));
		expect(screen.getByRole("button", { name: "2. OpenAI" })).toBeVisible();
		await user.click(screen.getByRole("button", { name: "2. OpenAI" }));
		expect(actOnAgentUILogin).toHaveBeenCalledWith("goose", "login-1", "2", undefined);
	});

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
