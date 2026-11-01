import { act, cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkProviderKey, useProviderKeys, type ProviderKey } from "../../hooks/useProviderKeys";
import { ApiKeysSection } from "./ApiKeysSection";

vi.mock("../../hooks/useProviderKeys", () => ({
	useProviderKeys: vi.fn(),
	useInvalidateProviderKeys: () => vi.fn().mockResolvedValue(undefined),
	checkProviderKey: vi.fn(),
	saveProviderKey: vi.fn(),
	removeProviderKey: vi.fn(),
}));

const keys: ProviderKey[] = [
	{ name: "OPENAI_API_KEY", label: "OpenAI", configured: true, hint: "abcd", device: false, checkable: true, agents: ["goose", "crush"] },
	{ name: "TAVILY_API_KEY", label: "Tavily", configured: false, device: false, checkable: false, agents: ["goose"] },
];

beforeEach(() => {
	vi.mocked(useProviderKeys).mockReturnValue({ data: keys, isPending: false, isError: false } as unknown as ReturnType<typeof useProviderKeys>);
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks(); });

describe("ApiKeysSection", () => {
	it("lists every key in one list with its state and a saved count", () => {
		render(<ApiKeysSection titleHidden />);
		expect(screen.getAllByTestId("settings-section")).toHaveLength(1);
		expect(screen.getByText("1 of 2 saved")).toBeVisible();
		expect(screen.getByText(/Saved, ending abcd · Used by/)).toBeVisible();
		expect(screen.getByText(/Not set · Used by/)).toBeVisible();
		expect(document.querySelector('[data-key-status="saved"]')).not.toBeNull();
		expect(document.querySelector('[data-key-status="unset"]')).not.toBeNull();
	});

	it("adds a check result under the key's state instead of replacing it, then clears it", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		vi.mocked(checkProviderKey).mockResolvedValue({ status: "valid" } as Awaited<ReturnType<typeof checkProviderKey>>);
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Check" }));
		expect(await screen.findByText("The provider accepted this key.")).toHaveAttribute("role", "status");
		expect(screen.getByText(/Saved, ending abcd · Used by/)).toBeVisible();
		await act(async () => { vi.advanceTimersByTime(6_000); });
		expect(screen.queryByText("The provider accepted this key.")).toBeNull();
	});

	it("opens an editor with a labelled key field and the device-environment opt-in", async () => {
		const user = userEvent.setup();
		render(<ApiKeysSection titleHidden />);
		await user.click(screen.getByRole("button", { name: "Add key" }));
		const form = screen.getByLabelText("API key").closest("form")!;
		expect(within(form).getByText("TAVILY_API_KEY")).toBeVisible();
		expect(within(form).getByRole("checkbox", { name: /Also set for apps outside AO/ })).not.toBeChecked();
		expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();
	});
});
