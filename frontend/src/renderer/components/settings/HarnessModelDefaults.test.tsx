import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { HarnessModelDefaults } from "./HarnessModelDefaults";

const mocks = vi.hoisted(() => ({ update: vi.fn(), settings: vi.fn(), mutation: vi.fn() }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: mocks.settings, useUpdateHarnessDefault: mocks.mutation }));

const models = [
	{ id: "alpha/old", label: "Old model", provider: "alpha" },
	{ id: "beta/org/new", label: "New model", provider: "beta", isDefault: true },
];
const catalog: AgentModelCatalog = {
	agentId: "pi", models, selectionMode: "catalog", customModelEntry: "direct", allowCustom: true,
	source: "cli", fetchedAt: "2026-10-13T00:00:00Z", stale: false,
};

describe("HarnessModelDefaults provider selection", () => {
	beforeEach(() => {
		mocks.update.mockReset().mockResolvedValue(undefined);
		mocks.settings.mockReset().mockReturnValue({ settings: { harnessDefaults: { pi: { model: "alpha/old" } } }, isLoading: false });
		mocks.mutation.mockReset().mockReturnValue({ update: mocks.update, saving: false });
	});

	it("preserves the saved model while browsing and saves the exact chosen native ID, including a harness default", async () => {
		const props = { agentId: "pi", agentLabel: "Pi", hostId: "remote-1", catalog };
		const view = render(<HarnessModelDefaults {...props} selectableModels={[models[0]]} />);
		expect(screen.getByRole("button", { name: "Default model" })).toHaveTextContent("Old model");
		view.rerender(<HarnessModelDefaults {...props} selectableModels={[models[1]]} />);
		expect(screen.getByText("Current selection: Old model")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save defaults" })).toBeDisabled();
		expect(mocks.update).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Default model" }));
		await userEvent.click(screen.getByRole("menuitem", { name: /New model/ }));
		expect(mocks.update).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Save defaults" }));
		expect(mocks.update).toHaveBeenCalledWith({ agentId: "pi", model: "beta/org/new", effort: "" });
		expect(mocks.settings).toHaveBeenCalledWith("remote-1");
		expect(mocks.mutation).toHaveBeenCalledWith("remote-1");
	});
});
