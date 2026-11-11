import { act, cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { checkProviderKey, saveProviderKey, useProviderKeys, type ProviderKey } from "../../hooks/useProviderKeys";
import { ApiKeysSection } from "./ApiKeysSection";

vi.mock("../../hooks/useHostConnection", () => ({ useConnectedHosts: vi.fn(() => []) }));

vi.mock("../../hooks/useProviderKeys", () => ({
	useProviderKeys: vi.fn(),
	useInvalidateProviderKeys: () => vi.fn().mockResolvedValue(undefined),
	checkProviderKey: vi.fn(),
	saveProviderKey: vi.fn(),
	removeProviderKey: vi.fn(),
}));

const keys: ProviderKey[] = [
	{ name: "OPENAI_API_KEY", label: "OpenAI", configured: true, hint: "abcd", device: false, deviceAvailable: true, checkable: true, agents: ["goose", "crush"] },
	{ name: "TAVILY_API_KEY", label: "Tavily", configured: false, device: false, deviceAvailable: true, checkable: false, agents: ["goose"] },
];

beforeEach(() => {
	vi.mocked(useProviderKeys).mockReturnValue({ data: keys, isPending: false, isError: false } as unknown as ReturnType<typeof useProviderKeys>);
	vi.mocked(useConnectedHosts).mockReturnValue([]);
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks(); });

describe("ApiKeysSection", () => {
	it("lists every key in one list with who reads it, its saved state and a saved count", () => {
		render(<ApiKeysSection titleHidden />);
		expect(screen.getAllByTestId("settings-section")).toHaveLength(1);
		expect(screen.getByText("1 of 2 saved")).toBeVisible();
		expect(screen.getByTitle("Goose, Crush")).toHaveTextContent("Goose, Crush");
		// A saved key shows its last four; screen readers get the whole state.
		expect(screen.getByText("••••abcd")).toBeVisible();
		expect(screen.getByText("Saved, ending abcd")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Edit · OpenAI" })).toBeVisible();
		expect(screen.getByRole("button", { name: "Add key · Tavily" })).toBeVisible();
		expect(document.querySelector('[data-key-status="saved"]')).not.toBeNull();
		expect(document.querySelector('[data-key-status="unset"]')).not.toBeNull();
	});

	it("shows a check result on the key's row, then brings its saved state back", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		vi.mocked(checkProviderKey).mockResolvedValue({ status: "valid" } as Awaited<ReturnType<typeof checkProviderKey>>);
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Check" }));
		expect(await screen.findByText("The provider accepted this key.")).toHaveAttribute("role", "status");
		await act(async () => { vi.advanceTimersByTime(6_000); });
		expect(screen.queryByText("The provider accepted this key.")).toBeNull();
		expect(screen.getByText("••••abcd")).toBeVisible();
	});

	it("opens an editor with the key field, Cancel and Save on one line and the share opt-in", async () => {
		const user = userEvent.setup();
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Add key · Tavily" }));
		const input = screen.getByLabelText("Tavily");
		expect(input).toHaveAttribute("type", "password");
		expect(input).toHaveAttribute("placeholder", "TAVILY_API_KEY");
		const form = input.closest("form")!;
		expect(within(form).getByRole("checkbox", { name: "Share with other apps" })).not.toBeChecked();
		expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();
		// The editor's own Cancel replaces the row's button while it is open.
		expect(screen.queryByRole("button", { name: "Add key · Tavily" })).toBeNull();
		await user.click(within(form).getByRole("button", { name: "Cancel" }));
		expect(screen.queryByLabelText("Tavily")).toBeNull();
		expect(screen.getByRole("button", { name: "Add key · Tavily" })).toHaveFocus();
	});

	it("offers no share opt-in where the daemon can't share keys outside AO", async () => {
		const user = userEvent.setup();
		vi.mocked(useProviderKeys).mockReturnValue({ data: keys.map((key) => ({ ...key, deviceAvailable: false })), isPending: false, isError: false } as unknown as ReturnType<typeof useProviderKeys>);
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Add key · Tavily" }));
		expect(screen.queryByRole("checkbox", { name: "Share with other apps" })).toBeNull();
	});

	it("explains sharing only in the ⓘ tooltip, without ticking the box", async () => {
		const user = userEvent.setup();
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Add key · Tavily" }));
		expect(screen.queryByText(/Writes the key to your user environment/)).toBeNull();
		await user.click(screen.getByRole("button", { name: /Writes the key to your user environment/ }));
		expect(await screen.findByRole("tooltip")).toHaveTextContent(/Writes the key to your user environment/);
		expect(screen.getByRole("checkbox", { name: "Share with other apps" })).not.toBeChecked();
	});

	it("uses this computer's daemon when no host is chosen", () => {
		render(<ApiKeysSection titleHidden />);
		expect(useProviderKeys).toHaveBeenLastCalledWith(undefined);
		expect(screen.queryByRole("button", { name: "Host" })).toBeNull();
	});

	// Settings opened for a remote host's harness: its keys live on that host.
	it("lists, checks and saves the keys of the host Settings was opened for", async () => {
		const user = userEvent.setup();
		vi.mocked(useConnectedHosts).mockReturnValue(["host-a"]);
		vi.mocked(checkProviderKey).mockResolvedValue({ status: "valid" } as Awaited<ReturnType<typeof checkProviderKey>>);
		vi.mocked(saveProviderKey).mockResolvedValue(keys[1]);
		render(<ApiKeysSection titleHidden hostId="host-a" />);
		expect(useProviderKeys).toHaveBeenLastCalledWith("host-a");
		await user.click(screen.getByRole("button", { name: "Check" }));
		expect(checkProviderKey).toHaveBeenCalledWith("OPENAI_API_KEY", "", "host-a");
		await user.click(screen.getByRole("button", { name: "Add key · Tavily" }));
		await user.type(screen.getByLabelText("Tavily"), "tv-secret");
		await user.click(screen.getByRole("button", { name: "Save" }));
		expect(saveProviderKey).toHaveBeenCalledWith("TAVILY_API_KEY", "tv-secret", false, "host-a");
	});

	it("says so instead of showing this computer's keys when the chosen host is offline", () => {
		render(<ApiKeysSection titleHidden hostId="host-gone" />);
		expect(screen.getByRole("alert")).toHaveTextContent("Host is offline");
		expect(screen.queryByRole("button", { name: "Add key · Tavily" })).toBeNull();
	});
});
