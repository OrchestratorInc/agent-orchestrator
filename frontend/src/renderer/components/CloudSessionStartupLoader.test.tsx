import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudSessionStartupLoader } from "./CloudSessionStartupLoader";
import { TooltipProvider } from "./ui/tooltip";

const workspaceUrl = "https://coder.acme.example.com/@dev/dev-3f2a9c1e7b4d";

function mount(props: Parameters<typeof CloudSessionStartupLoader>[0]) {
	return render(<TooltipProvider><CloudSessionStartupLoader {...props} /></TooltipProvider>);
}

describe("CloudSessionStartupLoader", () => {
	afterEach(() => vi.restoreAllMocks());

	it("links the bring-your-own Coder workspace beside the workspace step", async () => {
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		mount({ activeIndex: 0, coderWorkspaceUrl: workspaceUrl });
		const workspaceStep = screen.getByText("Creating the workspace").closest("li");
		expect(workspaceStep).not.toBeNull();
		const link = within(workspaceStep!).getByRole("button", { name: "Open the workspace in Coder" });
		await userEvent.click(link);
		expect(openExternal).toHaveBeenCalledWith(workspaceUrl);
		expect(screen.getAllByRole("button", { name: "Open the workspace in Coder" })).toHaveLength(1);
	});

	it("keeps the link after the workspace step completes", () => {
		mount({ activeIndex: 2, coderWorkspaceUrl: workspaceUrl });
		const workspaceStep = screen.getByText("Creating the workspace").closest("li");
		expect(within(workspaceStep!).getByRole("button", { name: "Open the workspace in Coder" })).toBeInTheDocument();
	});

	it("shows no link without a workspace URL or for a non-web URL", () => {
		const view = mount({ activeIndex: 1 });
		expect(screen.queryByTestId("coder-workspace-link")).toBeNull();
		view.unmount();
		mount({ activeIndex: 1, coderWorkspaceUrl: "javascript:alert(1)" });
		expect(screen.queryByTestId("coder-workspace-link")).toBeNull();
	});
});
