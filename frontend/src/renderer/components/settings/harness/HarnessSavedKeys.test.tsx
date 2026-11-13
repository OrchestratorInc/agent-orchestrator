import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { removeProviderKey, type ProviderKey } from "../../../hooks/useProviderKeys";
import { HarnessSavedKeys } from "./HarnessSavedKeys";

vi.mock("../../../hooks/useProviderKeys", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../../hooks/useProviderKeys")>()), removeProviderKey: vi.fn() }));

const ampKey: ProviderKey = { name: "AMP_API_KEY", label: "Amp API key", configured: true, hint: "abcd", device: true, deviceAvailable: true, checkable: false, agents: ["amp"] };
const openAIKey: ProviderKey = { name: "OPENAI_API_KEY", label: "OpenAI API key", configured: true, hint: "wxyz", device: false, deviceAvailable: true, checkable: true, agents: ["codex", "goose"] };
const unsavedKey: ProviderKey = { ...ampKey, name: "FACTORY_API_KEY", label: "Factory API key", configured: false, hint: undefined, device: false, agents: ["amp"] };

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("HarnessSavedKeys", () => {
	it("lists only the saved keys this harness reads", () => {
		render(<HarnessSavedKeys agentId="amp" providerKeys={[ampKey, openAIKey, unsavedKey]} />);
		expect(screen.getByText("API keys")).toBeVisible();
		expect(screen.getByText("Amp API key")).toBeVisible();
		expect(screen.getByText("Saved, ending abcd · Also set outside AO")).toBeVisible();
		expect(screen.queryByText("OpenAI API key")).toBeNull();
		expect(screen.queryByText("Factory API key")).toBeNull();
	});

	// Gemini's key method saves GEMINI_API_KEY, whose readers don't include Gemini.
	it("lists the GEMINI_API_KEY that Gemini's key method saved", async () => {
		const user = userEvent.setup();
		const geminiKey: ProviderKey = { name: "GEMINI_API_KEY", label: "Gemini API key", configured: true, hint: "wxyz", device: false, deviceAvailable: true, checkable: true, agents: ["opencode", "pi"] };
		vi.mocked(removeProviderKey).mockResolvedValue({ ...geminiKey, configured: false, hint: undefined });
		render(<HarnessSavedKeys agentId="gemini" providerKeys={[geminiKey, openAIKey]} />);
		expect(screen.getByText("Gemini API key")).toBeVisible();
		expect(screen.queryByText("OpenAI API key")).toBeNull();
		await user.click(screen.getByRole("button", { name: "Remove" }));
		await user.click(screen.getByRole("button", { name: "Confirm remove" }));
		expect(removeProviderKey).toHaveBeenCalledWith("GEMINI_API_KEY", undefined);
	});

	it("renders nothing when the harness has no saved key", () => {
		const { container } = render(<HarnessSavedKeys agentId="amp" providerKeys={[openAIKey, unsavedKey]} />);
		expect(container).toBeEmptyDOMElement();
	});

	// A harness that isn't installed has no sign-in panel, so its page is the
	// only place to remove a key saved for it and withdraw AO's shared copy.
	it("removes a saved key only after a second, confirming click", async () => {
		const user = userEvent.setup();
		const onProviderKeysChanged = vi.fn();
		vi.mocked(removeProviderKey).mockResolvedValue({ ...ampKey, configured: false, hint: undefined, device: false });
		const { rerender } = render(<HarnessSavedKeys agentId="amp" hostId="host-1" providerKeys={[ampKey]} onProviderKeysChanged={onProviderKeysChanged} />);
		await user.click(screen.getByRole("button", { name: "Remove" }));
		expect(removeProviderKey).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Confirm remove" }));
		expect(removeProviderKey).toHaveBeenCalledWith("AMP_API_KEY", "host-1");
		expect(onProviderKeysChanged).toHaveBeenCalled();
		// The notice stays once the reloaded list no longer has the key.
		rerender(<HarnessSavedKeys agentId="amp" hostId="host-1" providerKeys={[{ ...ampKey, configured: false }]} onProviderKeysChanged={onProviderKeysChanged} />);
		expect(await screen.findByText("Removed. Copies an agent saved in its own settings stay there.")).toBeVisible();
		expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
	});

	it("warns when AO's copy is still in the user environment", async () => {
		const user = userEvent.setup();
		vi.mocked(removeProviderKey).mockResolvedValue({ ...ampKey, configured: false, withdrawFailed: true });
		render(<HarnessSavedKeys agentId="amp" providerKeys={[ampKey]} />);
		await user.click(screen.getByRole("button", { name: "Remove" }));
		await user.click(screen.getByRole("button", { name: "Confirm remove" }));
		expect(await screen.findByText("Removed, but apps outside AO can still see this key until you log out.")).toBeVisible();
	});

	it("keeps the key and shows why when removal fails", async () => {
		const user = userEvent.setup();
		const onProviderKeysChanged = vi.fn();
		vi.mocked(removeProviderKey).mockRejectedValue(new Error("daemon unavailable"));
		render(<HarnessSavedKeys agentId="amp" providerKeys={[ampKey]} onProviderKeysChanged={onProviderKeysChanged} />);
		await user.click(screen.getByRole("button", { name: "Remove" }));
		await user.click(screen.getByRole("button", { name: "Confirm remove" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("daemon unavailable");
		expect(screen.getByRole("button", { name: "Remove" })).toBeEnabled();
		expect(onProviderKeysChanged).not.toHaveBeenCalled();
	});
});
