import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { actOnAgentUILogin, cancelAgentUILogin, getAgentUILogin, startAgentUILogin, submitAgentUILoginCode, type AgentLoginSnapshot, fetchCatalogProviders, startCatalogKeyLogin } from "../../../hooks/useAgentAuth";
import { isAgentLoginPending } from "../../../lib/agent-login-lifecycle";
import { checkProviderKey, removeProviderKey } from "../../../hooks/useProviderKeys";
import { aoBridge } from "../../../lib/bridge";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

vi.mock("../../../hooks/useProviderKeys", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../../hooks/useProviderKeys")>()), checkProviderKey: vi.fn(), removeProviderKey: vi.fn() }));
vi.mock("../../../hooks/useAgentAuth", () => ({ startAgentUILogin: vi.fn(), startCatalogKeyLogin: vi.fn(), fetchCatalogProviders: vi.fn(), getAgentUILogin: vi.fn(), cancelAgentUILogin: vi.fn(), actOnAgentUILogin: vi.fn(), submitAgentUILoginCode: vi.fn() }));

const methods = [
	{ id: "browser", label: "ChatGPT in browser", secret: false },
	{ id: "device_code", label: "Device code", secret: false },
	{ id: "api_key", label: "OpenAI API key", secret: true },
];
const unsavedKey = (name: string) => ({ name, label: name, configured: false, device: false, deviceAvailable: true, checkable: false, agents: ["goose"] });
const snapshot: AgentLoginSnapshot ={ id: "login-1", agentId: "codex", method: "device_code", status: "running", expiresAt: "2026-10-23T12:00:00Z", loginUrl: "https://auth.openai.com/codex/device", deviceCode: "ABCD-EFGHI" };

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
	vi.mocked(checkProviderKey).mockResolvedValue({ status: "unverified" } as Awaited<ReturnType<typeof checkProviderKey>>);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); });

describe("HarnessLoginPanel", () => {
	it("saves a provider key through the masked Account form and labels its device scope", async () => {
		const user = userEvent.setup();
		const onVerify = vi.fn().mockResolvedValue("configured");
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId: "goose", method: "env_OPENAI_API_KEY", status: "succeeded", loginUrl: undefined, deviceCode: undefined });
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[unsavedKey("OPENAI_API_KEY")]} onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "OpenAI API key" })).toHaveAccessibleDescription("Not set");
		expect(screen.getByText("AO passes these keys to the agent sessions it starts.")).toBeVisible();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		// The device choice shown beside the key is always sent explicitly.
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "env_OPENAI_API_KEY", "test-secret", undefined, undefined, false);
		await waitFor(() => expect(onVerify).toHaveBeenCalledWith("env_OPENAI_API_KEY"));
		expect(screen.getByRole("button", { name: "Done" })).toBeVisible();
	});
	it("waits for the saved keys before saving one, and offers a retry when they fail to load", async () => {
		const user = userEvent.setup();
		const onRetry = vi.fn();
		const keyMethods = [{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }];
		const { rerender } = render(<HarnessLoginPanel agentId="goose" methods={keyMethods} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.type(screen.getByLabelText("API key"), "test-secret");
		expect(screen.getByRole("status")).toHaveTextContent("Loading API keys…");
		expect(screen.getByRole("button", { name: "Save and verify" })).toBeDisabled();
		rerender(<HarnessLoginPanel agentId="goose" methods={keyMethods} providerKeysError="Could not load API keys." onRetryProviderKeys={onRetry} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Could not load API keys.");
		await user.click(screen.getByRole("button", { name: "Retry" }));
		expect(onRetry).toHaveBeenCalled();
		expect(startAgentUILogin).not.toHaveBeenCalled();
	});
	it("keeps the typed key when checking it fails", async () => {
		const user = userEvent.setup();
		vi.mocked(checkProviderKey).mockRejectedValue(new Error("Could not check the key."));
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[{ ...unsavedKey("OPENAI_API_KEY"), checkable: true }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.type(screen.getByLabelText("API key"), "test-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not check the key.");
		expect(screen.getByLabelText("API key")).toHaveValue("test-secret");
		expect(startAgentUILogin).not.toHaveBeenCalled();
	});
	it.each(["qwen", "continue", "openhands"] as const)("requires a model for %s provider keys", async (agentId) => {
		const user = userEvent.setup();
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId, method: "env_OPENAI_API_KEY", status: "succeeded" });
		// Where the daemon can't share keys outside AO, no device setting is sent.
		render(<HarnessLoginPanel agentId={agentId} methods={[{ id: agentId === "openhands" ? "env_LLM_API_KEY" : "env_OPENAI_API_KEY", label: "Provider API key", secret: true }]} providerKeys={[{ ...unsavedKey(agentId === "openhands" ? "LLM_API_KEY" : "OPENAI_API_KEY"), deviceAvailable: false }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.queryByRole("checkbox", { name: "Share with other apps" })).toBeNull();
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
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[{ name: "OPENAI_API_KEY", label: "OpenAI API key", configured: true, hint: "abcd", device: true, deviceAvailable: true, checkable: false, agents: ["goose"] }]} onProviderKeysChanged={onProviderKeysChanged} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "OpenAI API key" })).toHaveAccessibleDescription(/Saved, ending abcd · Also set outside AO/);
		expect(screen.getByRole("checkbox", { name: "Share with other apps" })).toBeChecked();
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "env_OPENAI_API_KEY", "", undefined, undefined, true);
		await waitFor(() => expect(onProviderKeysChanged).toHaveBeenCalled());
	});
	it("warns that a Claude Code API key can replace the subscription", () => {
		render(<HarnessLoginPanel agentId="claude-code" methods={[{ id: "env_ANTHROPIC_API_KEY", label: "Anthropic API key", secret: true }]} providerKeys={[{ name: "ANTHROPIC_API_KEY", label: "Anthropic API key", configured: false, device: false, deviceAvailable: true, checkable: true, agents: ["claude-code", "crush"] }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByText(/instead of your Claude subscription/)).toBeVisible();
		// The billing note is a warning with its icon, not muted help text.
		expect(screen.getByText(/instead of your Claude subscription/).closest("p")?.querySelector("svg.text-warning")).not.toBeNull();
		expect(screen.getByRole("checkbox", { name: "Share with other apps" })).not.toBeChecked();
	});
	it("offers the harness's provider catalog and needs a provider before saving", async () => {
		const user = userEvent.setup();
		vi.mocked(fetchCatalogProviders).mockResolvedValue([{ id: "zeta", name: "Zeta AI", env: "ZETA_API_KEY", models: 2 }]);
		render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "provider_key", label: "Provider API key", secret: true }, { id: "native", label: "Other native sign-in", secret: false }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		// Once the catalog loads, the row says how many providers it offers.
		await waitFor(() => expect(screen.getByRole("button", { name: "Provider API key" })).toHaveAccessibleDescription(/^1 provider/));
		expect(fetchCatalogProviders).toHaveBeenCalledWith("opencode", undefined);
		await user.type(screen.getByLabelText("API key"), "zeta-secret");
		expect(screen.getByRole("button", { name: "Save and verify" })).toBeDisabled();
		expect(startCatalogKeyLogin).not.toHaveBeenCalled();
	});
	it("drops a model list that arrives after switching to another provider", async () => {
		const user = userEvent.setup();
		let answer: (value: Awaited<ReturnType<typeof checkProviderKey>>) => void = () => undefined;
		vi.mocked(checkProviderKey).mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		const saved = (name: string, label: string) => ({ name, label, configured: true, hint: "abcd", device: false, deviceAvailable: true, checkable: true, agents: ["qwen"] });
		render(<HarnessLoginPanel agentId="qwen" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }, { id: "env_ANTHROPIC_API_KEY", label: "Anthropic API key", secret: true }]} providerKeys={[saved("OPENAI_API_KEY", "OpenAI API key"), saved("ANTHROPIC_API_KEY", "Anthropic API key")]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		// OpenAI's form is open first; ask it for models, then switch away.
		await user.click(screen.getByRole("button", { name: "Load models" }));
		expect(checkProviderKey).toHaveBeenCalledWith("OPENAI_API_KEY", "", undefined);
		await user.click(screen.getByRole("button", { name: "Anthropic API key" }));
		await act(async () => answer({ status: "valid", models: ["gpt-4o"] } as Awaited<ReturnType<typeof checkProviderKey>>));
		expect(screen.queryByText("gpt-4o")).toBeNull();
		expect(screen.getByLabelText("Model")).toHaveValue("");
		expect(screen.getByRole("button", { name: "Load models" })).toBeEnabled();
	});
	it("lists API keys before native sign-in, and keeps a real account sign-in first", () => {
		vi.mocked(fetchCatalogProviders).mockReturnValue(new Promise(() => undefined));
		const { unmount } = render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "native", label: "Other native sign-in", secret: false }, { id: "provider_key", label: "Provider API key", secret: true }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		const order = () => Array.from(document.querySelectorAll("[data-login-method]")).map((button) => button.getAttribute("aria-label"));
		// The panel shows its own translated label for the native method.
		expect(order()).toEqual(["Provider API key", "Native sign-in"]);
		unmount();
		render(<HarnessLoginPanel agentId="codex" methods={methods} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(order()[0]).toBe("ChatGPT in browser");
	});
	it("says the catalog is loading once, in the form, while the method keeps a plain description", () => {
		vi.mocked(fetchCatalogProviders).mockReturnValue(new Promise(() => undefined));
		render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "provider_key", label: "Provider API key", secret: true }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Provider API key" })).toHaveAccessibleDescription("Choose a provider from OpenCode's catalog. The key is saved in OpenCode's own settings.");
		expect(screen.getAllByText("Loading OpenCode's providers…")).toHaveLength(1);
	});
	it("warns that a Crush key replaces Crush's own sign-in for that provider", async () => {
		const user = userEvent.setup();
		vi.mocked(fetchCatalogProviders).mockResolvedValue([
			{ id: "openai", name: "OpenAI", env: "OPENAI_API_KEY", models: 30 },
			{ id: "groq", name: "Groq", env: "GROQ_API_KEY", models: 1 },
		]);
		render(<HarnessLoginPanel agentId="crush" methods={[{ id: "provider_key", label: "Provider API key", secret: true }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(await screen.findByRole("button", { name: "Provider" }));
		await user.click(screen.getByRole("menuitem", { name: "Groq" }));
		expect(screen.queryByText(/stored Groq sign-in/)).toBeNull();
		await user.click(screen.getByRole("button", { name: "Provider" }));
		await user.click(screen.getByRole("menuitem", { name: "OpenAI" }));
		expect(screen.getByText(/removes Crush's stored OpenAI sign-in/)).toBeVisible();
	});
	it("says which other agents also get a key saved for Gemini", () => {
		render(<HarnessLoginPanel agentId="gemini" methods={[{ id: "gemini_api_key", label: "Google AI Studio API key", secret: true }]} providerKeys={[{ ...unsavedKey("GEMINI_API_KEY"), agents: ["gemini", "opencode", "pi"] }]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(screen.getByText("AO also gives this key to OpenCode, Pi, which read GEMINI_API_KEY.")).toBeVisible();
	});
	it("clears a key typed for one catalog provider when another is chosen", async () => {
		const user = userEvent.setup();
		vi.mocked(fetchCatalogProviders).mockResolvedValue([
			{ id: "zai", name: "Z.AI", env: "ZAI_API_KEY", models: 14 },
			{ id: "groq", name: "Groq", env: "GROQ_API_KEY", models: 1 },
		]);
		render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "provider_key", label: "Provider API key", secret: true }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(await screen.findByRole("button", { name: "Provider" }));
		await user.click(screen.getByRole("menuitem", { name: "Z.AI" }));
		await user.type(screen.getByLabelText("API key"), "zai-secret");
		await user.click(screen.getByRole("button", { name: "Provider" }));
		await user.click(screen.getByRole("menuitem", { name: "Groq" }));
		expect(screen.getByLabelText("API key")).toHaveValue("");
		expect(screen.getByRole("button", { name: "Save and verify" })).toBeDisabled();
		expect(startCatalogKeyLogin).not.toHaveBeenCalled();
	});
	it("picks a catalog provider from the searchable menu and saves its key", async () => {
		const user = userEvent.setup();
		vi.mocked(fetchCatalogProviders).mockResolvedValue([
			{ id: "zai", name: "Z.AI", env: "ZAI_API_KEY", models: 14 },
			{ id: "zai-coding-plan", name: "Z.AI Coding Plan", env: "ZAI_API_KEY", models: 9 },
			{ id: "groq", name: "Groq", env: "GROQ_API_KEY", models: 1 },
		]);
		vi.mocked(startCatalogKeyLogin).mockResolvedValue({ ...snapshot, agentId: "opencode", method: "provider_key", status: "succeeded", loginUrl: undefined, deviceCode: undefined });
		render(<HarnessLoginPanel agentId="opencode" methods={[{ id: "provider_key", label: "Provider API key", secret: true }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(await screen.findByRole("button", { name: "Provider" }));
		expect(screen.getAllByRole("menuitem")).toHaveLength(3);
		await user.type(screen.getByRole("searchbox", { name: "Search provider" }), "coding");
		expect(screen.getAllByRole("menuitem")).toHaveLength(1);
		await user.click(screen.getByRole("menuitem", { name: "Z.AI Coding Plan" }));
		expect(screen.getByRole("button", { name: "Provider" })).toHaveTextContent("Z.AI Coding Plan");
		// The chosen provider's key variable is the key field's placeholder.
		expect(screen.getByLabelText("API key")).toHaveAttribute("placeholder", "ZAI_API_KEY");
		await user.click(screen.getByRole("button", { name: "Provider" }));
		await user.click(screen.getByRole("menuitem", { name: "Groq" }));
		await user.type(screen.getByLabelText("API key"), "groq-secret");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startCatalogKeyLogin).toHaveBeenCalledWith("opencode", "groq", "groq-secret", undefined);
	});
	it("runs a native login invisibly and presents reviewed menu controls", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "menu", selectedOption: "OpenAI", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		vi.mocked(getAgentUILogin).mockResolvedValue(native);
		vi.mocked(actOnAgentUILogin).mockResolvedValue({ ...native, nativePrompt: "waiting", selectedOption: undefined });
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		// Native sign-in is the only method, so it starts without a click.
		expect(await screen.findByText("Selected: OpenAI")).toBeVisible();
		expect(startAgentUILogin).toHaveBeenCalledTimes(1);
		expect(startAgentUILogin).toHaveBeenCalledWith("goose", "native", "", undefined);
		await user.click(screen.getByRole("button", { name: "Next option" }));
		expect(actOnAgentUILogin).toHaveBeenCalledWith("goose", "login-1", "next", undefined);
		expect(screen.queryByText("Selected: OpenAI")).toBeNull();
	});

	it("offers the terminal when a native screen stays unrecognized", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "waiting", loginUrl: undefined, deviceCode: undefined };
			vi.mocked(startAgentUILogin).mockResolvedValue(native);
			vi.mocked(getAgentUILogin).mockResolvedValue(native);
			const onTerminal = vi.fn();
			render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={onTerminal} />);
			expect(await screen.findByText("Waiting for the native sign-in flow…")).toBeVisible();
			await act(async () => { await vi.advanceTimersByTimeAsync(13_000); });
			expect(screen.getByText("AO can't read this sign-in screen. Continue in the terminal login.")).toBeVisible();
			// The terminal becomes the one main action; AO does not leave on its own.
			expect(screen.getAllByRole("button", { name: "Open terminal login" })).toHaveLength(1);
			expect(onTerminal).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("verifies a native TUI login and stops its hidden PTY while the TUI is still running", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "waiting", loginUrl: undefined, deviceCode: undefined };
		const onVerify = vi.fn().mockResolvedValue(true);
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(await screen.findByRole("button", { name: "Check sign-in" }));
		await waitFor(() => expect(cancelAgentUILogin).toHaveBeenCalledWith("goose", "login-1", undefined));
		expect(onVerify).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Done" })).toBeVisible();
	});

	// Gemini, Hermes and MiMo Code can only report their own credentials as
	// present: the sign-in finishes as configured, never as verified.
	it("finishes a native sign-in the harness can only report as configured", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "hermes", method: "native", nativePrompt: "waiting", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		render(<HarnessLoginPanel agentId="hermes" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.click(await screen.findByRole("button", { name: "Check sign-in" }));
		await waitFor(() => expect(cancelAgentUILogin).toHaveBeenCalledWith("hermes", "login-1", undefined));
		expect(await screen.findByText("Configured")).toBeVisible();
		expect(screen.queryByText("Connected")).toBeNull();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("hands an unrecognized first native screen straight to the terminal", async () => {
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "terminal_required", loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		const onTerminal = vi.fn().mockResolvedValue(undefined);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={onTerminal} />);
		await waitFor(() => expect(onTerminal).toHaveBeenCalledTimes(1));
		expect(cancelAgentUILogin).toHaveBeenCalledWith("goose", "login-1", undefined);
	});
	it("offers the terminal as the main action when a native screen goes unrecognized mid-flow", async () => {
		const user = userEvent.setup();
		const menu: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "menu", nativeOptions: ["Anthropic", "OpenAI"], loginUrl: undefined, deviceCode: undefined };
		const stuck: AgentLoginSnapshot = { ...menu, nativePrompt: "terminal_required", nativeOptions: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(menu);
		vi.mocked(getAgentUILogin).mockResolvedValue(menu);
		vi.mocked(actOnAgentUILogin).mockImplementation(async () => { vi.mocked(getAgentUILogin).mockResolvedValue(stuck); return stuck; });
		const onTerminal = vi.fn().mockResolvedValue(undefined);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={onTerminal} />);
		await user.click(await screen.findByRole("button", { name: "2. OpenAI" }));
		expect(await screen.findByText("This step needs the terminal. Open the terminal login to continue.")).toBeVisible();
		expect(screen.queryByRole("button", { name: "Check sign-in" })).toBeNull();
		expect(onTerminal).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Open terminal login" }));
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
		expect(screen.getByRole("button", { name: "Open terminal login" })).toBeVisible();
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
		expect(screen.getByRole("button", { name: "DeepSeek API key" })).toHaveAccessibleDescription("Saved in CodeWhale's own credential store.");
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
		expect(screen.getByRole("button", { name: "OpenRouter API key" })).toHaveAccessibleDescription("Saved in Aider's configuration file.");
		await user.click(screen.getByRole("button", { name: "OpenRouter API key" }));
		expect(startAgentUILogin).not.toHaveBeenCalled();
		const input = screen.getByLabelText("API key");
		expect(input).toHaveAttribute("type", "password");
		await user.type(input, "sk-or");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("aider", "aider_openrouter_key", "sk-or", undefined);
		expect(await screen.findByText("Configured", {}, { timeout: 3_000 })).toBeVisible();
	});
	// Keys are set here, so this is where the user hears that turning sharing
	// off left AO's copy in the user environment.
	it("warns when a saved key is still in the user environment", async () => {
		const user = userEvent.setup();
		const saved: AgentLoginSnapshot = { ...snapshot, agentId: "gemini", method: "gemini_api_key", status: "succeeded", loginUrl: undefined, deviceCode: undefined, withdrawFailed: true };
		vi.mocked(startAgentUILogin).mockResolvedValue(saved);
		render(<HarnessLoginPanel agentId="gemini" methods={[{ id: "gemini_api_key", label: "Google AI Studio API key", secret: true }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.type(screen.getByLabelText("API key"), "sk-gemini");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(await screen.findByText("Saved, but apps outside AO can still see this key until you log out.")).toBeVisible();
	});
	it("says nothing about the user environment when the key left it", async () => {
		const user = userEvent.setup();
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId: "gemini", method: "gemini_api_key", status: "succeeded", loginUrl: undefined, deviceCode: undefined });
		render(<HarnessLoginPanel agentId="gemini" methods={[{ id: "gemini_api_key", label: "Google AI Studio API key", secret: true }]} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await user.type(screen.getByLabelText("API key"), "sk-gemini");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(await screen.findByText("Configured", {}, { timeout: 3_000 })).toBeVisible();
		expect(screen.queryByText(/apps outside AO can still see this key/)).toBeNull();
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
		expect(await screen.findByRole("button", { name: "2. OpenAI" })).toBeVisible();
		await user.click(screen.getByRole("button", { name: "2. OpenAI" }));
		expect(actOnAgentUILogin).toHaveBeenCalledWith("goose", "login-1", "2", undefined, "OpenAI");
	});
	it("offers Enter separately for a numbered menu that still waits after a choice", async () => {
		const user = userEvent.setup();
		const native: AgentLoginSnapshot = { ...snapshot, agentId: "goose", method: "native", nativePrompt: "menu", nativeOptions: ["Anthropic", "OpenAI"], loginUrl: undefined, deviceCode: undefined };
		vi.mocked(startAgentUILogin).mockResolvedValue(native);
		vi.mocked(getAgentUILogin).mockResolvedValue(native);
		render(<HarnessLoginPanel agentId="goose" methods={[{ id: "native", label: "Native sign-in", secret: false }]} onVerify={vi.fn().mockResolvedValue(false)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		expect(await screen.findByRole("group", { name: "Choose how to continue:" })).toBeVisible();
		await user.click(screen.getByRole("button", { name: "Press Enter" }));
		expect(actOnAgentUILogin).toHaveBeenCalledWith("goose", "login-1", "select", undefined);
	});

	it.each([undefined, "local"])("explains local sign-in methods and offers terminal login beside Cancel (%s)", (hostId) => {
		setup(hostId);
		expect(screen.getByRole("button", { name: "ChatGPT in browser" })).toHaveAccessibleDescription("Use your ChatGPT account.");
		expect(screen.getByRole("button", { name: "ChatGPT in browser" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: "Continue in browser" })).toBeNull();
		expect(screen.getByRole("button", { name: "Open terminal login" })).toBeVisible();
		expect(screen.getByRole("button", { name: "Cancel" })).toBeVisible();
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

	it("saves a masked Claude setup token and verifies it like other sign-ins", async () => {
		vi.mocked(startAgentUILogin).mockResolvedValue({ ...snapshot, agentId: "claude-code", method: "setup_token", loginUrl: undefined, deviceCode: undefined, status: "succeeded" });
		const onVerify = vi.fn().mockResolvedValue(true);
		render(<HarnessLoginPanel agentId="claude-code" methods={[
			{ id: "browser", label: "Claude subscription", secret: false },
			{ id: "setup_token", label: "Setup token", secret: true },
		]} onVerify={onVerify} onClose={vi.fn()} onTerminal={vi.fn()} />);
		const user = userEvent.setup();
		const method = screen.getByRole("button", { name: "Setup token" });
		expect(method).toHaveAccessibleDescription("Paste a token from claude setup-token.");
		await user.click(method);
		expect(startAgentUILogin).not.toHaveBeenCalled();
		const input = screen.getByPlaceholderText("sk-ant-oat01-…");
		expect(input).toHaveAttribute("type", "password");
		expect(input).toHaveAccessibleName("Setup token");
		await user.type(input, "sk-ant-oat01-test-setup-token");
		await user.click(screen.getByRole("button", { name: "Save and verify" }));
		expect(startAgentUILogin).toHaveBeenCalledWith("claude-code", "setup_token", "sk-ant-oat01-test-setup-token", undefined);
		await waitFor(() => expect(onVerify).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Connected")).toBeVisible();
		expect(document.body).not.toHaveTextContent("sk-ant-oat01-test-setup-token");
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

	it("keeps the harness pending when unmount cancellation cannot reach the host", async () => {
		vi.mocked(cancelAgentUILogin).mockRejectedValue(new Error("Host is disconnected"));
		const { user, unmount } = setup("remote-1");
		await user.click(screen.getByRole("button", { name: "Device code" }));
		await screen.findByText("ABCD-EFGHI");
		unmount();
		await waitFor(() => expect(cancelAgentUILogin).toHaveBeenCalledWith("codex", "login-1", "remote-1"));
		await act(async () => { await Promise.resolve(); });
		expect(isAgentLoginPending("codex", "remote-1")).toBe(true);
	});

	it("does not open a recovered remote Codex browser login on this computer", async () => {
		const browser: AgentLoginSnapshot = { ...snapshot, method: "browser", deviceCode: undefined, loginUrl: "https://auth.openai.com/oauth/authorize" };
		vi.mocked(startAgentUILogin).mockResolvedValue(browser);
		vi.mocked(getAgentUILogin).mockResolvedValue(browser);
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const { user } = setup("remote-1");
		await user.click(screen.getByRole("button", { name: "Device code" }));
		expect(await screen.findByText("Complete sign-in in your browser. Waiting for approval…")).toBeVisible();
		expect(open).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
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

	// Keys are set here, so they are removed here too: AO stops passing the key
	// to sessions and takes its copy out of the user environment.
	describe("removing a saved key", () => {
		const savedOpenAIKey = { name: "OPENAI_API_KEY", label: "OpenAI API key", configured: true, hint: "abcd", device: true, deviceAvailable: true, checkable: false, agents: ["goose"] };
		const renderSaved = (onProviderKeysChanged = vi.fn()) => {
			render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[savedOpenAIKey]} onProviderKeysChanged={onProviderKeysChanged} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
			return onProviderKeysChanged;
		};

		it("removes it only after a second, confirming click", async () => {
			const user = userEvent.setup();
			vi.mocked(removeProviderKey).mockResolvedValue({ ...savedOpenAIKey, configured: false, hint: undefined, device: false });
			const onProviderKeysChanged = renderSaved();
			await user.click(await screen.findByRole("button", { name: "Remove" }));
			expect(removeProviderKey).not.toHaveBeenCalled();
			await user.click(screen.getByRole("button", { name: "Confirm remove" }));
			expect(removeProviderKey).toHaveBeenCalledWith("OPENAI_API_KEY", undefined);
			expect(await screen.findByText("Removed. Copies an agent saved in its own settings stay there.")).toBeVisible();
			expect(onProviderKeysChanged).toHaveBeenCalled();
		});

		it("warns when AO's copy is still in the user environment", async () => {
			const user = userEvent.setup();
			vi.mocked(removeProviderKey).mockResolvedValue({ ...savedOpenAIKey, configured: false, hint: undefined, device: false, withdrawFailed: true });
			renderSaved();
			await user.click(await screen.findByRole("button", { name: "Remove" }));
			await user.click(screen.getByRole("button", { name: "Confirm remove" }));
			expect(await screen.findByText("Removed, but apps outside AO can still see this key until you log out.")).toBeVisible();
		});

		// Gemini's own key method also saves GEMINI_API_KEY in AO's store, so it
		// is removed from here as well, without offering sharing it does not take.
		it("removes the GEMINI_API_KEY that Gemini's key method saved in AO", async () => {
			const user = userEvent.setup();
			const savedGeminiKey = { name: "GEMINI_API_KEY", label: "Gemini API key", configured: true, hint: "wxyz", device: false, deviceAvailable: true, checkable: true, agents: ["gemini", "opencode"] };
			vi.mocked(removeProviderKey).mockResolvedValue({ ...savedGeminiKey, configured: false, hint: undefined });
			const onProviderKeysChanged = vi.fn();
			render(<HarnessLoginPanel agentId="gemini" methods={[{ id: "gemini_api_key", label: "Google AI Studio API key", secret: true }, { id: "native", label: "Other native sign-in", secret: false }]} providerKeys={[savedGeminiKey]} onProviderKeysChanged={onProviderKeysChanged} onVerify={vi.fn().mockResolvedValue("configured")} onClose={vi.fn()} onTerminal={vi.fn()} />);
			expect(screen.queryByRole("checkbox", { name: "Share with other apps" })).toBeNull();
			await user.click(await screen.findByRole("button", { name: "Remove" }));
			await user.click(screen.getByRole("button", { name: "Confirm remove" }));
			expect(removeProviderKey).toHaveBeenCalledWith("GEMINI_API_KEY", undefined);
			expect(await screen.findByText("Removed. Copies an agent saved in its own settings stay there.")).toBeVisible();
			expect(onProviderKeysChanged).toHaveBeenCalled();
		});

		it("offers no removal while no key is saved", async () => {
			render(<HarnessLoginPanel agentId="goose" methods={[{ id: "env_OPENAI_API_KEY", label: "OpenAI API key", secret: true }]} providerKeys={[unsavedKey("OPENAI_API_KEY")]} onVerify={vi.fn()} onClose={vi.fn()} onTerminal={vi.fn()} />);
			expect(await screen.findByLabelText("API key")).toBeVisible();
			expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
		});
	});
});
